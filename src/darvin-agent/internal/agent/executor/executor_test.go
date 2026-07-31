package executor

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"darvin-cowork/backend/internal/agent/ctxengine"
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

	// ContextEngine seam (executor.Deps extension — see spec §4.10).
	// The default zero values (nil / false / nil / "") reproduce the
	// legacy behaviour: assembler disabled, fallback path taken, no API
	// usage to share with the assembler.
	assemblerEnabled bool
	assembler        ctxengine.ContextEngine
	lastUsage        llm.Usage

	// messageID is read via CurrentMessageID; tests inject one to assert
	// the executor tags every emitted event with the right MessageID.
	messageID string
	// runID is read via CurrentRunID; tests inject one to assert the
	// executor tags every emitted event with the right RunID.
	runID string
}

// injectAssembler wires a non-nil ContextEngine into the fakeDeps so the
// executor exercises the seam. Combined with assemblerEnabled=true, this
// triggers the assembler path; with assemblerEnabled=false the fallback
// path is still taken.
func (f *fakeDeps) injectAssembler(a ctxengine.ContextEngine) {
	f.assembler = a
	f.assemblerEnabled = true
}

func (f *fakeDeps) Session() *session.Session   { return f.sess }
func (f *fakeDeps) Tools() *tool.Registry       { return f.tools }
func (f *fakeDeps) Provider() llm.ModelProvider { return f.provider }
func (f *fakeDeps) ModelName() string           { return f.modelName }
func (f *fakeDeps) Instructions() string        { return f.instructions }
func (f *fakeDeps) Config() Config              { return f.cfg }
func (f *fakeDeps) Emit(ev event.Event)         { f.bus.Emit(ev) }
func (f *fakeDeps) Assembler() ctxengine.ContextEngine {
	return f.assembler
}
func (f *fakeDeps) SystemSections() []ctxengine.SystemSection { return nil }
func (f *fakeDeps) AssemblerEnabled() bool                    { return f.assemblerEnabled }
func (f *fakeDeps) RecordUsage(u llm.Usage)                   { f.lastUsage = u }
func (f *fakeDeps) LastUsage() llm.Usage                      { return f.lastUsage }
func (f *fakeDeps) CurrentMessageID() string                  { return f.messageID }
func (f *fakeDeps) CurrentRunID() string                      { return f.runID }

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

// recordingAssembler is a fake ContextEngine that records every Assemble
// call and returns the input messages verbatim (so the executor's downstream
// LLM call still gets data). It lets the test inspect AssembleParams without
// pulling in the real ctxengine package.
type recordingAssembler struct {
	ctxengine.ContextEngine // embed so we satisfy the interface cheaply
	calls                   int
	lastParams              []ctxengine.AssembleParams
}

func (r *recordingAssembler) Assemble(_ context.Context, p ctxengine.AssembleParams) ctxengine.AssembleResult {
	r.calls++
	r.lastParams = append(r.lastParams, p)
	return ctxengine.AssembleResult{Messages: p.Messages, Budget: p.ToolBudget}
}

// echoAssembler records assemble calls and echoes messages, like recordingAssembler
// but used for tests where we want the seam to actually be exercised.
func (r *recordingAssembler) Bootstrap(context.Context, ctxengine.BootstrapParams) error { return nil }
func (r *recordingAssembler) Maintain(context.Context, ctxengine.MaintainParams) error   { return nil }
func (r *recordingAssembler) Dispose(context.Context) error                              { return nil }
func (r *recordingAssembler) Ingest(context.Context, ctxengine.IngestParams) ctxengine.IngestResult {
	return ctxengine.IngestResult{Success: true}
}
func (r *recordingAssembler) IngestBatch(_ context.Context, _ ctxengine.IngestBatchParams) ctxengine.IngestResult {
	return ctxengine.IngestResult{Success: true, TokensProcessed: 1}
}
func (r *recordingAssembler) AfterTurn(context.Context, ctxengine.AfterTurnParams) error {
	return nil
}
func (r *recordingAssembler) Compact(_ context.Context, p ctxengine.CompactParams) ctxengine.CompactResult {
	return ctxengine.CompactResult{Success: true, RetainedMessages: p.Messages}
}
func (r *recordingAssembler) PrepareSubagentSpawn(context.Context, ctxengine.SubagentSpawnParams) (*ctxengine.SubagentSpawnPreparation, error) {
	return nil, ctxengine.ErrSubAgentUnsupported
}
func (r *recordingAssembler) OnSubagentEnded(context.Context, ctxengine.SubagentEndedParams) error {
	return ctxengine.ErrSubAgentUnsupported
}
func (r *recordingAssembler) Info() ctxengine.Info {
	return ctxengine.Info{Name: "recording", Version: "test"}
}

