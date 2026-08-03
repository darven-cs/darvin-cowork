package transport

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func newObservedLogger() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zap.DebugLevel)
	return zap.New(core), logs
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

func TestStdioSendRecv_FrameRoundtrip(t *testing.T) {
	tp := &StdioTransport{Command: "cat", Logger: zap.NewNop()}
	if err := tp.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tp.Close()

	body := []byte(`{"jsonrpc":"2.0","id":1,"result":42}`)
	if err := tp.Send(context.Background(), body); err != nil {
		t.Fatal(err)
	}

	got, err := tp.Recv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Body) != string(body) {
		t.Fatalf("body mismatch: got %q want %q", got.Body, body)
	}
}

func TestStdioSendRecv_MultipleFrames(t *testing.T) {
	tp := &StdioTransport{Command: "cat", Logger: zap.NewNop()}
	if err := tp.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tp.Close()

	for i := 0; i < 3; i++ {
		body := []byte(`{"jsonrpc":"2.0","id":` + strconv.Itoa(i) + `}`)
		if err := tp.Send(context.Background(), body); err != nil {
			t.Fatal(err)
		}
		got, err := tp.Recv(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if string(got.Body) != string(body) {
			t.Fatalf("frame %d: got %q want %q", i, got.Body, body)
		}
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

func TestStdio_Stderr_DrainedToLogger(t *testing.T) {
	log, observed := newObservedLogger()
	tp := &StdioTransport{
		Command: "sh",
		Args:    []string{"-c", "echo boom-stderr >&2; cat"},
		Logger:  log,
	}
	if err := tp.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tp.Close()

	body := []byte("echoed")
	if err := tp.Send(context.Background(), body); err != nil {
		t.Fatal(err)
	}
	got, err := tp.Recv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Body) != string(body) {
		t.Fatalf("body mismatch: got %q want %q", got.Body, body)
	}

	if !waitFor(2*time.Second, func() bool {
		for _, entry := range observed.All() {
			if entry.Message != "mcp-stdio-stderr" {
				continue
			}
			if line := getFieldString(entry.Context, "line"); line == "boom-stderr" {
				return true
			}
		}
		return false
	}) {
		t.Fatalf("expected stderr line in log, got %d entries", observed.Len())
	}
}

func TestStdio_RecvMissingContentLength_Errors(t *testing.T) {
	tp := &StdioTransport{
		Command: "sh",
		// cat keeps the child alive so its exit cannot race the alive flag
		// against Recv reading the malformed frame.
		Args:   []string{"-c", `printf 'no-headers-here\n\n'; cat`},
		Logger: zap.NewNop(),
	}
	if err := tp.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tp.Close()

	_, err := tp.Recv(context.Background())
	if err == nil {
		t.Fatal("expected error for missing Content-Length")
	}
	if !strings.Contains(err.Error(), "missing Content-Length") {
		t.Fatalf("err = %v, want it to mention missing Content-Length", err)
	}
}

func TestStdio_ReconnectSameTransport(t *testing.T) {
	tp := &StdioTransport{Command: "cat", Logger: zap.NewNop()}
	if err := tp.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"id":1}`)
	if err := tp.Send(context.Background(), body); err != nil {
		t.Fatal(err)
	}
	got, err := tp.Recv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Body) != string(body) {
		t.Fatalf("body mismatch: got %q want %q", got.Body, body)
	}
	if err := tp.Close(); err != nil {
		t.Fatal(err)
	}

	if err := tp.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tp.Close()
	if err := tp.Send(context.Background(), body); err != nil {
		t.Fatal(err)
	}
	got, err = tp.Recv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Body) != string(body) {
		t.Fatalf("body mismatch: got %q want %q", got.Body, body)
	}
}

// waitFor polls cond every 20ms until it returns true or the timeout
// elapses. Used to bridge the gap between subprocess exit and the
// transport's alive-flip goroutine.
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

// getFieldString pulls a single zap context field as a string, returning
// "" when the field is missing or of an unexpected type.
func getFieldString(fields []zap.Field, name string) string {
	for _, f := range fields {
		if f.Key != name {
			continue
		}
		if s, ok := f.Interface.(string); ok {
			return s
		}
		if f.String != "" {
			return f.String
		}
	}
	return ""
}
