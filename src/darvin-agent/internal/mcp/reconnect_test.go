package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"darvin-cowork/backend/internal/mcp/transport"
)

// TestRetry_RecoversOnConnectionError: first Call returns io.EOF, factory
// rebuilds the transport, second Call succeeds.
func TestRetry_RecoversOnConnectionError(t *testing.T) {
	first := newFakeTransport(
		fakeStep{err: io.EOF},
	)
	var factoryCalls atomic.Int32
	factory := func() (transport.Transport, error) {
		factoryCalls.Add(1)
		// The rebuilt transport echoes back whatever id the Client sends
		// so the response-id check passes.
		return newEchoFakeTransport(), nil
	}

	c := NewClient(first).WithReconnectFactory(factory)
	raw, err := c.CallWithRetry(context.Background(), "m", nil, 3, time.Millisecond)
	if err != nil {
		t.Fatalf("CallWithRetry err = %v", err)
	}
	if string(raw) != `{"recovered":true}` {
		t.Fatalf("raw = %s, want recovered", raw)
	}
	if factoryCalls.Load() != 1 {
		t.Fatalf("factory called %d times, want 1", factoryCalls.Load())
	}
}

// TestRetry_DoesNotRetryRPCError: first Call returns *RPCError, retry
// should give up immediately and not call the factory.
func TestRetry_DoesNotRetryRPCError(t *testing.T) {
	ft := newFakeTransport(
		fakeStep{body: []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"nope"}}`)},
	)
	var factoryCalls atomic.Int32
	factory := func() (transport.Transport, error) {
		factoryCalls.Add(1)
		return newFakeTransport(), nil
	}

	c := NewClient(ft).WithReconnectFactory(factory)
	_, err := c.CallWithRetry(context.Background(), "m", nil, 3, time.Millisecond)
	if err == nil {
		t.Fatal("expected error")
	}
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("err type = %T, want *RPCError", err)
	}
	if factoryCalls.Load() != 0 {
		t.Fatalf("factory called %d times, want 0 (RPC errors must not retry)", factoryCalls.Load())
	}
}

// TestRetry_MaxRetriesExceeded: 3 connection errors → returns
// ErrRPCMaxRetries. Factory gets called 3 times (one per retry).
func TestRetry_MaxRetriesExceeded(t *testing.T) {
	// First call returns io.EOF, factory rebuilds transports that all
	// return io.EOF on the first Recv → all 3 retries fail.
	ft := newFakeTransport(
		fakeStep{err: io.EOF},
	)
	var factoryCalls atomic.Int32
	factory := func() (transport.Transport, error) {
		factoryCalls.Add(1)
		return newFakeTransport(fakeStep{err: io.EOF}), nil
	}

	c := NewClient(ft).WithReconnectFactory(factory)
	_, err := c.CallWithRetry(context.Background(), "m", nil, 3, time.Millisecond)
	if err == nil {
		t.Fatal("expected max-retries error")
	}
	if !errors.Is(err, ErrRPCMaxRetries) {
		t.Fatalf("err = %v, want ErrRPCMaxRetries", err)
	}
	// 3 retries after the initial attempt = 3 factory calls.
	if got := factoryCalls.Load(); got != 3 {
		t.Fatalf("factory called %d times, want 3", got)
	}
}

// TestRetry_NoFactory_ConnectionError: connection error but no factory
// configured → still retries up to maxRetries, then returns
// ErrRPCMaxRetries wrapping the reconnect failure.
func TestRetry_NoFactory_ConnectionError(t *testing.T) {
	ft := newFakeTransport()
	ft.recvErr = io.EOF

	c := NewClient(ft)
	_, err := c.CallWithRetry(context.Background(), "m", nil, 2, time.Millisecond)
	if !errors.Is(err, ErrRPCMaxRetries) {
		t.Fatalf("err = %v, want ErrRPCMaxRetries", err)
	}
}

// TestRetry_BackoffExponential: verify elapsed time roughly matches
// 1+2+4 = 7 base units (here 7ms total). Loose bounds to avoid CI flake.
func TestRetry_BackoffExponential(t *testing.T) {
	ft := newFakeTransport()
	ft.recvErr = io.EOF
	factory := func() (transport.Transport, error) {
		nt := newFakeTransport()
		nt.recvErr = io.EOF
		return nt, nil
	}

	c := NewClient(ft).WithReconnectFactory(factory)
	base := 20 * time.Millisecond
	start := time.Now()
	_, _ = c.CallWithRetry(context.Background(), "m", nil, 3, base)
	elapsed := time.Since(start)
	// Expected: 20 + 40 + 80 = 140ms minimum.
	if elapsed < 100*time.Millisecond {
		t.Fatalf("elapsed = %v, want >= 100ms (backoff should have accumulated)", elapsed)
	}
}

// newEchoFakeTransport returns a fakeTransport that, on Recv, copies
// whatever id the Client put on the wire into a canned success body.
// Used by recovery tests so the response-id check passes.
func newEchoFakeTransport() *fakeTransport {
	return newFakeTransport(fakeStep{onRecv: func(req []byte) []byte {
		var r Request
		_ = json.Unmarshal(req, &r)
		return []byte(`{"jsonrpc":"2.0","id":` + strconv.FormatInt(r.ID, 10) + `,"result":{"recovered":true}}`)
	}})
}