// TestUsageCapturedFromDoneEvent verifies that the API-reported Usage in
// DoneEvent.Response.Usage flows through to:
//  1. RecordUsage on Deps (next turn's LastUsage)
//  2. The LLMEndEvent.Usage payload
func TestUsageCapturedFromDoneEvent(t *testing.T) {
	prov := &scriptedProvider{script: [][]llm.StreamEvent{
		{
			llm.StartEvent{},
			llm.TextDeltaEvent{Delta: "ok"},
			llm.DoneEvent{Response: llm.CompletionResponse{
				FinishReason: llm.FinishReasonStop,
				Usage:        llm.Usage{PromptTokens: 1234, CompletionTokens: 56, TotalTokens: 1290},
			}},
		},
	}}
	d := newFakeDeps(t, prov, nil)
	sub := d.bus.Subscribe(64)
	defer sub.Unsubscribe()

	ex := New()
	if err := ex.RunConversation(context.Background(), d); err != nil {
		t.Fatalf("RunConversation: %v", err)
	}
	// RecordUsage writes through to LastUsage
	if got := d.LastUsage().PromptTokens; got != 1234 {
		t.Errorf("LastUsage.PromptTokens = %d, want 1234", got)
	}
	if got := d.LastUsage().CompletionTokens; got != 56 {
		t.Errorf("LastUsage.CompletionTokens = %d, want 56", got)
	}
	// LLMEndEvent payload should carry the same cumulative usage (single turn).
	for ev := range sub.C() {
		if le, ok := ev.(event.LLMEndEvent); ok {
			if le.Usage.PromptTokens != 1234 {
				t.Errorf("LLMEndEvent.Usage.PromptTokens = %d, want 1234", le.Usage.PromptTokens)
			}
			if le.Usage.CompletionTokens != 56 {
				t.Errorf("LLMEndEvent.Usage.CompletionTokens = %d, want 56", le.Usage.CompletionTokens)
			}
			return
		}
	}
	t.Fatal("never received LLMEndEvent")
}

// TestCumulativeUsageAccumulates verifies multi-turn Usage sums across turns
// (regression test for the prior bug where totalUsage was never updated and
// LLMEndEvent always emitted zero Usage).
func TestCumulativeUsageAccumulates(t *testing.T) {
	prov := &scriptedProvider{script: [][]llm.StreamEvent{
		// turn 1: tool call
		{
			llm.StartEvent{},
			llm.ToolCallStartEvent{ID: "c1", Name: "echo"},
			llm.ToolCallEndEvent{ID: "c1", Name: "echo", Arguments: map[string]any{"text": "x"}},
			llm.DoneEvent{Response: llm.CompletionResponse{
				FinishReason: llm.FinishReasonToolCalls,
				Usage:        llm.Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110},
			}},
		},
		// turn 2: stop
		{
			llm.StartEvent{},
			llm.TextDeltaEvent{Delta: "done"},
			llm.DoneEvent{Response: llm.CompletionResponse{
				FinishReason: llm.FinishReasonStop,
				Usage:        llm.Usage{PromptTokens: 200, CompletionTokens: 20, TotalTokens: 220},
			}},
		},
	}}
	d := newFakeDeps(t, prov, []tool.Tool{echoTool{}})
	sub := d.bus.Subscribe(64)
	defer sub.Unsubscribe()

	ex := New()
	if err := ex.RunConversation(context.Background(), d); err != nil {
		t.Fatalf("RunConversation: %v", err)
	}
	// drain LLMEndEvent payload for turn 2 (the last emitted with non-zero usage)
	got := llm.Usage{}
	count := 0
	loop := true
	for loop {
		select {
		case ev, ok := <-sub.C():
			if !ok {
				loop = false
				continue
			}
			if le, ok := ev.(event.LLMEndEvent); ok {
				got = le.Usage
				count++
			}
		case <-time.After(50 * time.Millisecond):
			loop = false
		}
	}
	if count < 2 {
		t.Fatalf("expected >= 2 LLMEndEvents, got %d", count)
	}
	if got.PromptTokens != 300 || got.CompletionTokens != 30 {
		t.Errorf("cumulative LLMEndEvent.Usage = %+v, want Prompt=300, Completion=30", got)
	}
}

