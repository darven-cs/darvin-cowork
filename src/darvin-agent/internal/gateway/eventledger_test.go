package gateway

import (
	"testing"
	"time"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agent/event"
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

	end := mapEventToTS(event.AgentEndEvent{SessionID: "s1"}, "s1")
	m, _ = end.(map[string]any)
	if m["type"] != "agent_end" {
		t.Fatalf("agent_end shape: %+v", m)
	}

	// Unknown type falls through to the default branch.
	other := mapEventToTS(event.RunStartEvent{SessionID: "s1"}, "s1")
	m, _ = other.(map[string]any)
	if m["type"] != "run_start" {
		t.Fatalf("default shape: %+v", m)
	}
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
