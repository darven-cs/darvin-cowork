package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"darvin-cowork/backend/internal/agents/queue"
	"darvin-cowork/backend/internal/agents/session"
	"darvin-cowork/backend/internal/llm"
	tool "darvin-cowork/backend/internal/tools"
)

// blockingProvider emits one event then blocks until ctx is cancelled.
type blockingProvider struct{}

func (b *blockingProvider) Name() string { return "blocking" }
func (b *blockingProvider) Complete(_ context.Context, _ *llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return nil, errors.New("not implemented")
}
func (b *blockingProvider) Stream(ctx context.Context, _ *llm.CompletionRequest) (*llm.StreamingResponse, error) {
	ch := make(chan llm.StreamEvent, 1)
	ch <- llm.TextDeltaEvent{Delta: "x"}
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return llm.NewStreamingResponse(ch, nil), nil
}

func newAgentForTest(t *testing.T, p llm.ModelProvider) *Agent {
	t.Helper()
	a, err := New(NewAgentConfig{
		Session:  session.NewSession("s"),
		Provider: p,
		Tools:    tool.NewRegistry(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestPromptBusy(t *testing.T) {
	a := newAgentForTest(t, &blockingProvider{})
	if err := a.Prompt(context.Background(), "first", nil); err != nil {
		t.Fatal(err)
	}
	// can't call Run synchronously; simulate "already running" by manually
	// setting state via a second enqueue attempt while another run holds it.
	// Easiest: run the loop in a goroutine, then call Prompt.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	// wait until the run is actually in progress
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if a.IsRunning() {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := a.Prompt(context.Background(), "second", nil); !errors.Is(err, ErrAgentBusy) {
		t.Errorf("Prompt while running: err = %v, want ErrAgentBusy", err)
	}
	cancel()
	<-done
}

func TestRunNaturalStop(t *testing.T) {
	prov := &scriptedProvider{events: []llm.StreamEvent{
		llm.TextDeltaEvent{Delta: "hi"},
		llm.DoneEvent{Response: llm.CompletionResponse{FinishReason: llm.FinishReasonStop}},
	}}
	a := newAgentForTest(t, prov)
	if err := a.Prompt(context.Background(), "hello", nil); err != nil {
		t.Fatal(err)
	}
	if err := a.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if a.SessionHandle().Len() != 2 {
		t.Errorf("session len = %d, want 2 (user + assistant)", a.SessionHandle().Len())
	}
}

func TestToLLMImages_SplitsDataURL(t *testing.T) {
	got := toLLMImages([]queue.ImageRef{
		{Path: "/a.png", DataURL: "data:image/png;base64,cG5nZGF0YQ=="},
		{Path: "/bad", DataURL: "not-a-data-url"},
		{Path: "/jpg", DataURL: "data:image/jpeg;base64,"}, // empty payload → skipped
	})
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 (malformed skipped)", len(got))
	}
	if got[0].MediaType != "image/png" || got[0].Data != "cG5nZGF0YQ==" {
		t.Errorf("got %+v, want image/png block", got[0])
	}
}

func TestSubscribeReceivesEvents(t *testing.T) {
	prov := &scriptedProvider{events: []llm.StreamEvent{
		llm.TextDeltaEvent{Delta: "ok"},
		llm.DoneEvent{Response: llm.CompletionResponse{FinishReason: llm.FinishReasonStop}},
	}}
	a := newAgentForTest(t, prov)
	sub := a.Subscribe(16)
	defer sub.Unsubscribe()
	_ = a.Prompt(context.Background(), "hi", nil)
	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	// expect at least: PromptReceived, RunStart? (no), TurnStart, LLMStart,
	// TextDelta, LLMEnd, TurnEnd, AgentEnd — at least 6.
	count := 0
	timeout := time.After(100 * time.Millisecond)
loop:
	for {
		select {
		case _, ok := <-sub.C():
			if !ok {
				break loop
			}
			count++
		case <-timeout:
			break loop
		}
	}
	if count < 5 {
		t.Errorf("got %d events, want >= 5", count)
	}
}
