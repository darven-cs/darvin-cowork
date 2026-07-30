package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agent"
	"darvin-cowork/backend/internal/agent/llm"
	"darvin-cowork/backend/internal/agent/session"
	"darvin-cowork/backend/internal/config"
	"darvin-cowork/backend/internal/database"
	"darvin-cowork/backend/internal/logger"

	// Blank import triggers anthropic.init() which registers the provider
	// with llm.NewProvider's name-based factory registry.
	_ "darvin-cowork/backend/internal/agent/llm/anthropic"
)

func main() {
	cfg, err := config.Load("config.yaml")
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

	log.Info("starting application", zap.String("name", cfg.App.Name), zap.String("env", cfg.App.Env))

	dbCfg := &database.Config{
		SessionsDSN: cfg.Database.SessionsDSN,
	}
	if err := database.Init(dbCfg); err != nil {
		log.Error("failed to init database", zap.Error(err))
		os.Exit(1)
	}
	log.Info("database initialized", zap.String("sessions_dsn", cfg.Database.SessionsDSN))

	// --- Agent wiring (M7: config + cmd sync) -------------------------
	// Build the LLM provider from cfg.LLM. The provider name must match
	// a registered factory (currently only "anthropic"); an unknown name
	// surfaces as llm.ErrUnknownProvider. Logger is left nil —
	// llm.NewHTTPClient accepts nil and silently skips per-call logging.
	provider, err := llm.NewProvider(context.Background(), cfg.LLM.Provider, llm.ProviderConfig{
		APIKey:  cfg.LLM.APIKey,
		BaseURL: cfg.LLM.BaseURL,
	})
	if err != nil {
		log.Error("failed to construct LLM provider", zap.String("provider", cfg.LLM.Provider), zap.Error(err))
		os.Exit(1)
	}

	// Translate cfg.Agent (config.AgentConfig) → agent.Config. The only
	// non-trivial conversion is ToolTimeoutMS → time.Duration. Defaults
	// for MaxTurns / ToolTimeout / EventBuffer are applied inside
	// agent.New when zero, so we forward the YAML values verbatim.
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

	log.Info("application started successfully")
}
