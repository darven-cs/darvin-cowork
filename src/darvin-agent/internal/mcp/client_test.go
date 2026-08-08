// Tests for the JSON-RPC client framing and serialization.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"darvin-cowork/backend/internal/mcp/transport"
)

// fakeTransport is a programmable Transport for client tests. Each step
// in `script` is consumed in order on Recv; the matching frame (or
// error) is returned. After the script runs out, additional Recv calls
// fail with a clear "script exhausted" error so tests fail loudly
// instead of hanging.
type fakeTransport struct {
	mu     sync.Mutex
	script []fakeStep
	sent   [][]byte
	closed atomic.Bool
	alive  atomic.Bool
	// recvErr, when non-nil, is returned by every Recv after the script
	// is exhausted. Lets a test model a "stays broken forever" transport
	// without sizing the script precisely.
	recvErr error
}

type fakeStep struct {
	// body is returned verbatim when onRecv is nil. When onRecv is set,
	// the body field is ignored and onRecv(lastSent) is used instead.
	body  []byte
	err   error
	delay time.Duration
	// onRecv lets a test synthesize a response from the request body
	// (e.g. echo back the request id so response-id matching passes).
	onRecv func(req []byte) []byte
}

func newFakeTransport(steps ...fakeStep) *fakeTransport {
	ft := &fakeTransport{script: steps}
	ft.alive.Store(true)
	return ft
}

func (f *fakeTransport) Connect(_ context.Context) error { f.alive.Store(true); return nil }

func (f *fakeTransport) Send(_ context.Context, body []byte) error {
	if f.closed.Load() || !f.alive.Load() {
		return transport.ErrTransportClosed
	}
	f.mu.Lock()
	cp := make([]byte, len(body))
	copy(cp, body)
	f.sent = append(f.sent, cp)
	f.mu.Unlock()
	return nil
}

func (f *fakeTransport) Recv(_ context.Context) (transport.Frame, error) {
	if f.closed.Load() || !f.alive.Load() {
		return transport.Frame{}, transport.ErrTransportClosed
	}
	f.mu.Lock()
	if len(f.script) == 0 {
		err := f.recvErr
		f.mu.Unlock()
		if err != nil {
			return transport.Frame{}, err
		}
		return transport.Frame{}, errors.New("fakeTransport: script exhausted")
	}
	step := f.script[0]
	f.script = f.script[1:]
	// Snapshot the most recent sent body so onRecv can build a
	// response that matches the request id.
	var lastSent []byte
	if len(f.sent) > 0 {
		lastSent = f.sent[len(f.sent)-1]
	}
	f.mu.Unlock()

	if step.delay > 0 {
		time.Sleep(step.delay)
	}
	if step.err != nil {
		return transport.Frame{}, step.err
	}
	if step.onRecv != nil {
		return transport.Frame{Body: step.onRecv(lastSent)}, nil
	}
	return transport.Frame{Body: step.body}, nil
}

func (f *fakeTransport) Close() error {
	f.closed.Store(true)
	f.alive.Store(false)
	return nil
}

func (f *fakeTransport) Alive() bool { return f.alive.Load() }

// sentSnapshot returns a copy of what was sent to keep tests from racing
// on the live slice.
func (f *fakeTransport) sentSnapshot() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]byte, len(f.sent))
	copy(out, f.sent)
	return out
}

// --- Call / Initialize / ListTools / CallTool ---

