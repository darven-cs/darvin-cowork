package runtime

import (
	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agentloop"
	"darvin-cowork/backend/internal/agents"
	"darvin-cowork/backend/internal/agents/store"
	"darvin-cowork/backend/internal/llm"
	"darvin-cowork/backend/internal/tools"
)

// AgentFactoryDeps collects every dependency the runtime feeds into
// the per-session agent factory. ExtraPlugins is appended at
// construction time; skill / mcp plugins are added inside Build after
// bootstrap.
type AgentFactoryDeps struct {
	Name             string
	Instructions     string
	Model            agent.ModelRef
	Provider         llm.ModelProvider
	Store            store.SessionStore
	MessageStore     store.MessageStore
	Logger           *zap.Logger
	Config           agent.Config
	Tools            *tool.Registry
	AssemblerEnabled bool
	HarnessSelector  agentloop.HarnessSelector
	ExtraPlugins     []tool.Plugin
}

// newAgentFactory constructs the per-session factory. The Selector
// is always set explicitly (defaultHarnessSelector when the caller
// passes nil) so factory.Selector is never an implicit fallback.
func newAgentFactory(d AgentFactoryDeps) *agentloop.AgentFactory {
	selector := d.HarnessSelector
	if selector == nil {
		selector = defaultHarnessSelector
	}
	return &agentloop.AgentFactory{
		Name:             d.Name,
		Instructions:     d.Instructions,
		Model:            d.Model,
		Provider:         d.Provider,
		Store:            d.Store,
		MessageStore:     d.MessageStore,
		Logger:           d.Logger,
		Config:           d.Config,
		Tools:            d.Tools,
		AssemblerEnabled: d.AssemblerEnabled,
		Selector:         selector,
		Plugins:          d.ExtraPlugins,
	}
}
