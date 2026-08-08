// Tests for the WS server start, port output, and shutdown behavior.

package gateway

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// TestServerStartPrintsPort spins a Server and a pipe-backed stdout
// capture. The port line must be the only line on stdout.
func TestServerStartPrintsPort(t *testing.T) {
	h, _ := newTestHandler(t)
	gs := NewServer(h, zap.NewNop(), WithPort(0))

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	oldStdout := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = oldStdout })

	if err := gs.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = gs.Shutdown(ctx)
	})

	// Close writer; the side that reads the captured output is on r.
	_ = w.Close()

	scanner := bufio.NewScanner(r)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) != 1 {
		t.Fatalf("expected 1 stdout line, got %d: %v", len(lines), lines)
	}
	line := lines[0]
	if !strings.HasPrefix(line, "<port>") || !strings.HasSuffix(line, "</port>") {
		t.Fatalf("bad port line: %q", line)
	}
	portStr := strings.TrimSuffix(strings.TrimPrefix(line, "<port>"), "</port>")
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 {
		t.Fatalf("bad port: %q", portStr)
	}
	if port != gs.Port() {
		t.Fatalf("port mismatch: stdout=%d gs.Port()=%d", port, gs.Port())
	}
}

// TestServerShutdownReturnsCleanly confirms the 3s-budget Shutdown path
// used by main.go's signal handler.
func TestServerShutdownReturnsCleanly(t *testing.T) {
	h, _ := newTestHandler(t)
	gs := NewServer(h, zap.NewNop(), WithPort(0))
	if err := gs.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := gs.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

// TestServerWSAcceptsConnections wires a real WS client to /ws and
// confirms a prompt round-trips through handleWS. The server is
// started with its real listener, not httptest, so this also covers
// the "no httptest behind the WS upgrade" path that main.go uses.
func TestServerWSAcceptsConnections(t *testing.T) {
	h, _ := newTestHandler(t)
	h.Ledger.fakeDelay = 5 * time.Millisecond
	gs := NewServer(h, zap.NewNop(), WithPort(0))

	// Capture stdout so the test doesn't pollute the test runner's output.
	r, w, _ := os.Pipe()
	oldStdout := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = oldStdout })
	if err := gs.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = gs.Shutdown(ctx)
	})
	_ = w.Close()
	go func() { _, _ = bufio.NewReader(r).ReadString('\n') }()

	host := gs.listener.Addr().(*net.TCPAddr).IP.String()
	port := gs.Port()
	wsURL := "ws://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/ws"

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v status=%v", err, resp)
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string]any{
		"jsonrpc": "2.0", "id": "1", "method": "agent.prompt",
		"params": map[string]any{"content": "hi"},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(msg), `"result":{`) {
		t.Fatalf("no result in response: %s", msg)
	}
}

// resp is captured for debugging on dial error; the discard avoids
// "declared and not used" while keeping the call site readable.
var _ = (*http.Response)(nil)
