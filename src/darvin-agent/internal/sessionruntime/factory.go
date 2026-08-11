// AgentFactory assembles the Agent, Harness, and Loop for a new session.

package sessionruntime

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	agent "darvin-cowork/backend/internal/agents"
	"darvin-cowork/backend/internal/agents/ctxengine"
	"darvin-cowork/backend/internal/agents/event"
	"darvin-cowork/backend/internal/agents/protocol"
	"darvin-cowork/backend/internal/agents/session"
	"darvin-cowork/backend/internal/agents/store"
	"darvin-cowork/backend/internal/harness"
	"darvin-cowork/backend/internal/llm"
	"darvin-cowork/backend/internal/memory"
	"darvin-cowork/backend/internal/subagent"
	tool "darvin-cowork/backend/internal/tools"
)

// AgentFactory carries the shared dependencies needed to build an
// *agent.Agent. main.go constructs one and injects it into
// SessionManager, which calls NewSessionRuntime on the lazy build path.
type AgentFactory struct {
	Name          string
	Instructions  string
	Model         agent.ModelRef
	Provider      llm.ModelProvider
	Store         store.SessionStore
	MessageStore  store.MessageStore
	UsageStore    store.UsageStore
	DigestStore   store.DigestStore
	SubagentStore store.SubagentStore
	Logger        *zap.Logger
	Config        agent.Config
	Tools         *tool.Registry
	Assembler     ctxengine.ContextEngine

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
	// harness.SelectHarness at NewSessionRuntime time.
	HarnessID string

	// Selector is the factory's harness selector. nil falls back to the
	// built-in helper that uses harness.SelectHarness with the factory's
	// provider / model state.
	Selector HarnessSelector
}

// HarnessSelector chooses a harness for a given session.
type HarnessSelector func(a *agent.Agent, f *AgentFactory) (harness.Harness, error)

// NewSessionRuntime constructs the Agent + Harness + Loop and attaches
// Loop's CurrentMessageID / CurrentRunID onto Agent so event IDs match
// Loop's state. Order matters: build Loop first, then call
// AttachMessageIDSrc, otherwise Deps.Current* resolves to "". When
// MessageStore is wired, TextDeltaHook (streaming persistence) is also
// attached and cleaned up by SessionRuntime.Close on evict.
func (f *AgentFactory) NewSessionRuntime(sessionID string) (*SessionRuntime, error) {
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
	artifactHook := agent.NewArtifactHook(f.Config.Workdir, f.Logger)
	artifactHook.Attach(a)
	sess := &SessionRuntime{
		SessionID:    sessionID,
		Agent:        a,
		Harness:      h,
		Loop:         l,
		DeltaHook:    deltaHook,
		ArtifactHook: artifactHook,
	}
	if f.SubagentStore != nil {
		sess.Subagents = subagent.NewManager(subagent.Deps{
			Store:         f.SubagentStore,
			ParentSession: sessionID,
			Runner:        f.buildSubagentRunner(a, l),
			MaxConcurrent: 8,
			ResultBufCap:  1 << 20,
		})
		a.AttachSubagents(sess.Subagents)
	}
	return sess, nil
}

// buildSubagentRunner returns a subagent.Runner that drives a real
// *agent.Agent under a scoped ToolRegistry. The sub-agent session id
// is namespaced under the parent; the persisted MessageStore rows are
// isolated by that id so the renderer can fetch sub-agent history
// without leaking into the parent's view.
func (f *AgentFactory) buildSubagentRunner(parent *agent.Agent, parentLoop *Loop) subagent.Runner {
	return func(ctx context.Context, req subagent.RunnerRequest) (subagent.RunnerResult, error) {
		scopedReg := f.Tools.ScopedForSkill(req.Scope)
		modelRef := f.Model
		if req.Model != "" {
			modelRef = agent.ModelRef{Model: req.Model}
		}
		subSession := session.NewSession(req.SubagentID)
		sub, err := agent.New(agent.NewAgentConfig{
			Name:         "subagent",
			Instructions: buildSubagentInstructions(parent, req),
			Model:        modelRef,
			Provider:     f.Provider,
			Session:      subSession,
			// nil → MemoryStore: the sub-agent must not create a row in
			// the sessions table (it would pollute the sidebar list).
			// Messages still persist via MessageStore under the run id.
			Store:              nil,
			MessageStore:       f.MessageStore,
			UsageStore:         f.UsageStore,
			Logger:             f.Logger,
			Config:             f.Config,
			Tools:              scopedReg,
			Assembler:          f.Assembler,
			AssemblerEnabled:   false,
			Memory:             nil,
			WorkspaceBootstrap: f.WorkspaceBootstrap,
			DigestStore:        nil,
		})
		if err != nil {
			return subagent.RunnerResult{}, fmt.Errorf("subagent build: %w", err)
		}
		// Same generator functions as the parent loop, so events stamped
		// by the sub-agent share id shape with parent-loop events.
		sub.AttachMessageIDSrc(parentLoop.CurrentMessageID)
		sub.AttachRunIDSrc(parentLoop.CurrentRunID)
		sub.AttachUserMessageIDSrc(parentLoop.CurrentUserMessageID)

		// Subscribe to text_delta events so we can capture the final
		// assistant text and tool-call count without re-reading the
		// MessageStore (which may lag).
		subCh := sub.Subscribe(64)
		defer subCh.Unsubscribe()
		var (
			toolCalls int
			finalText strings.Builder
		)
		go func() {
			for ev := range subCh.C() {
				switch e := ev.(type) {
				case event.TextDeltaEvent:
					finalText.WriteString(e.Delta)
				case event.ToolStartEvent:
					toolCalls++
				}
			}
		}()

		if err := sub.Prompt(ctx, req.Prompt, nil); err != nil {
			return subagent.RunnerResult{}, fmt.Errorf("subagent prompt: %w", err)
		}
		if err := sub.Run(ctx); err != nil {
			return subagent.RunnerResult{
				FinalText: finalText.String(),
				ToolCalls: toolCalls,
			}, err
		}
		return subagent.RunnerResult{
			FinalText: finalText.String(),
			ToolCalls: toolCalls,
		}, nil
	}
}

// buildSubagentInstructions wraps the parent's instructions in a
// sub-agent context block that explains the isolated scope (no parent
// history visible, tool whitelist enforced, depth=1).
func buildSubagentInstructions(parent *agent.Agent, req subagent.RunnerRequest) string {
	base := parent.Instructions()
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\n<subagent-context>\n")
	b.WriteString("You are a sub-agent of session ")
	b.WriteString(req.ParentID)
	b.WriteString(".\n")
	if req.Description != "" {
		b.WriteString("Task: ")
		b.WriteString(req.Description)
		b.WriteString("\n")
	}
	if len(req.Scope) > 0 {
		b.WriteString("Allowed tools: ")
		b.WriteString(strings.Join(req.Scope, ", "))
		b.WriteString("\n")
	}
	b.WriteString("You do NOT see the parent's conversation history; only the prompt you received and your own tool results are visible.\n")
	b.WriteString("You may not spawn further sub-agents (depth=1).\n")
	b.WriteString("</subagent-context>\n")
	return b.String()
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
