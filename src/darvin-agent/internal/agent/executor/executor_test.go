package executor

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"darvin-cowork/backend/internal/agent/event"
	"darvin-cowork/backend/internal/agent/llm"
	"darvin-cowork/backend/internal/agent/session"
	"darvin-cowork/backend/internal/agent/tool"
)

// scriptedProvider is a fake llm.ModelProvider that returns a scripted
// sequence of stream events. The script is consumed once per Stream call;
// multiple Stream calls replay the same script.
type scriptedProvider struct {
	mu     sync.Mutex
	script [][]llm.StreamEvent // outer = per-call; inner = events of one call
	calls  int
}

func (s *scriptedProvider) Name() string { return "scripted" }

func (s *scriptedProvider) Complete(_ context.Context, _ *llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return nil, errors.New("not implemented")
}

func (s *scriptedProvider) Stream(_ context.Context, _ *llm.CompletionRequest) (*llm.StreamingResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.calls
	s.calls++
	if idx >= len(s.script) {
		idx = len(s.script) - 1
	}
	events := make(chan llm.StreamEvent, len(s.script[idx])+1)
	for _, ev := range s.script[idx] {
		events <- ev
	}
	close(events)
	sr := llm.NewStreamingResponse(events, nil)
	return sr, nil
}

// scriptedStreamErrProvider fails immediately with a given setup error.
type scriptedStreamErrProvider struct{ err error }

func (s *scriptedStreamErrProvider) Name() string { return "scripted-err" }
func (s *scriptedStreamErrProvider) Complete(_ context.Context, _ *llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return nil, s.err
}
func (s *scriptedStreamErrProvider) Stream(_ context.Context, _ *llm.CompletionRequest) (*llm.StreamingResponse, error) {
	return nil, s.err
}

type fakeDeps struct {
	sess         *session.Session
	tools        *tool.Registry
	provider     llm.ModelProvider
	modelName    string
	instructions string
	cfg          Config
	bus          *event.Bus
}

func (f *fakeDeps) Session() *session.Session   { return f.sess }
func (f *fakeDeps) Tools() *tool.Registry       { return f.tools }
func (f *fakeDeps) Provider() llm.ModelProvider { return f.provider }
func (f *fakeDeps) ModelName() string           { return f.modelName }
func (f *fakeDeps) Instructions() string        { return f.instructions }
func (f *fakeDeps) Config() Config              { return f.cfg }
func (f *fakeDeps) Emit(ev event.Event)         { f.bus.Emit(ev) }

func newFakeDeps(t *testing.T, provider llm.ModelProvider, regs []tool.Tool) *fakeDeps {
	t.Helper()
	reg := tool.NewRegistry()
	for _, r := range regs {
		reg.MustRegister(r)
	}
	return &fakeDeps{
		sess:         session.NewSession("test"),
		tools:        reg,
		provider:     provider,
		modelName:    "fake-model",
		instructions: "you are a test",
		cfg:          Config{MaxTurns: 5, ToolTimeout: time.Second},
		bus:          event.NewBus(),
	}
}

type echoTool struct{}

func (echoTool) Name() string        { return "echo" }
func (echoTool) Description() string { return "echo args back" }
func (echoTool) Parameters() llm.ParameterSchema {
	return llm.ParameterSchema{Type: "object", Properties: map[string]llm.ParameterProperty{"text": {Type: "string"}}, Required: []string{"text"}}
}
func (echoTool) Execute(_ context.Context, args map[string]any) tool.Result {
	return tool.Result{Content: "echoed:" + args["text"].(string)}
}

func TestSingleTurnStop(t *testing.T) {
	prov := &scriptedProvider{script: [][]llm.StreamEvent{
		{
			llm.StartEvent{Partial: llm.AssistantMessage{Model: "fake-model"}},
			llm.TextDeltaEvent{Delta: "hello"},
			llm.TextDeltaEvent{Delta: " world"},
			llm.DoneEvent{Response: llm.CompletionResponse{FinishReason: llm.FinishReasonStop}},
		},
	}}
	d := newFakeDeps(t, prov, nil)
	sub := d.bus.Subscribe(64)
	defer sub.Unsubscribe()

	ex := New()
	if err := ex.RunConversation(context.Background(), d); err != nil {
		t.Fatalf("RunConversation: %v", err)
	}
	// RunConversation is invoked AFTER the user message is appended by the
	// dispatcher; here we start from an empty session, so only the
	// assistant is expected.
	if d.sess.Len() != 1 {
		t.Fatalf("session len = %d, want 1 (assistant only — user is appended by dispatcher)", d.sess.Len())
	}
	last := d.sess.Messages()[0]
	if last.Role != llm.RoleAssistant {
		t.Errorf("last role = %q, want assistant", last.Role)
	}
	if last.Content != "hello world" {
		t.Errorf("last content = %q, want 'hello world'", last.Content)
	}
	// emit sequence: TurnStart, LLMStart, TextDelta x2, LLMEnd, TurnEnd
	seen := drainEvents(t, sub, 6)
	if seen[0].(event.TurnStartEvent).TurnIndex != 1 {
		t.Errorf("first event = %T %+v, want TurnStartEvent", seen[0], seen[0])
	}
	if seen[len(seen)-1].(event.TurnEndEvent).StopReason != llm.FinishReasonStop {
		t.Errorf("last event = %+v, want StopReason=stop", seen[len(seen)-1])
	}
}

