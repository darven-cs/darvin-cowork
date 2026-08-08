// Tests for WebSocket client frame writing and notification sends.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// TestSendNotificationFormatsCorrectFrame writes a notification via
// SendNotification and asserts the wire format. The receiver is a real
// *websocket.Conn dialed against an httptest server that reads the
// inbound frame and forwards it through a channel — that's how the
// test inspects the bytes the client sent.
func TestSendNotificationFormatsCorrectFrame(t *testing.T) {
	conn, got := dialEchoServer(t)
	defer func() { _ = conn.Close() }()

	c := &client{conn: conn, log: zap.NewNop()}
	c.SendNotification("agent.event", map[string]any{"type": "text_delta", "delta": "x"})

	select {
	case msg := <-got:
		var n Notification
		if err := json.Unmarshal(msg, &n); err != nil {
			t.Fatalf("unmarshal: %v: %s", err, msg)
		}
		if n.Method != "agent.event" {
			t.Fatalf("method: %q", n.Method)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for notification")
	}
}

// TestWriteJSONRefusesNilConn is the regression test for the panic
// observed when EmitStub's goroutine fires after run's defer Close:
// a nil c.conn must short-circuit WriteJSON with errClosed, not panic
// inside the gorilla/websocket package.
func TestWriteJSONRefusesNilConn(t *testing.T) {
	c := &client{conn: nil, log: zap.NewNop()}
	if err := c.writeJSON(map[string]any{"x": 1}); err == nil {
		t.Fatalf("expected errClosed, got nil")
	}
}

// dialEchoServer returns a client *websocket.Conn to a server that
// reads each inbound frame and pushes it to the got channel. The
// caller owns both; the test reads from got to assert on what the
// client wrote.
func dialEchoServer(t *testing.T) (*websocket.Conn, <-chan []byte) {
	t.Helper()
	upg := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	got := make(chan []byte, 4)
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upg.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		// Read frames in a loop. Each frame is forwarded to got for
		// the test to inspect. The loop ends when the peer closes;
		// that happens on t.Cleanup.
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			select {
			case got <- msg:
			case <-done:
				return
			}
		}
	}))
	t.Cleanup(func() {
		close(done)
		srv.Close()
	})

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return conn, got
}
