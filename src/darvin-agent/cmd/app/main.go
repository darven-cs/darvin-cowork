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

	dbCfg := &database.Config{
		SessionsDSN: cfg.Database.SessionsDSN,
	}
	if err := database.Init(dbCfg); err != nil {
		log.Error("failed to init database", zap.Error(err))
		os.Exit(1)
	}
	log.Info("database initialized", zap.String("sessions_dsn", cfg.Database.SessionsDSN))

	if err := database.AutoMigrate(
		&store.Session{},
		&store.Message{},
		&store.CompactionCheckpoint{},
		&store.SkillSnapshot{},
	); err != nil {
		log.Error("auto migrate failed", zap.Error(err))
		os.Exit(1)
	}
	log.Info("database migrated")

	sqliteStore := store.NewSQLiteStore(database.Get())
	msgStore := store.NewSQLiteMessageStore(database.Get())

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

	a, err := agent.New(agent.NewAgentConfig{
		Name:             cfg.App.Name + "-agent",
		Instructions:     cfg.Agent.Instructions,
		Model:            agent.ModelRef{Provider: cfg.Agent.ProviderName, Model: cfg.Agent.Model},
		Provider:         provider,
		Session:          session.NewSession("default"),
		Store:            sqliteStore,
		MessageStore:     msgStore,
		Logger:           log.Logger, // *logger.Logger embeds *zap.Logger; dereference for agent.NewAgentConfig.Logger (*zap.Logger).
		Config:           agentCfg,
		AssemblerEnabled: cfg.Agent.AssemblerEnabled,
	})
	if err != nil {
		log.Error("failed to construct agent", zap.Error(err))
		os.Exit(1)
	}
	log.Info("agent initialized",
		zap.String("name", a.SessionHandle().ID),
		zap.Bool("assembler_enabled", cfg.Agent.AssemblerEnabled),
		zap.Int("token_budget", cfg.Agent.TokenBudget),
	)

	sessions := gateway.NewSessionManager()
	ledger := gateway.NewEventLedger(log.Logger)

	loop := acp.NewLoop(a)
	a.AttachMessageIDSrc(loop.CurrentMessageID)
	steer := acp.NewSteerControl(a)

	handler := gateway.NewHandler(sessions, ledger, loop, steer, sqliteStore, msgStore)
	gs := gateway.NewServer(handler, log.Logger)
	if err := gs.Start(rootCtx); err != nil {
		log.Error("gateway start failed", zap.Error(err))
		os.Exit(1)
	}

	sub := a.Subscribe(64)
	ledger.AttachSubscription(sub)

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

	if err := a.Abort(context.Background()); err != nil {
		log.Error("agent abort", zap.Error(err))
	}

	sub.Unsubscribe()

	if err := sqliteStore.Close(); err != nil {
		log.Error("sqlite close", zap.Error(err))
	}

	log.Info("graceful shutdown complete")
}
