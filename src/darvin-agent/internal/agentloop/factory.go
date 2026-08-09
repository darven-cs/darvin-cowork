// AgentFactory assembles the Agent, Harness, and Loop for a new session.

package agentloop

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	agent "darvin-cowork/backend/internal/agents"
	"darvin-cowork/backend/internal/agents/ctxengine"
	"darvin-cowork/backend/internal/agents/protocol"
	"darvin-cowork/backend/internal/agents/session"
	"darvin-cowork/backend/internal/agents/store"
	"darvin-cowork/backend/internal/harness"
	"darvin-cowork/backend/internal/llm"
	"darvin-cowork/backend/internal/memory"
	tool "darvin-cowork/backend/internal/tools"
)

// AgentFactory carries the shared dependencies needed to build an
// *agent.Agent. main.go constructs one and injects it into
// SessionManager, which calls NewAgentLoopSession on the lazy build path.
type AgentFactory struct {
	Name         string
	Instructions string
	Model        agent.ModelRef
	Provider     llm.ModelProvider
	Store        store.SessionStore
	MessageStore store.MessageStore
	UsageStore   store.UsageStore
	DigestStore  store.DigestStore
	Logger       *zap.Logger
	Config       agent.Config
	Tools        *tool.Registry
	Assembler    ctxengine.ContextEngine

	// Plugins are applied to the agent's tool registry after every Build
	// (skill / mcp). SessionManager.RefreshAllTools reuses the same list.
	Plugins []tool.Plugin

	// AssemblerEnabled and Config.AssemblerEnabled are two independent
	// switches: the latter builds the default Assembler, the former
	// flips the executor onto the assembler path.
	AssemblerEnabled bool

	// Memory feeds ctxengine.Deps.MemoryFacts; nil disables the MEMORY block.
	Memory *memory.Manager
	// WorkspaceBootstrap feeds ctxengine.Deps.MemoryBootstrap; nil means
	// no IDENTITY/SOUL/USER blocks. Must be the workspace-level singleton
	// so bootstrap.write invalidation propagates to every session.
	WorkspaceBootstrap agent.BootstrapReader

	// HarnessID pins a specific harness by id. Empty defers to
	// harness.SelectHarness at NewAgentLoopSession time.
	HarnessID string

	// Selector is the factory's harness selector. nil falls back to the
	// built-in helper that uses harness.SelectHarness with the factory's
	// provider / model state.
	Selector HarnessSelector
}

// HarnessSelector chooses a harness for a given session.
type HarnessSelector func(a *agent.Agent, f *AgentFactory) (harness.Harness, error)

// NewAgentLoopSession constructs the Agent + Harness + Loop and attaches
// Loop's CurrentMessageID / CurrentRunID onto Agent so event IDs match
// Loop's state. Order matters: build Loop first, then call
// AttachMessageIDSrc, otherwise Deps.Current* resolves to "". When
// MessageStore is wired, TextDeltaHook (streaming persistence) is also
// attached and cleaned up by AgentLoopSession.Close on evict.
func (f *AgentFactory) NewAgentLoopSession(sessionID string) (*AgentLoopSession, error) {
	a, err := f.Build(sessionID)
	if err != nil {
		return nil, err
	}
	// Replay historical messages from MessageStore; warn-and-continue on failure.
	hydrateSession(context.Background(), f, a.Session())
	h, err := f.resolveHarnessFor(a)
	if err != nil {
		return nil, err
	}
	l := NewLoop(a, h)
	a.AttachMessageIDSrc(l.CurrentMessageID)
	a.AttachRunIDSrc(l.CurrentRunID)
	a.AttachUserMessageIDSrc(l.CurrentUserMessageID)
	deltaHook := agent.NewTextDeltaHook(f.MessageStore, f.Logger)
	deltaHook.Attach(a)
	return &AgentLoopSession{
		SessionID: sessionID,
		Agent:     a,
		Harness:   h,
		Loop:      l,
		DeltaHook: deltaHook,
	}, nil
}

// resolveHarnessFor picks a harness. The default path goes through
// harness.SelectHarness (no agent needed); the Selector variant can
// accept the just-built agent to wire its closures.
func (f *AgentFactory) resolveHarnessFor(a *agent.Agent) (harness.Harness, error) {
	if id := f.HarnessID; id != "" {
		h, ok := harness.Get(id)
		if !ok {
			return nil, fmt.Errorf("acp: harness %q not registered", id)
		}
		return h, nil
	}
	if f.Selector != nil {
		return f.Selector(a, f)
	}
	decision, err := harness.SelectHarness(harness.SelectionParams{
		Provider: extractProviderName(a),
		Model:    f.Model.Model,
	})
	if err != nil {
		return nil, fmt.Errorf("acp: harness selection: %w", err)
	}
	return decision.Harness, nil
}

// Build constructs a fresh *agent.Agent with the given sessionID.
// Agent's own session.ID is the only source of the sessionId field that
// the dispatcher stamps on events — if it is wrong,
// EventLedger.publishLocked routes to the wrong bucket. Plugins are
// applied in order; registration failures are logged and do not block.
func (f *AgentFactory) Build(sessionID string) (*agent.Agent, error) {
	a, err := agent.New(agent.NewAgentConfig{
		Name:               f.Name,
		Instructions:       f.Instructions,
		Model:              f.Model,
		Provider:           f.Provider,
		Session:            session.NewSession(sessionID),
		Store:              f.Store,
		MessageStore:       f.MessageStore,
		UsageStore:         f.UsageStore,
		Logger:             f.Logger,
		Config:             f.Config,
		Tools:              f.Tools,
		Assembler:          f.Assembler,
		AssemblerEnabled:   f.AssemblerEnabled,
		Memory:             f.Memory,
		WorkspaceBootstrap: f.WorkspaceBootstrap,
		DigestStore:        f.DigestStore,
	})
	if err != nil {
		return nil, err
	}
	for _, p := range f.Plugins {
		reg, ok := a.Tools().(protocol.ToolRegistrar)
		if !ok {
			if f.Logger != nil {
				f.Logger.Warn("agent tools are not a registrar", zap.String("plugin", p.PluginID()))
			}
			continue
		}
		if err := p.Register(reg); err != nil {
			if f.Logger != nil {
				f.Logger.Warn("plugin register failed", zap.String("plugin", p.PluginID()), zap.Error(err))
			}
		}
	}
	return a, nil
}

// extractProviderName reads the provider name off the agent for harness
// selection; a nil agent or provider yields "".
func extractProviderName(a *agent.Agent) string {
	if a == nil {
		return ""
	}
	p := a.Provider()
	if p == nil {
		return ""
	}
	if n, ok := p.(interface{ Name() string }); ok {
		return n.Name()
	}
	// Fallback: empty name means "no constraint" in selection.
	_ = p
	return ""
}
