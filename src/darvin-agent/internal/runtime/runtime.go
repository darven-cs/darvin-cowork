// Package runtime is the single assembly entry of the darvin-agent
// process. Build loads config + database + LLM provider, wires the
// per-session agent factory, the gateway handler, and the bootstrap
// state (skills, MCP), starts the gateway server, and returns a
// ready-to-drive *Runtime. Frontend (cmd/app/main, future TUI / debug
// entry points) holds only *Runtime; every internal package the agent
// needs is wired inside Build and is not visible across the boundary.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	agent "darvin-cowork/backend/internal/agents"
	"darvin-cowork/backend/internal/agents/store"
	"darvin-cowork/backend/internal/config"
	"darvin-cowork/backend/internal/database"
	"darvin-cowork/backend/internal/gateway"
	"darvin-cowork/backend/internal/harness"
	"darvin-cowork/backend/internal/llm"
	"darvin-cowork/backend/internal/mcp"
	"darvin-cowork/backend/internal/memory"
	"darvin-cowork/backend/internal/sessionruntime"
	"darvin-cowork/backend/internal/skills"
	tool "darvin-cowork/backend/internal/tools"
)

// Options carries runtime knobs callers may override. Defaults come
// from config; this struct only holds fields that come from outside
// (CLI / env / test harness).
type Options struct {
	// ConfigPath overrides the default config.yaml lookup
	// ($DARVIN_CONFIG > exe-dir > cwd).
	ConfigPath string

	// WorkspaceRoot overrides cfg.Agent.Workdir. The desktop bridge
	// passes $DARVIN_AGENT_WORKSPACE here so fsSandbox.root matches
	// the Electron main process.
	WorkspaceRoot string

	// HarnessSelector injects a custom harness selector for tests;
	// nil falls back to defaultHarnessSelector (embedded harness that
	// drives agent.Prompt + agent.Run).
	HarnessSelector sessionruntime.HarnessSelector

	// ExtraPlugins is appended to the factory's plugin list at Build
	// time. nil in production; tests / ACP session/new may inject.
	ExtraPlugins []tool.Plugin
}

// Runtime is the dependency face the frontend holds. The gateway
// server, the LLM provider, the per-session factory, the database
// stores, and the bootstrap state are pre-wired here. The frontend
// must not call into individual fields to drive the agent — that goes
// through the gateway server.
type Runtime struct {
	Cfg                *config.Config
	Log                *zap.Logger
	Provider           llm.ModelProvider
	Sessions           *gateway.SessionManager
	Ledger             *gateway.EventLedger
	Handler            *gateway.Handler
	Server             *gateway.Server
	MCP                *mcp.Registry
	Skills             *skills.BootstrapResult
	Factory            *sessionruntime.AgentFactory
	Stores             Stores
	WorkspaceBootstrap *WorkspaceBootstrap
}

// Stores aggregates the SQLite-backed stores the runtime owns.
// Sessions is the concrete *SQLiteStore so Shutdown can release the
// underlying connection; the other four are concrete pointer types
// because handlers / factories accept them by pointer.
type Stores struct {
	Sessions      *store.SQLiteStore
	Messages      store.MessageStore
	AppState      *store.AppStateStore
	ImportedFiles *store.ImportedFileStore
	Usages        *store.SQLiteUsageStore
	Digests       store.DigestStore
	Subagents     *store.SQLiteSubagentStore
}

