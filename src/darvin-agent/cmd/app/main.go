package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agentloop"
	"darvin-cowork/backend/internal/agents"
	"darvin-cowork/backend/internal/agents/session"
	"darvin-cowork/backend/internal/agents/store"
	"darvin-cowork/backend/internal/config"
	"darvin-cowork/backend/internal/database"
	"darvin-cowork/backend/internal/gateway"
	"darvin-cowork/backend/internal/harness"
	"darvin-cowork/backend/internal/llm"
	"darvin-cowork/backend/internal/logger"
	"darvin-cowork/backend/internal/mcp"
	"darvin-cowork/backend/internal/skills"
	"darvin-cowork/backend/internal/tools"

	// Blank import triggers anthropic.init() which registers the provider
	// with llm.NewProvider's name-based factory registry.
	_ "darvin-cowork/backend/internal/llm/anthropic"
)

// configPath resolves config.yaml in three places, in order:
//  1. $DARVIN_CONFIG, if set (lets Electron point at a project-local file)
//  2. <exe-dir>/config.yaml, the production layout where the binary
//     and the file ship side-by-side
//  3. "config.yaml" — relative to the Go agent's cwd, for `go run`
func configPath() string {
	if p := os.Getenv("DARVIN_CONFIG"); p != "" {
		return p
	}
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), "config.yaml")
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	return "config.yaml"
}

