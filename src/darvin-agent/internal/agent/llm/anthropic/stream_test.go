package anthropic

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"darvin-cowork/backend/internal/agent/llm"
)

func TestParseToolArgs_Empty(t *testing.T) {
	got := parseToolArgs("")
	if len(got) != 0 {
		t.Errorf("empty input should yield empty map, got %+v", got)
	}
}

func TestParseToolArgs_Whitespace(t *testing.T) {
	got := parseToolArgs("   \n\t  ")
	if len(got) != 0 {
		t.Errorf("whitespace input should yield empty map, got %+v", got)
	}
}

func TestParseToolArgs_ValidJSON(t *testing.T) {
	got := parseToolArgs(`{"location":"SF","unit":"celsius"}`)
	want := map[string]any{"location": "SF", "unit": "celsius"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestParseToolArgs_InvalidJSON(t *testing.T) {
	// Fallback path: invalid JSON yields a synthetic envelope so the
	// caller still sees the raw bytes via the "_raw" key.
	got := parseToolArgs(`not json`)
	if got["_raw"] != "not json" {
		t.Errorf("expected _raw key, got %+v", got)
	}
	if _, ok := got["_error"]; !ok {
		t.Errorf("expected _error key, got %+v", got)
	}
}

func TestParseToolArgs_NullJSON(t *testing.T) {
	got := parseToolArgs("null")
	if len(got) != 0 {
		t.Errorf("null should yield empty map, got %+v", got)
	}
}

func TestAnthropicErrorCode(t *testing.T) {
	cases := map[string]string{
		"authentication_error":  llm.ErrCodeAuth,
		"rate_limit_error":      llm.ErrCodeRateLimit,
		"invalid_request_error": llm.ErrCodeInvalidRequest,
		"model_error":           llm.ErrCodeInternal,
		"":                      llm.ErrCodeInternal,
	}
	for in, want := range cases {
		if got := anthropicErrorCode(in); got != want {
			t.Errorf("anthropicErrorCode(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestDispatch_StateMachine walks the full state machine through
// runStream with a synthetic SSE body, verifying the unified event
// sequence and the final DoneEvent payload.
func TestDispatch_StateMachine(t *testing.T) {
	frames := strings.Join([]string{
		`event: message_start`,
		`data: {"message":{"id":"msg_1","model":"claude-x","usage":{"input_tokens":7,"output_tokens":0}}}`,
		``,
		`event: content_block_start`,
		`data: {"index":0,"type":"content_block_start","content_block":{"type":"text"}}`,
		``,
		`event: content_block_delta`,
		`data: {"index":0,"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello"}}`,
		``,
		`event: content_block_delta`,
		`data: {"index":0,"type":"content_block_delta","delta":{"type":"text_delta","text":" world"}}`,
		``,
		`event: content_block_start`,
		`data: {"index":1,"type":"content_block_start","content_block":{"type":"tool_use","id":"toolu_1","name":"f"}}`,
		``,
		`event: content_block_delta`,
		`data: {"index":1,"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{\"a\""}}`,
		``,
		`event: content_block_delta`,
		`data: {"index":1,"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":":1}"}}`,
		``,
		`event: content_block_stop`,
		`data: {"index":1,"type":"content_block_stop"}`,
		``,
		`event: message_delta`,
		`data: {"delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":12}}`,
		``,
		`event: content_block_stop`,
		`data: {"index":0,"type":"content_block_stop"}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
		``,
	}, "\n")

	r := strings.NewReader(frames)
	events := make(chan llm.StreamEvent, 32)
	if err := runStream(testCtx(), r, events, "claude-x"); err != nil {
		t.Fatalf("runStream: %v", err)
	}
	close(events)

	var got []llm.StreamEvent
	for ev := range events {
		got = append(got, ev)
	}

	// Expected sequence:
	//   StartEvent, TextDelta, TextDelta, ToolCallStart, ToolCallDelta,
	//   ToolCallDelta, ToolCallEnd, DoneEvent
	wantTypes := []string{
		"StartEvent",
		"TextDeltaEvent", "TextDeltaEvent",
		"ToolCallStartEvent",
		"ToolCallDeltaEvent", "ToolCallDeltaEvent",
		"ToolCallEndEvent",
		"DoneEvent",
	}
	if len(got) != len(wantTypes) {
		t.Fatalf("got %d events, want %d: %+v", len(got), len(wantTypes), eventTypes(got))
	}
	for i, want := range wantTypes {
		if got := typeName(got[i]); got != want {
			t.Errorf("event[%d] type = %s, want %s", i, got, want)
		}
	}

	// Validate specific payloads.
	start := got[0].(llm.StartEvent)
	if start.Partial.Model != "claude-x" {
		t.Errorf("StartEvent model = %q", start.Partial.Model)
	}

	text1 := got[1].(llm.TextDeltaEvent)
	text2 := got[2].(llm.TextDeltaEvent)
	if text1.Delta+text2.Delta != "Hello world" {
		t.Errorf("text concatenation = %q", text1.Delta+text2.Delta)
	}

	tcs := got[5].(llm.ToolCallDeltaEvent)
	tce := got[6].(llm.ToolCallEndEvent)
	if tcs.ID != "toolu_1" {
		t.Errorf("delta ID = %q", tcs.ID)
	}
	if tce.Name != "f" {
		t.Errorf("end name = %q", tce.Name)
	}
	if !reflect.DeepEqual(tce.Arguments, map[string]any{"a": 1.0}) {
		t.Errorf("end arguments = %+v", tce.Arguments)
	}

	done := got[7].(llm.DoneEvent)
	if done.Response.Content != "Hello world" {
		t.Errorf("DoneEvent content = %q", done.Response.Content)
	}
	if len(done.Response.ToolCalls) != 1 {
		t.Fatalf("DoneEvent tool calls = %d", len(done.Response.ToolCalls))
	}
	if done.Response.FinishReason != llm.FinishReasonToolCalls {
		t.Errorf("DoneEvent finish reason = %v", done.Response.FinishReason)
	}
	if done.Response.Usage.PromptTokens != 7 {
		t.Errorf("DoneEvent prompt tokens = %d", done.Response.Usage.PromptTokens)
	}
	if done.Response.Usage.CompletionTokens != 12 {
		t.Errorf("DoneEvent completion tokens = %d", done.Response.Usage.CompletionTokens)
	}
}

// TestDispatch_ErrorFrame verifies that an Anthropic SSE error frame
// surfaces as a ProviderError from runStream.
func TestDispatch_ErrorFrame(t *testing.T) {
	frames := strings.Join([]string{
		`event: error`,
		`data: {"error":{"type":"rate_limit_error","message":"slow down"}}`,
		``,
		``,
	}, "\n")

	events := make(chan llm.StreamEvent, 4)
	err := runStream(testCtx(), strings.NewReader(frames), events, "claude-x")
	if err == nil {
		t.Fatal("expected error from error frame")
	}
	if !llm.IsCode(err, llm.ErrCodeRateLimit) {
		t.Errorf("expected ErrCodeRateLimit, got %v", err)
	}
	close(events)
}

// TestDispatch_PingIgnored verifies ping frames produce no events.
func TestDispatch_PingIgnored(t *testing.T) {
	frames := strings.Join([]string{
		`event: ping`,
		`data: {}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
		``,
	}, "\n")

	events := make(chan llm.StreamEvent, 8)
	if err := runStream(testCtx(), strings.NewReader(frames), events, "claude-x"); err != nil {
		t.Fatalf("runStream: %v", err)
	}
	close(events)

	var got []llm.StreamEvent
	for ev := range events {
		got = append(got, ev)
	}
	// Should only see DoneEvent (no StartEvent because message_start was
	// absent — runStream synthesises it defensively).
	for _, ev := range got {
		if _, ok := ev.(llm.TextDeltaEvent); ok {
			t.Errorf("unexpected text delta from ping-only stream")
		}
	}
}

// TestDispatch_ThinkingDelta verifies the extended-thinking branch: a
// content_block_delta whose delta.type is "thinking_delta" carries its
// text in the "thinking" field (not "text") and must surface as a
// ThinkingDeltaEvent, kept distinct from ordinary text deltas.
func TestDispatch_ThinkingDelta(t *testing.T) {
	frames := strings.Join([]string{
		`event: message_start`,
		`data: {"message":{"id":"msg_1","model":"claude-x","usage":{"input_tokens":0,"output_tokens":0}}}`,
		``,
		`event: content_block_start`,
		`data: {"index":0,"type":"content_block_start","content_block":{"type":"thinking","thinking":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"index":0,"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"step one"}}`,
		``,
		`event: content_block_delta`,
		`data: {"index":0,"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":" step two"}}`,
		``,
		`event: content_block_stop`,
		`data: {"index":0,"type":"content_block_stop"}`,
		``,
		`event: content_block_start`,
		`data: {"index":1,"type":"content_block_start","content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"index":1,"type":"content_block_delta","delta":{"type":"text_delta","text":"answer"}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
		``,
	}, "\n")

	events := make(chan llm.StreamEvent, 16)
	if err := runStream(testCtx(), strings.NewReader(frames), events, "claude-x"); err != nil {
		t.Fatalf("runStream: %v", err)
	}
	close(events)

	var thinking, text []string
	for ev := range events {
		switch e := ev.(type) {
		case llm.ThinkingDeltaEvent:
			thinking = append(thinking, e.Delta)
		case llm.TextDeltaEvent:
			text = append(text, e.Delta)
		}
	}
	if got := strings.Join(thinking, ""); got != "step one step two" {
		t.Errorf("thinking deltas = %q, want %q", got, "step one step two")
	}
	if got := strings.Join(text, ""); got != "answer" {
		t.Errorf("text deltas = %q, want %q", got, "answer")
	}
}

// TestDispatch_TruncatedToolArgs verifies that an in-flight tool buffer
// is flushed on stream end even when the content_block_stop frame is
// missing.
func TestDispatch_TruncatedToolArgs(t *testing.T) {
	frames := strings.Join([]string{
		`event: message_start`,
		`data: {"message":{"id":"msg_1","model":"claude-x","usage":{"input_tokens":0,"output_tokens":0}}}`,
		``,
		`event: content_block_start`,
		`data: {"index":0,"type":"content_block_start","content_block":{"type":"tool_use","id":"toolu_2","name":"f"}}`,
		``,
		`event: content_block_delta`,
		`data: {"index":0,"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{\"q\":\"hi\"}"}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
		``,
	}, "\n")

	events := make(chan llm.StreamEvent, 16)
	if err := runStream(testCtx(), strings.NewReader(frames), events, "claude-x"); err != nil {
		t.Fatalf("runStream: %v", err)
	}
	close(events)

	var got []llm.StreamEvent
	for ev := range events {
		got = append(got, ev)
	}
	// Expect: StartEvent, ToolCallStart, ToolCallDelta, DoneEvent
	// (no ToolCallEnd because content_block_stop was missing).
	hasEnd := false
	for _, ev := range got {
		if _, ok := ev.(llm.ToolCallEndEvent); ok {
			hasEnd = true
		}
	}
	if hasEnd {
		t.Errorf("did not expect ToolCallEnd when stop frame missing")
	}
	// The DoneEvent should still carry the tool call.
	var done llm.DoneEvent
	for _, ev := range got {
		if d, ok := ev.(llm.DoneEvent); ok {
			done = d
		}
	}
	if len(done.Response.ToolCalls) != 1 {
		t.Fatalf("DoneEvent expected 1 tool call (flushed), got %d", len(done.Response.ToolCalls))
	}
	if !reflect.DeepEqual(done.Response.ToolCalls[0].Arguments, map[string]any{"q": "hi"}) {
		t.Errorf("flushed tool args = %+v", done.Response.ToolCalls[0].Arguments)
	}
}

func typeName(ev llm.StreamEvent) string {
	switch ev.(type) {
	case llm.StartEvent:
		return "StartEvent"
	case llm.TextDeltaEvent:
		return "TextDeltaEvent"
	case llm.ToolCallStartEvent:
		return "ToolCallStartEvent"
	case llm.ToolCallDeltaEvent:
		return "ToolCallDeltaEvent"
	case llm.ToolCallEndEvent:
		return "ToolCallEndEvent"
	case llm.DoneEvent:
		return "DoneEvent"
	case llm.ErrorEvent:
		return "ErrorEvent"
	default:
		return "unknown"
	}
}

func eventTypes(evs []llm.StreamEvent) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = typeName(e)
	}
	return out
}

// testCtx returns a non-cancelled context for stream tests.
func testCtx() context.Context { return context.Background() }