func TestClient_Call_RoundtripReturnsResult(t *testing.T) {
	ft := newFakeTransport(fakeStep{
		body: []byte(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`),
	})
	c := NewClient(ft)

	raw, err := c.Call(context.Background(), "tools/list", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"ok":true}` {
		t.Fatalf("raw result = %s", raw)
	}
}

func TestClient_Call_SendsValidJSONRPCEnvelope(t *testing.T) {
	ft := newFakeTransport(fakeStep{
		body: []byte(`{"jsonrpc":"2.0","id":1,"result":{}}`),
	})
	c := NewClient(ft)
	if _, err := c.Call(context.Background(), "tools/list", map[string]any{"foo": "bar"}); err != nil {
		t.Fatal(err)
	}

	sent := ft.sentSnapshot()
	if len(sent) != 1 {
		t.Fatalf("sent = %d frames, want 1", len(sent))
	}
	var got map[string]any
	if err := json.Unmarshal(sent[0], &got); err != nil {
		t.Fatalf("unmarshal sent: %v", err)
	}
	if got["jsonrpc"] != "2.0" {
		t.Errorf("jsonrpc = %v, want 2.0", got["jsonrpc"])
	}
	if got["method"] != "tools/list" {
		t.Errorf("method = %v, want tools/list", got["method"])
	}
	if _, ok := got["id"]; !ok {
		t.Error("missing id field")
	}
}

func TestClient_Call_AssignsMonotonicIDs(t *testing.T) {
	ft := newFakeTransport(
		fakeStep{body: []byte(`{"jsonrpc":"2.0","id":1,"result":{}}`)},
		fakeStep{body: []byte(`{"jsonrpc":"2.0","id":2,"result":{}}`)},
		fakeStep{body: []byte(`{"jsonrpc":"2.0","id":3,"result":{}}`)},
	)
	c := NewClient(ft)
	for i := 0; i < 3; i++ {
		if _, err := c.Call(context.Background(), "m", nil); err != nil {
			t.Fatal(err)
		}
	}
	ids := make([]int64, 0, 3)
	for _, body := range ft.sentSnapshot() {
		var r Request
		if err := json.Unmarshal(body, &r); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.ID)
	}
	if len(ids) != 3 || ids[0] >= ids[1] || ids[1] >= ids[2] {
		t.Fatalf("ids = %v, want strictly increasing", ids)
	}
}

func TestClient_Call_RPCErrorPropagates(t *testing.T) {
	ft := newFakeTransport(fakeStep{
		body: []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"method not found"}}`),
	})
	c := NewClient(ft)

	_, err := c.Call(context.Background(), "nope", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("err type = %T, want *RPCError", err)
	}
	if rpcErr.Code != -32601 {
		t.Errorf("code = %d, want -32601", rpcErr.Code)
	}
	if rpcErr.Message != "method not found" {
		t.Errorf("message = %q", rpcErr.Message)
	}
}

func TestClient_Call_ResponseIDMismatch_Errors(t *testing.T) {
	ft := newFakeTransport(fakeStep{
		body: []byte(`{"jsonrpc":"2.0","id":999,"result":{}}`),
	})
	c := NewClient(ft)
	_, err := c.Call(context.Background(), "m", nil)
	if err == nil {
		t.Fatal("expected id mismatch error")
	}
	if !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("err = %v", err)
	}
}

func TestClient_Call_TransportNotAlive_Errors(t *testing.T) {
	ft := newFakeTransport()
	ft.alive.Store(false)
	c := NewClient(ft)
	_, err := c.Call(context.Background(), "m", nil)
	if !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("err = %v, want ErrTransportClosed", err)
	}
}

func TestClient_Initialize_HandshakeShape(t *testing.T) {
	ft := newFakeTransport(fakeStep{
		body: []byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"x","version":"1.0"}}}`),
	})
	c := NewClient(ft)
	res, err := c.Initialize(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.ProtocolVersion != "2024-11-05" {
		t.Errorf("ProtocolVersion = %q", res.ProtocolVersion)
	}
	if res.ServerInfo.Name != "x" {
		t.Errorf("ServerInfo.Name = %q", res.ServerInfo.Name)
	}

	sent := ft.sentSnapshot()
	if len(sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(sent))
	}
	var req Request
	if err := json.Unmarshal(sent[0], &req); err != nil {
		t.Fatal(err)
	}
	if req.Method != "initialize" {
		t.Errorf("method = %q, want initialize", req.Method)
	}
	params, _ := json.Marshal(req.Params)
	for _, want := range []string{`"protocolVersion":"2024-11-05"`, `"name":"darvin-cowork"`} {
		if !strings.Contains(string(params), want) {
			t.Errorf("params missing %s: %s", want, params)
		}
	}
}

func TestClient_ListTools_ParsesToolList(t *testing.T) {
	ft := newFakeTransport(fakeStep{
		body: []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"read_file","description":"r","inputSchema":{"type":"object"}},{"name":"write_file","description":"w","inputSchema":{"type":"object"}}]}}`),
	})
	c := NewClient(ft)
	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 {
		t.Fatalf("len(tools) = %d, want 2", len(tools))
	}
	if tools[0].Name != "read_file" {
		t.Errorf("first tool = %q", tools[0].Name)
	}
}

func TestClient_CallTool_ParseResult(t *testing.T) {
	ft := newFakeTransport(fakeStep{
		body: []byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"hello"}]}}`),
	})
	c := NewClient(ft)
	res, err := c.CallTool(context.Background(), "echo", map[string]any{"msg": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Error("IsError should be false")
	}
	if len(res.Content) != 1 || res.Content[0].Text != "hello" {
		t.Fatalf("content = %+v", res.Content)
	}
}

// TestClient_ConcurrentCall_Serialized verifies the client mutex
// prevents two Call()s from interleaving on the underlying transport.
// Without the mutex, both would race for the same Send/Recv pair.
func TestClient_ConcurrentCall_Serialized(t *testing.T) {
	ft := newFakeTransport(
		fakeStep{body: []byte(`{"jsonrpc":"2.0","id":1,"result":{}}`)},
		fakeStep{body: []byte(`{"jsonrpc":"2.0","id":2,"result":{}}`)},
		fakeStep{body: []byte(`{"jsonrpc":"2.0","id":3,"result":{}}`)},
		fakeStep{body: []byte(`{"jsonrpc":"2.0","id":4,"result":{}}`)},
	)
	c := NewClient(ft)

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Call(context.Background(), "m", nil); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent call err: %v", err)
	}
}

func TestClient_Close_ClosesTransport(t *testing.T) {
	ft := newFakeTransport()
	c := NewClient(ft)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if ft.Alive() {
		t.Fatal("transport should be dead after Client.Close")
	}
}

func TestIsConnectionError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"transport closed", ErrTransportClosed, true},
		{"io.EOF", io.EOF, true},
		{"unexpected EOF", io.ErrUnexpectedEOF, true},
		{"rpc error", &RPCError{Code: -32601, Message: "x"}, false},
		{"connection refused msg", errors.New("dial tcp: connection refused"), true},
		{"connection reset msg", errors.New("read: connection reset by peer"), true},
		{"broken pipe msg", errors.New("write: broken pipe"), true},
		{"random err", errors.New("nope"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isConnectionError(tc.err); got != tc.want {
				t.Errorf("isConnectionError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