func main() {
	cfg, err := config.Load(configPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	logCfg := &logger.Config{
		Level:      cfg.Log.Level,
		Encoding:   cfg.Log.Encoding,
		Output:     cfg.Log.Output,
		Filename:   cfg.Log.Filename,
		MaxSize:    cfg.Log.MaxSize,
		MaxBackups: cfg.Log.MaxBackups,
		MaxAge:     cfg.Log.MaxAge,
	}
	if err := logger.Init(logCfg); err != nil {
		fmt.Fprintf(os.Stderr, "failed to init logger: %v\n", err)
		os.Exit(1)
	}
	log := logger.Get()
	defer log.Sync()

	// signal.NotifyContext turns SIGINT/SIGTERM into a context
	// cancellation; everything downstream (Gateway Serve loop, the
	// per-connection ping loop) watches ctx and exits gracefully.
	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Info("starting application", zap.String("name", cfg.App.Name), zap.String("env", cfg.App.Env))

	dsn, err := config.ResolveSessionsDSN(cfg.Database.SessionsDSN)
	if err != nil {
		log.Error("failed to resolve sessions dsn", zap.Error(err))
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(dsn), 0o700); err != nil {
		log.Error("failed to create sessions dir", zap.Error(err))
		os.Exit(1)
	}
	dbCfg := &database.Config{
		SessionsDSN: dsn,
	}
	if err := database.Init(dbCfg); err != nil {
		log.Error("failed to init database", zap.Error(err))
		os.Exit(1)
	}
	log.Info("database initialized", zap.String("sessions_dsn", dsn))

	if err := database.AutoMigrate(
		&store.Session{},
		&store.Message{},
		&store.CompactionCheckpoint{},
		&store.SkillSnapshot{},
		&store.AppState{},
		&store.ImportedFile{},
	); err != nil {
		log.Error("auto migrate failed", zap.Error(err))
		os.Exit(1)
	}
	log.Info("database migrated")

	sqliteStore := store.NewSQLiteStore(database.Get())
	msgStore := store.NewSQLiteMessageStore(database.Get())
	appState := store.NewAppStateStore(database.Get())
	importedFiles := store.NewImportedFileStore(database.Get())

	provider, err := llm.NewProvider(rootCtx, cfg.LLM.Provider, llm.ProviderConfig{
		APIKey:  cfg.LLM.APIKey,
		BaseURL: cfg.LLM.BaseURL,
	})
	if err != nil {
		log.Error("failed to construct LLM provider", zap.String("provider", cfg.LLM.Provider), zap.Error(err))
		os.Exit(1)
	}

	// workspace 根优先取 Electron 注入的 DARVIN_AGENT_WORKSPACE(即 fsSandbox.root);
	// 未设置时退回 config.yaml 的 workdir(dev `go run` 兜底)。日志同时打 env 与
	// effective 两值,便于排查 env 与 main 端计算不一致。
	workspaceRoot := os.Getenv("DARVIN_AGENT_WORKSPACE")
	effectiveWorkdir := workspaceRoot
	if effectiveWorkdir == "" {
		effectiveWorkdir = cfg.Agent.Workdir
	}
	log.Info("workspace resolved",
		zap.String("env", workspaceRoot),
		zap.String("effective", effectiveWorkdir))

	agentCfg := agent.Config{
		MaxTurns:             cfg.Agent.MaxTurns,
		ToolTimeout:          time.Duration(cfg.Agent.ToolTimeoutMS) * time.Millisecond,
		Workdir:              effectiveWorkdir,
		ShellAllowlist:       cfg.Agent.ShellAllowlist,
		EventBuffer:          cfg.Agent.EventBuffer,
		TokenBudget:          cfg.Agent.TokenBudget,
		CompactTailKeep:      cfg.Agent.CompactTailKeep,
		ToolResultMaxBytes:   cfg.Agent.ToolResultMaxBytes,
		CompactMaxRetries:    cfg.Agent.CompactMaxRetries,
		SummarizeMaxTokens:   cfg.Agent.SummarizeMaxTokens,
		SystemPromptAddition: cfg.Agent.SystemPromptAddition,
		AssemblerEnabled:     cfg.Agent.AssemblerEnabled,
	}

	// Bootstrap the built-in tool registry once; the steer agent, the
	// per-session factory and the skills runner all share it.
	toolsReg, toolsErr := tool.NewBuiltins(effectiveWorkdir, cfg.Agent.ShellAllowlist)
	if toolsErr != nil {
		log.Warn("skills: tool registry init failed, using empty registry", zap.Error(toolsErr))
		toolsReg = tool.NewRegistry()
	}

	// Steer 仍接单例 Agent(本期不迁)。这个 steerAgent
	// 仅给 agentloop.NewSteerControl 持有,不会被任何 Loop 驱动、也不会订阅事件
	// —— UI 本期不发 steer message,实际不影响行为。
	steerAgent, err := agent.New(agent.NewAgentConfig{
		Name:             cfg.App.Name + "-agent",
		Instructions:     cfg.Agent.Instructions,
		Model:            agent.ModelRef{Provider: cfg.Agent.ProviderName, Model: cfg.Agent.Model},
		Provider:         provider,
		Session:          session.NewSession("steer-placeholder"),
		Store:            sqliteStore,
		MessageStore:     msgStore,
		Logger:           log.Logger,
		Config:           agentCfg,
		Tools:            toolsReg,
		AssemblerEnabled: cfg.Agent.AssemblerEnabled,
	})
	if err != nil {
		log.Error("failed to construct steer agent", zap.Error(err))
		os.Exit(1)
	}

	// Register the in-process harness so resolveHarness can find
	// it. The Run closure drives the per-session agent the factory builds;
	// the harness itself never imports internal/agents.
	harness.MustRegister(harness.NewEmbedded(harness.EmbeddedConfig{
		Run: func(ctx context.Context, p harness.RunAttemptParams) (*harness.AttemptResult, error) {
			// The factory Selector (below) is what actually maps a session
			// to its agent; the registry entry here is a fallback target for
			// selection scoring. Runtime prompt execution goes through the
			// factory's per-agent harness closure.
			return nil, harness.ErrNotImplemented
		},
	}), "")

	factory := &agentloop.AgentFactory{
		Name:             cfg.App.Name + "-agent",
		Instructions:     cfg.Agent.Instructions,
		Model:            agent.ModelRef{Provider: cfg.Agent.ProviderName, Model: cfg.Agent.Model},
		Provider:         provider,
		Store:            sqliteStore,
		MessageStore:     msgStore,
		Logger:           log.Logger,
		Config:           agentCfg,
		Tools:            toolsReg,
		AssemblerEnabled: cfg.Agent.AssemblerEnabled,
		Selector: func(a *agent.Agent, _ *agentloop.AgentFactory) (harness.Harness, error) {
			return harness.NewEmbedded(harness.EmbeddedConfig{
				Run: func(ctx context.Context, p harness.RunAttemptParams) (*harness.AttemptResult, error) {
					if err := a.Prompt(ctx, p.Prompt, nil, p.Attachments); err != nil {
						return nil, err
					}
					_ = a.Run(ctx)
					return &harness.AttemptResult{Status: harness.AttemptOK}, nil
				},
			}), nil
		},
	}

	// Bootstrap skills registry + runner. The runner resolves skill
	// execution contexts against toolsReg (built-ins only); per-session
	// skill/mcp tools are added by the plugins wired onto the factory below.
	skillsResult := skills.Bootstrap(rootCtx, log.Logger, skills.BootstrapConfig{
		UserDataDir: effectiveWorkdir,
		ToolReg:     toolsReg,
	})

	ledger := gateway.NewEventLedger(log.Logger)
	sessions := gateway.NewSessionManager(
		gateway.WithAgentFactory(factory),
		gateway.WithEventLedger(ledger),
	)
	steer := agentloop.NewSteerControl(steerAgent)

	// FR-9:启动期 active session 同步 —— 从 app_state 读出上次的
	// active_session_id,灌进 SessionManager。EnsureEntry 只建轻量
	// SessionEntry、不触发 AgentLoopSession 懒建(per-session spec 两阶段),
	// 事件流由 main 端 connect → subscribeAllSessions 时覆盖。
	if activeID, err := appState.GetActiveSession(rootCtx); err == nil && activeID != "" {
		if _, err := sessions.EnsureEntry(activeID); err != nil {
			log.Warn("bootstrap active session failed", zap.String("session_id", activeID), zap.Error(err))
		} else {
			log.Info("bootstrapped active session", zap.String("session_id", activeID))
		}
	}

	// MCP registry: 注册 bundled filesystem (走自身二进制
	// 的 mcp-filesystem subcommand) + 启动期 LoadStaleResolutions 扫上
	// 次卡 installing 状态。SQLite 持久化由 main 端接管,
	// Go 端目前用 in-memory + main 端 push resolution_changed 落库。
	mcpRoot := filepath.Join(effectiveWorkdir, "mcp-packages")
	if err := os.MkdirAll(mcpRoot, 0o755); err != nil {
		log.Warn("mcp packages dir create failed", zap.Error(err))
	}
	mcpResolver := mcp.NewResolverManager(mcpRoot).WithLogger(log.Logger)
	mcpRegistry := mcp.NewRegistry(mcpResolver, mcp.NewInMemoryResolutionPersistence()).WithLogger(log.Logger)
	if err := mcpRegistry.LoadStaleResolutions(rootCtx); err != nil {
		log.Warn("mcp stale resolution scan failed", zap.Error(err))
	}

	handler := gateway.NewHandler(sessions, ledger, steer, sqliteStore, msgStore, appState,
		gateway.HandlerOptions{
			ImportedFiles: importedFiles,
			WorkspaceRoot: effectiveWorkdir,
			Skills:        skillsResult.Registry,
			SkillRunner:   skillsResult.Runner,
			Mcp:           mcpRegistry,
			Log:           log.Logger,
		})
	// mcp registry → handler 回调：connectServer / Unregister / SetEnabled /
	// Update 触发的状态变化通过 ledger 广播成 mcp.connection_changed 与
	// mcp.resolution_changed 通知，main 端 mcpManager 订阅后落 SQLite。
	mcpRegistry.SetNotifier(mcp.Notifier{
		OnConnectionChanged: handler.OnMcpConnectionChanged,
		OnResolutionChanged: handler.OnMcpResolutionChanged,
	})

	// skill / mcp 插件注入 factory：每个新 AgentLoopSession 建好后工具面自动带上
	// skill__<id> 与 mcp__<server>__<tool>（双下划线命名空间，符合 Anthropic
	// 工具名 ^[a-zA-Z0-9_-]+$ 约束）。skill 启停与 mcp 连接变化由
	// handler 的 RefreshAllTools 重跑这两组插件（见 gateway 侧钩子）。
	skillPlugin := skills.NewSkillPlugin(skillsResult.Registry, skillsResult.Runner)
	mcpPlugin := tool.NewMcpPlugin(mcpRegistry)
	factory.Plugins = []tool.Plugin{skillPlugin, mcpPlugin}

	gs := gateway.NewServer(handler, log.Logger)
	if err := gs.Start(rootCtx); err != nil {
		log.Error("gateway start failed", zap.Error(err))
		os.Exit(1)
	}

	log.Info("application started successfully", zap.Int("port", gs.Port()))

	// Block until SIGINT/SIGTERM. The 3s budget covers the four-step
	// shutdown below; in practice the close is sub-200ms without
	// in-flight runs.
	<-rootCtx.Done()
	log.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := gs.Shutdown(shutdownCtx); err != nil {
		log.Error("gateway shutdown", zap.Error(err))
	}

	// dispose every registered harness so a plugin harness's
	// process-level resources are released. One harness failing does not
	// block the others.
	if err := harness.DisposeAll(shutdownCtx); err != nil {
		log.Warn("harness dispose", zap.Error(err))
	}

	if err := sqliteStore.Close(); err != nil {
		log.Error("sqlite close", zap.Error(err))
	}

	log.Info("graceful shutdown complete")
}
