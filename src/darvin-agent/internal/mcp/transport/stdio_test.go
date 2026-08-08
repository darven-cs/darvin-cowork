// Tests for the stdio transport.

package transport

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// waitFor polls cond every 20ms until it returns true or the timeout elapses.
func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

func TestStdioConnect_SetsAlive(t *testing.T) {
	tp := &StdioTransport{Command: "cat", Logger: zap.NewNop()}
	if err := tp.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tp.Close()

	if !tp.Alive() {
		t.Fatal("transport should be alive after Connect")
	}
}

func TestStdio_NotConnected_RecvReturnsClosed(t *testing.T) {
	tp := &StdioTransport{Command: "cat", Logger: zap.NewNop()}
	_, err := tp.Recv(context.Background())
	if err != ErrTransportClosed {
		t.Fatalf("err = %v, want ErrTransportClosed", err)
	}
}

func TestStdio_NotConnected_SendReturnsClosed(t *testing.T) {
	tp := &StdioTransport{Command: "cat", Logger: zap.NewNop()}
	err := tp.Send(context.Background(), []byte("anything"))
	if err != ErrTransportClosed {
		t.Fatalf("err = %v, want ErrTransportClosed", err)
	}
}

func TestStdioClose_IdempotentAndDead(t *testing.T) {
	tp := &StdioTransport{Command: "cat", Logger: zap.NewNop()}
	if err := tp.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := tp.Close(); err != nil {
		t.Fatal(err)
	}
	if tp.Alive() {
		t.Fatal("expected alive=false after Close")
	}
	if err := tp.Close(); err != nil {
		t.Fatalf("second Close must be a no-op, got %v", err)
	}
}

func TestStdio_ChildCrash_TransitionsToDead(t *testing.T) {
	tp := &StdioTransport{Command: "sh", Args: []string{"-c", "exit 1"}, Logger: zap.NewNop()}
	if err := tp.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tp.Close()

	if !waitFor(2*time.Second, func() bool { return !tp.Alive() }) {
		t.Fatal("transport should mark itself dead after child exit")
	}
}

func TestStdio_Notification_NoID_NotAwaited(t *testing.T) {
	// Notifications have no id field; Send should write and return immediately
	// without waiting for any response. Notifications are logged as notifications.
	tp := &StdioTransport{Command: "cat", Logger: zap.NewNop()}
	if err := tp.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tp.Close()

	// A JSON-RPC notification has no "id" key. Send appends a newline and
	// writes to cat. Since cat does not add a JSON-RPC response wrapper,
	// Send treats this as a notification and returns immediately.
	notification := []byte(`{"jsonrpc":"2.0","method":"notifications/sample"}`)
	if err := tp.Send(context.Background(), notification); err != nil {
		t.Fatal(err)
	}
	// Send should return immediately (no blocking on pending channel).
}

func TestStdio_SendWithID_EchoedBack(t *testing.T) {
	// Use node to echo a JSON-RPC response wrapping the incoming request id.
	script := `node -e "process.stdin.on('data',d=>{const l=d.toString().trim();if(l){try{const r=JSON.parse(l);process.stdout.write(JSON.stringify({jsonrpc:'2.0',id:r.id,result:'ok'})+'\n');}catch(e){}}});process.stdin.resume();"`
	tp := &StdioTransport{
		Command: "sh",
		Args:    []string{"-c", script},
		Logger:  zap.NewNop(),
	}
	if err := tp.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tp.Close()

	body := []byte(`{"jsonrpc":"2.0","id":42}`)
	if err := tp.Send(context.Background(), body); err != nil {
		t.Fatal(err)
	}

	got, err := tp.Recv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var resp map[string]any
	if err := json.Unmarshal(got.Body, &resp); err != nil {
		t.Fatalf("not JSON: %v, body=%q", err, got.Body)
	}
	if resp["id"] != float64(42) {
		t.Fatalf("wrong id in response: got %v want 42", resp["id"])
	}
}

func newObservedLogger() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zap.DebugLevel)
	return zap.New(core), logs
}