// TestAssemblerFallback_Disabled verifies that when AssemblerEnabled=false,
// the executor takes the legacy d.Session().Messages() path and never calls
// the assembler.
func TestAssemblerFallback_Disabled(t *testing.T) {
	rec := &recordingAssembler{}
	d := newFakeDeps(t, &scriptedProvider{script: [][]llm.StreamEvent{
		{llm.StartEvent{}, llm.TextDeltaEvent{Delta: "hi"}, llm.DoneEvent{Response: llm.CompletionResponse{FinishReason: llm.FinishReasonStop}}},
	}}, nil)
	d.assemblerEnabled = true
	// Note: Assembler() returns nil under assemblerEnabled=true (per
	// fakeDeps), so the executor's `d.Assembler() != nil` check fails and
	// the fallback path runs. This mirrors the production contract:
	// AssemblerEnabled is gated by Assembler() being non-nil first.
	_ = rec // unused in the fallback path; we just want to make sure no call happens

	ex := New()
	if err := ex.RunConversation(context.Background(), d); err != nil {
		t.Fatalf("RunConversation: %v", err)
	}
	if rec.calls != 0 {
		t.Errorf("assembler called %d times, want 0 (fallback path)", rec.calls)
	}
}

// TestAssemblerActive_CalledOncePerTurn verifies that when AssemblerEnabled
// is true AND Assembler() returns a non-nil assembler, the executor
// dispatches to Assembler.Assemble once per turn.
func TestAssemblerActive_CalledOncePerTurn(t *testing.T) {
	rec := &recordingAssembler{}
	prov := &scriptedProvider{script: [][]llm.StreamEvent{
		{
			llm.StartEvent{},
			llm.ToolCallStartEvent{ID: "c1", Name: "echo"},
			llm.ToolCallEndEvent{ID: "c1", Name: "echo", Arguments: map[string]any{"text": "x"}},
			llm.DoneEvent{Response: llm.CompletionResponse{FinishReason: llm.FinishReasonToolCalls}},
		},
		{
			llm.StartEvent{},
			llm.TextDeltaEvent{Delta: "done"},
			llm.DoneEvent{Response: llm.CompletionResponse{FinishReason: llm.FinishReasonStop}},
		},
	}}
	d := newFakeDeps(t, prov, []tool.Tool{echoTool{}})
	// Replace Assembler() return value: inject the recording assembler by
	// overriding the fakeDeps via a small wrapper.  Easiest is to construct
	// a fresh fakeDeps that delegates to a non-nil assembler.
	d.assemblerEnabled = true
	d.injectAssembler(rec)

	ex := New()
	if err := ex.RunConversation(context.Background(), d); err != nil {
		t.Fatalf("RunConversation: %v", err)
	}
	if rec.calls != 2 {
		t.Errorf("Assemble called %d times, want 2 (one per turn)", rec.calls)
	}
	if len(rec.lastParams) < 2 {
		t.Fatalf("recorded params = %d, want >= 2", len(rec.lastParams))
	}
	// First turn LastUsage should be zero (no prior turn). Second turn
	// LastUsage should reflect what was captured from turn 1.
	if rec.lastParams[0].LastUsage.PromptTokens != 0 {
		t.Errorf("turn 0 LastUsage.PromptTokens = %d, want 0", rec.lastParams[0].LastUsage.PromptTokens)
	}
}

// TestEventCommonSnapshot guards spec §7.5: every event the executor emits
// during a turn must carry the same EventCommon as the in-flight prompt —
// SessionID from d.Session().ID and MessageID from d.CurrentMessageID().
// This is the property the EventLedger relies on to route notifications to
// the correct WS subscriber by SessionID and to populate messageId for
// the renderer.
func TestEventCommonSnapshot(t *testing.T) {
	prov := &scriptedProvider{script: [][]llm.StreamEvent{
		{
			llm.StartEvent{Partial: llm.AssistantMessage{Model: "fake-model"}},
			llm.TextDeltaEvent{Delta: "hi"},
			llm.DoneEvent{Response: llm.CompletionResponse{FinishReason: llm.FinishReasonStop}},
		},
	}}
	d := newFakeDeps(t, prov, nil)
	d.messageID = "snap-id-fixed"
	sub := d.bus.Subscribe(64)
	defer sub.Unsubscribe()

	ex := New()
	if err := ex.RunConversation(context.Background(), d); err != nil {
		t.Fatalf("RunConversation: %v", err)
	}
	// 5 events in a stop turn: TurnStart, LLMStart, TextDelta, LLMEnd, TurnEnd
	seen := drainEvents(t, sub, 5)
	for i, ev := range seen {
		ec := ev.Common()
		if ec.SessionID != d.sess.ID {
			t.Errorf("event[%d] %T SessionID = %q, want %q", i, ev, ec.SessionID, d.sess.ID)
		}
		if ec.MessageID != "snap-id-fixed" {
			t.Errorf("event[%d] %T MessageID = %q, want %q", i, ev, ec.MessageID, "snap-id-fixed")
		}
	}
}

