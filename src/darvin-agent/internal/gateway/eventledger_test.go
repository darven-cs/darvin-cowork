package gateway

import (
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agent/event"
	"darvin-cowork/backend/internal/agent/llm"
)

func TestSubscribeAndUnsubscribeAll(t *testing.T) {
	l := NewEventLedger(zap.NewNop())
	// Two clients, one session. UnsubscribeAll(c1) should leave only c2
	// in the subscriber set.
	c1, c2 := &client{}, &client{}
	l.Subscribe("s1", c1)
	l.Subscribe("s1", c2)
	l.Subscribe("s2", c1)

	l.UnsubscribeAll(c1)

	l.mu.RLock()
	defer l.mu.RUnlock()
	if _, ok := l.bySession["s1"][c1]; ok {
		t.Fatalf("c1 still subscribed to s1")
	}
	if _, ok := l.bySession["s1"][c2]; !ok {
		t.Fatalf("c2 should still be subscribed to s1")
	}
	if _, ok := l.bySession["s2"]; ok {
		t.Fatalf("s2 set should be deleted (only c1 was on it)")
	}
}

func TestSubscribeIdempotent(t *testing.T) {
	l := NewEventLedger(zap.NewNop())
	c := &client{}
	l.Subscribe("s1", c)
	l.Subscribe("s1", c)
	if got := len(l.bySession["s1"]); got != 1 {
		t.Fatalf("expected set size 1, got %d", got)
	}
}

func TestMapEventToTSShapes(t *testing.T) {
	td := mapEventToTS(event.TextDeltaEvent{Delta: "x"}, "s1")
	m, ok := td.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", td)
	}
	if m["type"] != "text_delta" || m["delta"] != "x" {
		t.Fatalf("text_delta shape: %+v", m)
	}

	end := mapEventToTS(event.AgentEndEvent{
		EventBase: event.EventBase{EventCommon: event.EventCommon{SessionID: "s1"}},
	}, "s1")
	m, _ = end.(map[string]any)
	if m["type"] != "agent_end" {
		t.Fatalf("agent_end shape: %+v", m)
	}

	// Unknown type falls through to the default branch.
	other := mapEventToTS(event.RunStartEvent{
		EventBase: event.EventBase{EventCommon: event.EventCommon{SessionID: "s1"}},
	}, "s1")
	m, _ = other.(map[string]any)
	if m["type"] != "run_start" {
		t.Fatalf("default shape: %+v", m)
	}
}

// TestMapEventToTSCarriesMessageID pins the field names the renderer keys
// on (src/shared/darvin-api.ts). A drift here is invisible in Go but
// silently breaks the UI: a delta lands on no message, or an error leaves
// the bubble spinning with no text.
func TestMapEventToTSCarriesMessageID(t *testing.T) {
	ec := event.EventBase{EventCommon: event.EventCommon{SessionID: "s1", MessageID: "m1"}}

	cases := []struct {
		name string
		ev   event.Event
		want map[string]any
	}{
		{
			name: "text_delta",
			ev:   event.TextDeltaEvent{EventBase: ec, Delta: "x"},
			want: map[string]any{"type": "text_delta", "delta": "x", "messageId": "m1"},
		},
		{
			name: "thinking_delta",
			ev:   event.ThinkingDeltaEvent{EventBase: ec, Delta: "t"},
			want: map[string]any{"type": "thinking_delta", "delta": "t", "messageId": "m1"},
		},
		{
			name: "llm_end maps to done",
			ev:   event.LLMEndEvent{EventBase: ec},
			want: map[string]any{"type": "done", "messageId": "m1"},
		},
		{
			name: "agent_error maps to error",
			ev:   event.AgentErrorEvent{EventBase: ec, Err: errors.New("boom")},
			want: map[string]any{"type": "error", "messageId": "m1", "message": "boom"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := mapEventToTS(tc.ev, "s1").(map[string]any)
			if !ok {
				t.Fatalf("expected map")
			}
			if len(got) != len(tc.want) {
				t.Fatalf("field set = %+v, want %+v", got, tc.want)
			}
			for k, want := range tc.want {
				if got[k] != want {
					t.Errorf("%s = %v, want %v", k, got[k], want)
				}
			}
		})
	}
}

// TestMapEventToTSDoneUsage pins down FR-4: when an LLMEndEvent carries
// non-zero Usage, the resulting `done` notification gains a `usage`
// block. When Usage is zero, the block is omitted (no
// `{totalTokens: 0}` noise on every smoke-test turn).
func TestMapEventToTSDoneUsage(t *testing.T) {
	ec := event.EventBase{EventCommon: event.EventCommon{SessionID: "s1", MessageID: "m1"}}

	t.Run("zero usage omits block", func(t *testing.T) {
		got := mapEventToTS(event.LLMEndEvent{EventBase: ec}, "s1").(map[string]any)
		if _, present := got["usage"]; present {
			t.Errorf("usage block present for zero Usage; got %+v", got)
		}
	})

	t.Run("non-zero usage emits block", func(t *testing.T) {
		got := mapEventToTS(event.LLMEndEvent{
			EventBase: ec,
			Usage: llm.Usage{
				PromptTokens:     12,
				CompletionTokens: 34,
				TotalTokens:      46,
			},
		}, "s1").(map[string]any)
		u, ok := got["usage"].(map[string]any)
		if !ok {
			t.Fatalf("usage not a map; got %T (%+v)", got["usage"], got)
		}
		if u["inputTokens"] != 12 {
			t.Errorf("inputTokens = %v, want 12", u["inputTokens"])
		}
		if u["outputTokens"] != 34 {
			t.Errorf("outputTokens = %v, want 34", u["outputTokens"])
		}
		if u["totalTokens"] != 46 {
			t.Errorf("totalTokens = %v, want 46", u["totalTokens"])
		}
	})
}

// TestEmitStubDeliversNotifications pairs a real WebSocket server with a
// real *client; EmitStub's goroutine writes two frames which the test
// reads back via the server-side forwarder and asserts on.
func TestEmitStubDeliversNotifications(t *testing.T) {
	conn, got := dialEchoServer(t)
	defer conn.Close()

	c := &client{conn: conn, log: zap.NewNop()}
	l := NewEventLedger(zap.NewNop())
	l.fakeDelay = 5 * time.Millisecond
	l.Subscribe("s1", c)
	l.EmitStub("s1", "m1", "hi")

	wantTypes := []string{"text_delta", "agent_end"}
	for i, want := range wantTypes {
		select {
		case msg := <-got:
			if !contains(msg, `"type":"`+want+`"`) {
				t.Fatalf("frame %d = %s, missing type %q", i, msg, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout waiting for frame %d (%s)", i, want)
		}
	}
}

func contains(haystack []byte, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == needle {
			return true
		}
	}
	return false
}
