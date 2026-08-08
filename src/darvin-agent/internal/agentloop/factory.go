package agentloop

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agents"
	"darvin-cowork/backend/internal/agents/ctxengine"
	"darvin-cowork/backend/internal/agents/protocol"
	"darvin-cowork/backend/internal/agents/session"
	"darvin-cowork/backend/internal/agents/store"
	"darvin-cowork/backend/internal/harness"
	"darvin-cowork/backend/internal/llm"
	"darvin-cowork/backend/internal/memory"
	"darvin-cowork/backend/internal/tools"
)

// AgentFactory carries the shared dependencies needed to build an
// *agent.Agent. main.go constructs one and injects it into
// SessionManager, which calls NewAgentLoopSession on the lazy build
// path. From Agent's perspective, Provider / Store / Logger / Tools /
// Assembler are read-only; only the conversation history goes through
// session.Session's own lock.
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
	// (skill / mcp plugins). SessionManager.RefreshAllTools reuses the
	// same plugin list for the bulk Unregister + Register pass so the
	// tool surface tracks skill / mcp state changes.
	Plugins []tool.Plugin

	// AssemblerEnabled and Config.AssemblerEnabled are two independent
	// switches: the latter controls whether a default Assembler is
	// constructed; the former controls whether the executor takes the
	// assembler path. The factory does not merge them for callers —
	// each side decides on its own.
	AssemblerEnabled bool

	// Memory feeds ctxengine.Deps.MemoryFacts; nil disables the MEMORY
	// block instead of failing the run.
	Memory *memory.Manager
	// WorkspaceBootstrap feeds ctxengine.Deps.MemoryBootstrap; nil
	// means no IDENTITY/SOUL/USER blocks. Must be the workspace-level
	// singleton so bootstrap.write invalidation propagates to every
	// session.
	WorkspaceBootstrap agent.BootstrapReader

	// HarnessID pins a specific harness by id. An empty
	// value defers to harness.SelectHarness at NewAgentLoopSession time.
	HarnessID string

	// Selector is the factory's harness selector. nil falls back to a
	// built-in helper that uses harness.SelectHarness with the factory's
	// provider / model / explicit-id state. Production wiring uses the
	// default; tests inject a fake to keep the registry deterministic.
	Selector HarnessSelector
}

// HarnessSelector chooses a harness for a given session. The default
// implementation goes through harness.SelectHarness.
type HarnessSelector func(a *agent.Agent, f *AgentFactory) (harness.Harness, error)

// NewAgentLoopSession constructs the Agent + Harness + Loop in one go
// and attaches Loop's CurrentMessageID / CurrentRunID onto Agent so
// the messageID / runID carried by events matches Loop's current
// state. The order is important: build Loop first, then call
// AttachMessageIDSrc — otherwise the executor Deps.Current* hooks
// resolve to empty strings.
//
// When MessageStore is wired we also attach TextDeltaHook (streaming
// persistence); the hook subscription is cleaned up by
// AgentLoopSession.Close on evict.
func (f *AgentFactory) NewAgentLoopSession(sessionID string) (*AgentLoopSession, error) {
	a, err := f.Build(sessionID)
	if err != nil {
		return nil, err
	}
	// Replay historical messages from the persistent MessageStore into
	// the in-memory Session so the agent remembers prior turns after
	// restart / entry rebuild. Failures are warn-and-continue and do
	// not block AgentLoopSession construction.
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

// resolveHarnessFor picks a harness. If the Selector accepts the just-built
// agent, it can wire its closures to drive that exact instance. The default
// Selector path does not need an agent — it goes through harness.SelectHarness
// which only looks at provider / model facts.
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
// Agent's own session.ID is the only source of the sessionId field
// that the dispatcher stamps on events — if it is wrong,
// EventLedger.publishLocked routes to the wrong bucket. After each
// new agent is built we apply Plugins in order; plugin registration
// failures are logged as warnings and do not block agent availability.
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