func TestMultiTurnToolUse(t *testing.T) {
	prov := &scriptedProvider{script: [][]llm.StreamEvent{
		// turn 1: model asks to call echo("hi")
		{
			llm.StartEvent{},
			llm.ToolCallStartEvent{ID: "c1", Name: "echo"},
			llm.ToolCallEndEvent{ID: "c1", Name: "echo", Arguments: map[string]any{"text": "hi"}},
			llm.DoneEvent{Response: llm.CompletionResponse{FinishReason: llm.FinishReasonToolCalls}},
		},
		// turn 2: model says stop
		{
			llm.StartEvent{},
			llm.TextDeltaEvent{Delta: "done"},
			llm.DoneEvent{Response: llm.CompletionResponse{FinishReason: llm.FinishReasonStop}},
		},
	}}
	d := newFakeDeps(t, prov, []tool.Tool{echoTool{}})
	sub := d.bus.Subscribe(64)
	defer sub.Unsubscribe()

	ex := New()
	if err := ex.RunConversation(context.Background(), d); err != nil {
		t.Fatalf("RunConversation: %v", err)
	}
	// expected session: user + assistant(tc) + tool + assistant(stop) = 4
	// (RunConversation is called after user is already appended; here we
	// start from an empty session, so we expect 3: assistant(tc) + tool + assistant(stop))
	if got := d.sess.Len(); got != 3 {
		t.Fatalf("session len = %d, want 3", got)
	}
	msgs := d.sess.Messages()
	if msgs[0].Role != llm.RoleAssistant || len(msgs[0].ToolCalls) != 1 {
		t.Errorf("msgs[0] = %+v, want assistant with 1 tool call", msgs[0])
	}
	if msgs[1].Role != llm.RoleTool || msgs[1].ToolCallID != "c1" {
		t.Errorf("msgs[1] = %+v, want tool role with c1", msgs[1])
	}
	if msgs[2].Role != llm.RoleAssistant || msgs[2].Content != "done" {
		t.Errorf("msgs[2] = %+v, want assistant 'done'", msgs[2])
	}
}

func TestMaxTurnsExceeded(t *testing.T) {
	// script: always returns a tool call → loops until MaxTurns
	script := [][]llm.StreamEvent{}
	for i := 0; i < 10; i++ {
		script = append(script, []llm.StreamEvent{
			llm.StartEvent{},
			llm.ToolCallStartEvent{ID: "c1", Name: "echo"},
			llm.ToolCallEndEvent{ID: "c1", Name: "echo", Arguments: map[string]any{"text": "x"}},
			llm.DoneEvent{Response: llm.CompletionResponse{FinishReason: llm.FinishReasonToolCalls}},
		})
	}
	prov := &scriptedProvider{script: script}
	d := newFakeDeps(t, prov, []tool.Tool{echoTool{}})
	d.cfg.MaxTurns = 3

	ex := New()
	err := ex.RunConversation(context.Background(), d)
	if err == nil {
		t.Fatal("expected max-turns error")
	}
}

func TestCtxCancelDuringStream(t *testing.T) {
	// script emits one text delta, then would emit more — we cancel mid-stream.
	// Build a provider that emits a delta then blocks until ctx fires.
	blocking := &blockingProvider{}
	d := newFakeDeps(t, blocking, nil)
	d.cfg.MaxTurns = 5

	ctx, cancel := context.WithCancel(context.Background())
	ex := New()
	done := make(chan error, 1)
	go func() {
		done <- ex.RunConversation(ctx, d)
	}()
	// wait for at least one event to flow (the start + a text delta or so)
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunConversation did not return after ctx cancel")
	}
	// session should contain a (possibly partial) assistant with aborted finish
	// — the executor appends assistant + emits TurnEnd before returning
	if d.sess.Len() < 1 {
		t.Errorf("session len = %d, want >= 1", d.sess.Len())
	}
}

func TestStreamSetupError(t *testing.T) {
	prov := &scriptedStreamErrProvider{err: errors.New("boom")}
	d := newFakeDeps(t, prov, nil)
	ex := New()
	err := ex.RunConversation(context.Background(), d)
	if err == nil {
		t.Fatal("expected setup error")
	}
}

// drainEvents reads up to n events from sub, failing the test if it can't.
func drainEvents(t *testing.T, sub *event.Subscription, n int) []event.Event {
	t.Helper()
	out := make([]event.Event, 0, n)
	timeout := time.After(time.Second)
	for len(out) < n {
		select {
		case ev, ok := <-sub.C():
			if !ok {
				t.Fatalf("subscription channel closed after %d events", len(out))
			}
			out = append(out, ev)
		case <-timeout:
			t.Fatalf("timed out after %d events; want %d", len(out), n)
		}
	}
	return out
}

// blockingProvider emits a single TextDelta then blocks until ctx fires,
// then closes the channel so the consumer can observe the cancel.
type blockingProvider struct{}

func (b *blockingProvider) Name() string { return "blocking" }
func (b *blockingProvider) Complete(_ context.Context, _ *llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return nil, errors.New("not implemented")
}
func (b *blockingProvider) Stream(ctx context.Context, _ *llm.CompletionRequest) (*llm.StreamingResponse, error) {
	ch := make(chan llm.StreamEvent, 2)
	ch <- llm.StartEvent{}
	ch <- llm.TextDeltaEvent{Delta: "partial..."}
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return llm.NewStreamingResponse(ch, nil), nil
}
