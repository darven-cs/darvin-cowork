// Assembles the per-session agent factory and its dependencies.

package runtime

import (
	"go.uber.org/zap"

	agent "darvin-cowork/backend/internal/agents"
	"darvin-cowork/backend/internal/agents/store"
	"darvin-cowork/backend/internal/llm"
	"darvin-cowork/backend/internal/memory"
	"darvin-cowork/backend/internal/sessionruntime"
	tool "darvin-cowork/backend/internal/tools"
)

// AgentFactoryDeps collects every dependency the runtime feeds into
// the per-session agent factory. ExtraPlugins is appended at
// construction time; skill / mcp plugins are added inside Build after
// bootstrap.
type AgentFactoryDeps struct {
	Name               string
	Instructions       string
	Model              agent.ModelRef
	Provider           llm.ModelProvider
	Providers          map[string]llm.ModelProvider
	Store              store.SessionStore
	MessageStore       store.MessageStore
	UsageStore         store.UsageStore
	DigestStore        store.DigestStore
	SubagentStore      store.SubagentStore
	Logger             *zap.Logger
	Config             agent.Config
	Tools              *tool.Registry
	AssemblerEnabled   bool
	HarnessSelector    sessionruntime.HarnessSelector
	ExtraPlugins       []tool.Plugin
	Memory             *memory.Manager
	WorkspaceBootstrap *WorkspaceBootstrap
}

// newAgentFactory constructs the per-session factory. The Selector
// is always set explicitly (defaultHarnessSelector when the caller
// passes nil) so factory.Selector is never an implicit fallback.
func newAgentFactory(d AgentFactoryDeps) *sessionruntime.AgentFactory {
	selector := d.HarnessSelector
	if selector == nil {
		selector = defaultHarnessSelector
	}
	return &sessionruntime.AgentFactory{
		Name:               d.Name,
		Instructions:       d.Instructions,
		Model:              d.Model,
		Provider:           d.Provider,
		Providers:          d.Providers,
		Store:              d.Store,
		MessageStore:       d.MessageStore,
		UsageStore:         d.UsageStore,
		DigestStore:        d.DigestStore,
		SubagentStore:      d.SubagentStore,
		Logger:             d.Logger,
		Config:             d.Config,
		Tools:              d.Tools,
		AssemblerEnabled:   d.AssemblerEnabled,
		Selector:           selector,
		Plugins:            d.ExtraPlugins,
		Memory:             d.Memory,
		WorkspaceBootstrap: d.WorkspaceBootstrap,
	}
}