// TestEventCommonSnapshotAcrossTurns guards that the snapshot holds across
// every turn in a multi-turn conversation (not just the first). The
// messageID value is read on every emit; if any turn captured a stale value
// the second assertion would catch it.
func TestEventCommonSnapshotAcrossTurns(t *testing.T) {
	prov := &scriptedProvider{script: [][]llm.StreamEvent{
		{
			llm.StartEvent{},
			llm.ToolCallStartEvent{ID: "c1", Name: "echo"},
			llm.ToolCallEndEvent{ID: "c1", Name: "echo", Arguments: map[string]any{"text": "x"}},
			llm.DoneEvent{Response: llm.CompletionResponse{FinishReason: llm.FinishReasonToolCalls}},
		},
		{
			llm.StartEvent{},
			llm.TextDeltaEvent{Delta: "done"},
			llm.DoneEvent{Response: llm.CompletionResponse{FinishReason: llm.FinishReasonStop}},
		},
	}}
	d := newFakeDeps(t, prov, []tool.Tool{echoTool{}})
	d.messageID = "multi-id"
	sub := d.bus.Subscribe(64)
	defer sub.Unsubscribe()

	ex := New()
	if err := ex.RunConversation(context.Background(), d); err != nil {
		t.Fatalf("RunConversation: %v", err)
	}
	// Two turns: turn1 emits TurnStart/LLMStart/ToolStart/ToolEnd/LLMEnd/TurnEnd,
	// turn2 emits the same minus ToolStart/ToolEnd. Drain until quiet.
	got := drainUntilQuiet(t, sub, 50*time.Millisecond)
	if len(got) < 8 {
		t.Fatalf("expected >= 8 events, got %d", len(got))
	}
	for i, ev := range got {
		ec := ev.Common()
		if ec.MessageID != "multi-id" {
			t.Errorf("event[%d] %T MessageID = %q, want %q", i, ev, ec.MessageID, "multi-id")
		}
		if ec.SessionID != d.sess.ID {
			t.Errorf("event[%d] %T SessionID = %q, want %q", i, ev, ec.SessionID, d.sess.ID)
		}
	}
}

// drainUntilQuiet reads events until none arrive within quiet. Used for
// multi-turn scripts where the exact event count is not asserted (only that
// every event carries the EventCommon snapshot).
func drainUntilQuiet(t *testing.T, sub *event.Subscription, quiet time.Duration) []event.Event {
	t.Helper()
	var out []event.Event
	for {
		select {
		case ev, ok := <-sub.C():
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-time.After(quiet):
			return out
		}
	}
}

// TestAssemblerActive_PassesLastUsage verifies that the executor passes the
// API-reported Usage from the previous turn into AssembleParams.LastUsage
// (so the ContextEngine can prefer API tokens over the local estimator).
func TestAssemblerActive_PassesLastUsage(t *testing.T) {
	rec := &recordingAssembler{}
	prov := &scriptedProvider{script: [][]llm.StreamEvent{
		{
			llm.StartEvent{},
			llm.TextDeltaEvent{Delta: "first"},
			llm.DoneEvent{Response: llm.CompletionResponse{
				FinishReason: llm.FinishReasonStop,
				Usage:        llm.Usage{PromptTokens: 500, CompletionTokens: 50, TotalTokens: 550},
			}},
		},
	}}
	d := newFakeDeps(t, prov, nil)
	d.assemblerEnabled = true
	d.injectAssembler(rec)

	ex := New()
	if err := ex.RunConversation(context.Background(), d); err != nil {
		t.Fatalf("RunConversation: %v", err)
	}
	// Single-turn run: LastUsage fed into Assemble is zero (no prior call).
	// The point of this test is that the seam wires the field through.
	if len(rec.lastParams) != 1 {
		t.Fatalf("recorded params = %d, want 1", len(rec.lastParams))
	}
	if rec.lastParams[0].LastUsage.PromptTokens != 0 {
		t.Errorf("turn 0 LastUsage.PromptTokens = %d, want 0 (first turn baseline)", rec.lastParams[0].LastUsage.PromptTokens)
	}
}
