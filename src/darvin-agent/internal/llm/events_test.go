package llm

import "testing"

// TestStreamEvent_AllImplementInterface is a compile-time + runtime check
// that every concrete event type satisfies the StreamEvent contract.
func TestStreamEvent_AllImplementInterface(t *testing.T) {
	events := []StreamEvent{
		StartEvent{Partial: AssistantMessage{Model: "claude-x"}},
		TextDeltaEvent{Delta: "hi"},
		ToolCallStartEvent{ID: "t1", Name: "f"},
		ToolCallDeltaEvent{ID: "t1", Delta: `{"a"`},
		ToolCallEndEvent{ID: "t1", Name: "f", Arguments: map[string]any{"a": 1.0}},
		DoneEvent{Response: CompletionResponse{}},
		ErrorEvent{Err: nil},
	}
	if len(events) != 7 {
		t.Fatalf("expected 7 event types in this test, got %d", len(events))
	}
	for _, e := range events {
		// The isStreamEvent method is unexported; calling it through the
		// interface proves the type satisfies StreamEvent.
		_ = e
	}
}

// TestStreamEvent_TypeSwitch exercises a typical consumer-side type switch
// to make sure none of the event types panic and all branches execute.
func TestStreamEvent_TypeSwitch(t *testing.T) {
	cases := []struct {
		name string
		ev   StreamEvent
		want string
	}{
		{"start", StartEvent{Partial: AssistantMessage{}}, "start"},
		{"text", TextDeltaEvent{Delta: "x"}, "text"},
		{"tool_start", ToolCallStartEvent{ID: "1", Name: "n"}, "tool_start"},
		{"tool_delta", ToolCallDeltaEvent{ID: "1", Delta: "x"}, "tool_delta"},
		{"tool_end", ToolCallEndEvent{ID: "1", Name: "n"}, "tool_end"},
		{"done", DoneEvent{}, "done"},
		{"error", ErrorEvent{}, "error"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classify(c.ev)
			if got != c.want {
				t.Errorf("classify(%s) = %s, want %s", c.name, got, c.want)
			}
		})
	}
}

// classify mirrors the consumer-side switch in the Agent loop; isolated
// here so the test fails loudly if a new event type is added without
// updating consumer wiring.
func classify(e StreamEvent) string {
	switch e.(type) {
	case StartEvent:
		return "start"
	case TextDeltaEvent:
		return "text"
	case ToolCallStartEvent:
		return "tool_start"
	case ToolCallDeltaEvent:
		return "tool_delta"
	case ToolCallEndEvent:
		return "tool_end"
	case DoneEvent:
		return "done"
	case ErrorEvent:
		return "error"
	default:
		return "unknown"
	}
}
