// Bundles the Agent + Harness + Loop for a single active session.

package sessionruntime

import (
	agent "darvin-cowork/backend/internal/agents"
	"darvin-cowork/backend/internal/harness"
	"darvin-cowork/backend/internal/subagent"
)

// SessionRuntime bundles the Agent + Harness + Loop for a single
// session id. SessionManager lazily builds it on the first prompt
// and tears it down on evict.
//
// Harness is the newer field: Loop.executeTurn routes the prompt
// path through harness.RunAttemptWithLifecycle instead of calling
// Agent.Prompt + Agent.Run directly. The skill path still calls
// Agent directly because the skill flow needs Agent-held transient
// state (RunSkillPrompt / RunSkillTools).
type SessionRuntime struct {
	SessionID string
	Agent     *agent.Agent
	Harness   harness.Harness
	Loop      *Loop
	// DeltaHook subscribes to the Agent bus's text_delta events for
	// streaming persistence. May be nil (tests / factories without a
	// MessageStore wired).
	DeltaHook *agent.TextDeltaHook
	// Subagents is the per-session manager for sub-agent runs spawned
	// via delegate_subagent / parallel_subagents. nil when the factory
	// has no SubagentStore wired.
	Subagents *subagent.Manager
}

// Close shuts down the DeltaHook subscription + Loop and waits for
// the run goroutine to exit. Idempotent.
//
// Note: Loop.Close blocks until the in-flight turn exits, which is
// why SessionManager runs Close on a background goroutine —
// entry.cancel only triggers ctx.Done(); the actual Loop shutdown
// comes from the goroutine watching ctx, not from cancel itself.
func (s *SessionRuntime) Close() {
	if s == nil {
		return
	}
	if s.Subagents != nil {
		s.Subagents.Close()
	}
	if s.DeltaHook != nil {
		s.DeltaHook.Close()
	}
	if s.Loop != nil {
		s.Loop.Close()
	}
}
