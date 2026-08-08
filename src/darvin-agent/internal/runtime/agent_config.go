package runtime

import (
	"time"

	"darvin-cowork/backend/internal/agents"
	"darvin-cowork/backend/internal/config"
)

// newAgentConfig translates cfg.Agent into agent.Config. All field
// mappings, unit conversions, and default-touch points live here so
// the runtime cannot drift from config.yaml shape silently.
func newAgentConfig(cfg *config.Config, workspace string) agent.Config {
	cacheTTL, _ := time.ParseDuration(cfg.Agent.MemoryFactsCacheTTL)
	return agent.Config{
		MaxTurns:             cfg.Agent.MaxTurns,
		ToolTimeout:          time.Duration(cfg.Agent.ToolTimeoutMS) * time.Millisecond,
		Workdir:              workspace,
		ShellAllowlist:       cfg.Agent.ShellAllowlist,
		EventBuffer:          cfg.Agent.EventBuffer,
		TokenBudget:          cfg.Agent.TokenBudget,
		CompactTailKeep:      cfg.Agent.CompactTailKeep,
		CompactTailTokens:    cfg.Agent.CompactTailTokens,
		ToolResultMaxBytes:   cfg.Agent.ToolResultMaxBytes,
		CompactMaxRetries:    cfg.Agent.CompactMaxRetries,
		SummarizeMaxTokens:   cfg.Agent.SummarizeMaxTokens,
		SystemPromptAddition: cfg.Agent.SystemPromptAddition,
		AssemblerEnabled:     cfg.Agent.AssemblerEnabled,
		MemoryFactsLimit:     cfg.Agent.MemoryFactsLimit,
		MemoryFactsCacheTTL:  cacheTTL,
	}
}

// resolveWorkspace picks the effective agent workspace. The desktop
// bridge passes $DARVIN_AGENT_WORKSPACE via Options.WorkspaceRoot so
// fsSandbox.root matches the Electron main process; an empty value
// falls back to cfg.Agent.Workdir for `go run` development.
func resolveWorkspace(cfg *config.Config, override string) string {
	if override != "" {
		return override
	}
	return cfg.Agent.Workdir
}
