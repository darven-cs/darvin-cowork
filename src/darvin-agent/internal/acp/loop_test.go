package acp

import (
	"context"
	"errors"
	"testing"
	"time"

	"darvin-cowork/backend/internal/agent"
	"darvin-cowork/backend/internal/agent/event"
	"darvin-cowork/backend/internal/agent/llm"
	"darvin-cowork/backend/internal/agent/session"
	"darvin-cowork/backend/internal/agent/store"
)

// scriptedProvider replays a fixed StreamEvent script and closes the
// channel, which drives the executor to a natural stop.
type scriptedProvider struct{ events []llm.StreamEvent }

func (p *scriptedProvider) Name() string { return "scripted" }
func (p *scriptedProvider) Complete(_ context.Context, _ *llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return nil, errors.New("not implemented")
}
func (p *scriptedProvider) Stream(_ context.Context, _ *llm.CompletionRequest) (*llm.StreamingResponse, error) {
	ch := make(chan llm.StreamEvent, len(p.events))
	for _, e := range p.events {
		ch <- e
	}
	close(ch)
	return llm.NewStreamingResponse(ch, nil), nil
}

// blockingProvider emits one delta then blocks until ctx is cancelled,
// which is how the abort / busy tests hold the Agent in stateRunning.
type blockingProvider struct{}

func (p *blockingProvider) Name() string { return "blocking" }
func (p *blockingProvider) Complete(_ context.Context, _ *llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return nil, errors.New("not implemented")
}
func (p *blockingProvider) Stream(ctx context.Context, _ *llm.CompletionRequest) (*llm.StreamingResponse, error) {
	ch := make(chan llm.StreamEvent, 1)
	ch <- llm.TextDeltaEvent{Delta: "x"}
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return llm.NewStreamingResponse(ch, nil), nil
}

// newLoopForTest builds an Agent bound to session "default" plus the Loop
// wrapping it, with AttachMessageIDSrc wired exactly like main.go so the
// emitted events carry EventCommon.MessageID.
func newLoopForTest(t *testing.T, p llm.ModelProvider) (*agent.Agent, *Loop) {
	t.Helper()
	a, err := agent.New(agent.NewAgentConfig{
		Session:  session.NewSession("default"),
		Provider: p,
		Store:    store.NewMemoryStore(),
	})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	loop := NewLoop(a)
	a.AttachMessageIDSrc(loop.CurrentMessageID)
	return a, loop
}

// collect drains sub until the deadline or until an agent_end arrives.
func collect(t *testing.T, sub *event.Subscription, budget time.Duration) []event.Event {
	t.Helper()
	var got []event.Event
	deadline := time.After(budget)
	for {
		select {
		case ev, ok := <-sub.C():
			if !ok {
				return got
			}
			got = append(got, ev)
			if ev.EventName() == "agent_end" {
				return got
			}
		case <-deadline:
			return got
		}
	}
}

func names(evs []event.Event) []string {
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		out = append(out, e.EventName())
	}
	return out
}

func TestLoopEnd2End(t *testing.T) {
	prov := &scriptedProvider{events: []llm.StreamEvent{
		llm.TextDeltaEvent{Delta: "hello "},
		llm.TextDeltaEvent{Delta: "world"},
		llm.DoneEvent{Response: llm.CompletionResponse{
			Model: "test", Content: "hello world", FinishReason: llm.FinishReasonStop,
		}},
	}}
	a, loop := newLoopForTest(t, prov)
	sub := a.Subscribe(64)
	defer sub.Unsubscribe()

	msgID, err := loop.Prompt(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if len(msgID) != messageIDLen {
		t.Fatalf("messageID len = %d, want %d", len(msgID), messageIDLen)
	}

	got := collect(t, sub, 2*time.Second)
	gotNames := names(got)
	if len(gotNames) == 0 || gotNames[len(gotNames)-1] != "agent_end" {
		t.Fatalf("last event should be agent_end, got %v", gotNames)
	}

	// Every event must carry the session id so the gateway can route the
	// notification. messageId is additionally expected on everything the
	// executor emits; run_start / run_end are run-scoped rather than
	// prompt-scoped and deliberately leave it empty.
	var sawTextDelta, sawLLMEnd bool
	for _, ev := range got {
		c := ev.Common()
		if c.SessionID != "default" {
			t.Errorf("%s: sessionId = %q, want \"default\"", ev.EventName(), c.SessionID)
		}
		switch ev.EventName() {
		case "run_start", "run_end":
			if c.MessageID != "" {
				t.Errorf("%s: messageId = %q, want empty", ev.EventName(), c.MessageID)
			}
			continue
		case "text_delta":
			sawTextDelta = true
		case "llm_end":
			sawLLMEnd = true
		}
		if c.MessageID != msgID {
			t.Errorf("%s: messageId = %q, want %q", ev.EventName(), c.MessageID, msgID)
		}
	}
	if !sawTextDelta {
		t.Errorf("no text_delta in %v", gotNames)
	}
	if !sawLLMEnd {
		t.Errorf("no llm_end in %v", gotNames)
	}
}

func TestLoopAbort(t *testing.T) {
	a, loop := newLoopForTest(t, &blockingProvider{})
	sub := a.Subscribe(64)
	defer sub.Unsubscribe()

	if _, err := loop.Prompt(context.Background(), "hi"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	waitRunning(t, sub)

	if err := loop.Abort(context.Background()); err != nil {
		t.Fatalf("Abort: %v", err)
	}

	got := names(collect(t, sub, 2*time.Second))
	if len(got) == 0 || got[len(got)-1] != "agent_end" {
		t.Fatalf("abort should still terminate with agent_end, got %v", got)
	}
}

func TestLoopPromptErrAgentBusy(t *testing.T) {
	a, loop := newLoopForTest(t, &blockingProvider{})
	sub := a.Subscribe(64)
	defer sub.Unsubscribe()
	t.Cleanup(func() { _ = a.Abort(context.Background()) })

	if _, err := loop.Prompt(context.Background(), "first"); err != nil {
		t.Fatalf("first Prompt: %v", err)
	}
	waitRunning(t, sub)

	first := loop.CurrentMessageID()
	if _, err := loop.Prompt(context.Background(), "second"); !errors.Is(err, ErrAgentBusy) {
		t.Fatalf("second Prompt: err = %v, want ErrAgentBusy", err)
	}
	// A rejected Prompt must not disturb the in-flight run's messageID —
	// the events it is still emitting have to stay correlated.
	if got := loop.CurrentMessageID(); got != first {
		t.Fatalf("CurrentMessageID after rejected Prompt = %q, want %q", got, first)
	}
}

// waitRunning blocks until the first event of a run arrives. Agent.Run
// flips its state to running before emitting run_start, so observing any
// event guarantees a subsequent Prompt sees ErrAgentBusy.
func waitRunning(t *testing.T, sub *event.Subscription) {
	t.Helper()
	select {
	case <-sub.C():
	case <-time.After(2 * time.Second):
		t.Fatal("agent did not start running")
	}
}
