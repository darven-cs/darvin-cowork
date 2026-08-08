// Tests that the Loop drives turns through the harness path.

package agentloop

import (
	"context"
	"sync"
	"testing"
	"time"

	agent "darvin-cowork/backend/internal/agents"
	"darvin-cowork/backend/internal/agents/session"
	"darvin-cowork/backend/internal/agents/store"
	"darvin-cowork/backend/internal/harness"
	"darvin-cowork/backend/internal/llm"
	tool "darvin-cowork/backend/internal/tools"
)

// TestLoopExecuteTurnCallsHarness asserts the prompt path drives the
// harness's Run closure rather than calling the agent directly.
func TestLoopExecuteTurnCallsHarness(t *testing.T) {
	a, err := agent.New(agent.NewAgentConfig{
		Session:  session.NewSession("default"),
		Provider: &scriptedProvider{},
		Tools:    tool.NewRegistry(),
		Store:    store.NewMemoryStore(),
	})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}

	var mu sync.Mutex
	var harnessCalls []harness.RunAttemptParams
	h := harness.NewEmbedded(harness.EmbeddedConfig{
		Run: func(ctx context.Context, p harness.RunAttemptParams) (*harness.AttemptResult, error) {
			mu.Lock()
			harnessCalls = append(harnessCalls, p)
			mu.Unlock()
			// Do NOT call the agent — the assertion is that the harness is
			// the execution path, not that it forwards to the agent.
			return &harness.AttemptResult{Status: harness.AttemptOK}, nil
		},
	})
	loop := NewLoop(a, h)
	t.Cleanup(loop.Close)
	a.AttachMessageIDSrc(loop.CurrentMessageID)

	if _, err := loop.Submit(PromptRequest{Content: "hi"}); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(harnessCalls)
		mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(harnessCalls) != 1 {
		t.Fatalf("harness Run called %d times, want 1", len(harnessCalls))
	}
	if harnessCalls[0].Prompt != "hi" {
		t.Fatalf("prompt = %q, want hi", harnessCalls[0].Prompt)
	}
	if harnessCalls[0].MessageID == "" {
		t.Fatal("MessageID not minted")
	}
}

// TestLoopSkillBypassHarness asserts the skill path still calls the agent
// directly (RunSkillSession) and never reaches the harness.
func TestLoopSkillBypassHarness(t *testing.T) {
	a, err := agent.New(agent.NewAgentConfig{
		Session: session.NewSession("default"),
		Provider: &scriptedProvider{events: []llm.StreamEvent{
			llm.DoneEvent{Response: llm.CompletionResponse{
				Model: "test", Content: "ok", FinishReason: llm.FinishReasonStop,
			}},
		}},
		Tools: tool.NewRegistry(),
		Store: store.NewMemoryStore(),
	})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}

	var harnessCalls int
	h := harness.NewEmbedded(harness.EmbeddedConfig{
		Run: func(context.Context, harness.RunAttemptParams) (*harness.AttemptResult, error) {
			harnessCalls++
			return &harness.AttemptResult{Status: harness.AttemptOK}, nil
		},
	})
	loop := NewLoop(a, h)
	t.Cleanup(loop.Close)

	// A skill invocation with no allowed tools. The agent's Run drives the
	// mini loop, streams a natural stop and returns.
	skill := &SkillInvocation{
		SystemPrompt: "skill body",
		Content:      "/code-review",
		Tools:        []tool.Tool{},
	}
	if _, err := loop.SubmitSkill(*skill); err != nil {
		t.Fatalf("SubmitSkill: %v", err)
	}

	// Wait for the skill turn to finish (agent_end) or time out.
	sub := a.Subscribe(64)
	defer sub.Unsubscribe()
	deadline := time.After(3 * time.Second)
loop:
	for {
		select {
		case ev := <-sub.C():
			if ev.EventName() == "agent_end" {
				break loop
			}
		case <-deadline:
			t.Fatal("skill turn did not complete")
		}
	}

	if harnessCalls != 0 {
		t.Fatalf("harness Run called %d times; skill must bypass harness", harnessCalls)
	}
}