// Shutdown stops the gateway server, disposes every registered
// harness, and closes the SQLite store. Each step runs independently:
// a failure in one does not skip the others. The first error wins for
// the return value; every error is collected via errors.Join.
func (r *Runtime) Shutdown(ctx context.Context) error {
	var errs []error
	if r.Server != nil {
		if err := r.Server.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("server: %w", err))
		}
	}
	if err := harness.DisposeAll(ctx); err != nil {
		errs = append(errs, fmt.Errorf("harness dispose: %w", err))
	}
	if r.WorkspaceBootstrap != nil {
		r.WorkspaceBootstrap.Dispose()
	}
	if r.Stores.Sessions != nil {
		if err := r.Stores.Sessions.Close(); err != nil {
			errs = append(errs, fmt.Errorf("sqlite close: %w", err))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

// Build is the single assembly entry of the darvin-agent process.
// It loads config and logger, opens the database, constructs the LLM
// provider, builds the agent factory, bootstraps skills / MCP, wires
// the gateway handler, and starts the gateway server. The returned
// *Runtime is ready to drive — frontend waits on ctx.Done() then calls
// Shutdown.
func Build(ctx context.Context, opts Options) (*Runtime, error) {
	cfgPath := opts.ConfigPath
	if cfgPath == "" {
		cfgPath = resolveConfigPath()
	}
	cfg, log, err := loadConfig(cfgPath)
	if err != nil {
		return nil, err
	}

	stores, err := loadDatabase(ctx, cfg, log)
	if err != nil {
		return nil, err
	}

	provider, err := loadProvider(ctx, cfg, log)
	if err != nil {
		return nil, err
	}

	workspace := resolveWorkspace(cfg, opts.WorkspaceRoot)
	log.Info("workspace resolved",
		zap.String("env", opts.WorkspaceRoot),
		zap.String("effective", workspace))

	agentCfg := newAgentConfig(cfg, workspace)

	toolsReg, err := loadTools(workspace, cfg, log)
	if err != nil {
		return nil, err
	}

	// Memory subsystem: bootstrap-only (no FTS) so ctxengine can be
	// wired without blocking on the memory-core spec. The Manager's
	// Search stub returns nil; the FTS-backed implementation arrives
	// with the memory-core spec.
	memMgr := memory.New(workspace)

	workspaceBootstrap := NewWorkspaceBootstrap(memMgr, log)
	stores.Digests = store.NewSQLiteDigestStore(database.Get())

	factory := newAgentFactory(AgentFactoryDeps{
		Name:               cfg.App.Name + "-agent",
		Instructions:       cfg.Agent.Instructions,
		Model:              agent.ModelRef{Provider: cfg.Agent.ProviderName, Model: cfg.Agent.Model},
		Provider:           provider,
		Store:              stores.Sessions,
		MessageStore:       stores.Messages,
		UsageStore:         stores.Usages,
		DigestStore:        stores.Digests,
		SubagentStore:      stores.Subagents,
		Logger:             log,
		Config:             agentCfg,
		Tools:              toolsReg,
		AssemblerEnabled:   cfg.Agent.AssemblerEnabled,
		HarnessSelector:    opts.HarnessSelector,
		ExtraPlugins:       opts.ExtraPlugins,
		Memory:             memMgr,
		WorkspaceBootstrap: workspaceBootstrap,
	})

	skillsResult := bootstrapSkills(ctx, log, workspace, toolsReg)

	mcpRoot, err := resolveMCPPackagesDir()
	if err != nil {
		return nil, fmt.Errorf("resolve mcp packages dir: %w", err)
	}
	mcpReg, err := bootstrapMCP(ctx, log, mcpRoot)
	if err != nil {
		return nil, err
	}

	ledger := gateway.NewEventLedger(log)
	sessions := gateway.NewSessionManager(
		gateway.WithAgentFactory(factory),
		gateway.WithEventLedger(ledger),
	)

	skillPlugin := skills.NewSkillPlugin(skillsResult.Registry, skillsResult.Runner)
	mcpPlugin := tool.NewMcpPlugin(mcpReg)
	factory.Plugins = append(factory.Plugins, skillPlugin, mcpPlugin)

	// setWorkspace re-anchors the runtime workspace: update the sandbox
	// root, rescan project skills against the new workspace, refresh
	// the plugin registry / runner, and re-run tool plugins for every
	// already-built agent. The handler is forward-declared via var,
	// and the closure guards with a non-nil check to avoid a
	// construction-time race.
	var handler *gateway.Handler
	setWorkspace := func(root string) error {
		if err := toolsReg.SetWorkspaceRoot(root); err != nil {
			return err
		}
		newSkills := bootstrapSkills(ctx, log, root, toolsReg)
		skillPlugin.SetBootstrapResult(newSkills)
		if handler != nil {
			handler.Skills = newSkills.Registry
			handler.SkillRunner = newSkills.Runner
		}
		sessions.RefreshAllTools()
		return nil
	}

	handler = gateway.NewHandler(sessions, ledger,
		stores.Sessions, stores.Messages, stores.AppState,
		gateway.HandlerOptions{
			UsageStore:       stores.Usages,
			DigestStore:      stores.Digests,
			ImportedFiles:    stores.ImportedFiles,
			WorkspaceRoot:    workspace,
			SetWorkspaceRoot: setWorkspace,
			Skills:           skillsResult.Registry,
			SkillRunner:      skillsResult.Runner,
			Mcp:              mcpReg,
			SubagentStore:    stores.Subagents,
			Log:              log,
		})

	mcpReg.SetNotifier(mcp.Notifier{
		OnConnectionChanged: handler.OnMcpConnectionChanged,
		OnResolutionChanged: handler.OnMcpResolutionChanged,
	})

	server := gateway.NewServer(handler, log)
	if err := server.Start(ctx); err != nil {
		return nil, err
	}

	if activeID, err := stores.AppState.GetActiveSession(ctx); err == nil && activeID != "" {
		if _, err := sessions.EnsureEntry(activeID); err != nil {
			log.Warn("bootstrap active session failed", zap.String("session_id", activeID), zap.Error(err))
		} else {
			log.Info("bootstrapped active session", zap.String("session_id", activeID))
		}
	}

	return &Runtime{
		Cfg:                cfg,
		Log:                log,
		Provider:           provider,
		Sessions:           sessions,
		Ledger:             ledger,
		Handler:            handler,
		Server:             server,
		MCP:                mcpReg,
		Skills:             skillsResult,
		Factory:            factory,
		Stores:             stores,
		WorkspaceBootstrap: workspaceBootstrap,
	}, nil
}

// Run is the cmd/app/main entry. It builds the runtime, blocks on
// SIGINT/SIGTERM, then triggers graceful shutdown. Returns the process
// exit code: 0 on clean shutdown, 1 on Build failure.
func Run(args []string) int {
	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	rt, err := Build(rootCtx, Options{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "runtime build failed: %v\n", err)
		return 1
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := rt.Shutdown(shutdownCtx); err != nil {
			rt.Log.Warn("shutdown error", zap.Error(err))
		}
	}()

	rt.Log.Info("application started successfully", zap.Int("port", rt.Server.Port()))

	<-rootCtx.Done()
	rt.Log.Info("shutdown signal received")
	rt.Log.Info("graceful shutdown complete")
	return 0
}
