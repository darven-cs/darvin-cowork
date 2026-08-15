// Maps the agent config section onto agent.Config.

package runtime

import (
	"time"

	agent "darvin-cowork/backend/internal/agents"
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
		ContextWindow:        cfg.Agent.ContextWindow,
		SoftCompactRatio:     cfg.Agent.SoftCompactRatio,
		ToolResultSnipRatio:  cfg.Agent.ToolResultSnipRatio,
		CompactRatio:         cfg.Agent.CompactRatio,
		CompactForceRatio:    cfg.Agent.CompactForceRatio,
		CompactTailTokens:    cfg.Agent.CompactTailTokens,
		RecentKeep:           cfg.Agent.RecentKeep,
		ArchiveDir:           cfg.Agent.ArchiveDir,
		ToolResultMaxBytes:   cfg.Agent.ToolResultMaxBytes,
		SummarizeMaxTokens:   cfg.Agent.SummarizeMaxTokens,
		SystemPromptAddition: cfg.Agent.SystemPromptAddition,
		AssemblerEnabled:     cfg.Agent.AssemblerEnabled,
		MemoryFactsLimit:     cfg.Agent.MemoryFactsLimit,
		MemoryFactsCacheTTL:  cacheTTL,
	}
}

// resolveModelRef picks the effective agent model reference. The active
// preset's default_model wins; otherwise the global llm.model, then the
// legacy agent.model / agent.provider_name fall back.
func resolveModelRef(cfg *config.Config) agent.ModelRef {
	provider := cfg.LLM.Provider
	model := cfg.LLM.Model
	if entry, ok := cfg.Providers[provider]; ok && entry.DefaultModel != "" {
		model = entry.DefaultModel
	}
	if model == "" {
		model = cfg.Agent.Model
	}
	if provider == "" {
		provider = cfg.Agent.ProviderName
	}
	return agent.ModelRef{Provider: provider, Model: model}
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
