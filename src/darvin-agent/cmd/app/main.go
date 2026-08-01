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

	"darvin-cowork/backend/internal/acp"
	"darvin-cowork/backend/internal/agent"
	"darvin-cowork/backend/internal/agent/llm"
	"darvin-cowork/backend/internal/agent/session"
	"darvin-cowork/backend/internal/agent/store"
	"darvin-cowork/backend/internal/config"
	"darvin-cowork/backend/internal/database"
	"darvin-cowork/backend/internal/gateway"
	"darvin-cowork/backend/internal/logger"

	// Blank import triggers anthropic.init() which registers the provider
	// with llm.NewProvider's name-based factory registry.
	_ "darvin-cowork/backend/internal/agent/llm/anthropic"
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
	); err != nil {
		log.Error("auto migrate failed", zap.Error(err))
		os.Exit(1)
	}
	log.Info("database migrated")

	sqliteStore := store.NewSQLiteStore(database.Get())
	msgStore := store.NewSQLiteMessageStore(database.Get())
	appState := store.NewAppStateStore(database.Get())

	provider, err := llm.NewProvider(rootCtx, cfg.LLM.Provider, llm.ProviderConfig{
		APIKey:  cfg.LLM.APIKey,
		BaseURL: cfg.LLM.BaseURL,
	})
	if err != nil {
		log.Error("failed to construct LLM provider", zap.String("provider", cfg.LLM.Provider), zap.Error(err))
		os.Exit(1)
	}

	agentCfg := agent.Config{
		MaxTurns:             cfg.Agent.MaxTurns,
		ToolTimeout:          time.Duration(cfg.Agent.ToolTimeoutMS) * time.Millisecond,
		Workdir:              cfg.Agent.Workdir,
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

	// Steer 仍接单例 Agent(本期不迁,见 spec §1.3 非目标)。这个 steerAgent
	// 仅给 acp.NewSteerControl 持有,不会被任何 Loop 驱动、也不会订阅事件
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
		AssemblerEnabled: cfg.Agent.AssemblerEnabled,
	})
	if err != nil {
		log.Error("failed to construct steer agent", zap.Error(err))
		os.Exit(1)
	}

	factory := &acp.AgentFactory{
		Name:             cfg.App.Name + "-agent",
		Instructions:     cfg.Agent.Instructions,
		Model:            agent.ModelRef{Provider: cfg.Agent.ProviderName, Model: cfg.Agent.Model},
		Provider:         provider,
		Store:            sqliteStore,
		MessageStore:     msgStore,
		Logger:           log.Logger,
		Config:           agentCfg,
		AssemblerEnabled: cfg.Agent.AssemblerEnabled,
	}

	ledger := gateway.NewEventLedger(log.Logger)
	sessions := gateway.NewSessionManager(
		gateway.WithAgentFactory(factory),
		gateway.WithEventLedger(ledger),
	)
	steer := acp.NewSteerControl(steerAgent)

	// FR-9:启动期 active session 同步 —— 从 app_state 读出上次的
	// active_session_id,灌进 SessionManager。EnsureEntry 只建轻量
	// SessionEntry、不触发 AcpSession 懒建(per-session spec 两阶段),
	// 事件流由 main 端 connect → subscribeAllSessions 时覆盖。
	if activeID, err := appState.GetActiveSession(rootCtx); err == nil && activeID != "" {
		if _, err := sessions.EnsureEntry(activeID); err != nil {
			log.Warn("bootstrap active session failed", zap.String("session_id", activeID), zap.Error(err))
		} else {
			log.Info("bootstrapped active session", zap.String("session_id", activeID))
		}
	}

	handler := gateway.NewHandler(sessions, ledger, steer, sqliteStore, msgStore, appState)
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

	if err := sqliteStore.Close(); err != nil {
		log.Error("sqlite close", zap.Error(err))
	}

	log.Info("graceful shutdown complete")
}
