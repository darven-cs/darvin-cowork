// Tests for the crash-recovery path in Registry.CallTool.

package mcp

import (
	"context"
	"testing"
	"time"

	"darvin-cowork/backend/internal/mcp/transport"
)

const (
	reconnectInitResp = `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"fake","version":"1.0"}}}`
	reconnectListResp = `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"read_file","description":"r","inputSchema":{"type":"object"}}]}}`
	reconnectCallResp = `{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"ok"}]}}`
)

// TestRegistry_CallToolRecoversAfterCrash wires a fake transport whose first
// incarnation serves the handshake then dies on tools/call, and whose second
// incarnation (the reconnect) serves everything. CallTool must transparently
// re-establish and retry instead of surfacing the transport error.
func TestRegistry_CallToolRecoversAfterCrash(t *testing.T) {
	builds := 0
	reg := NewRegistry(noopResolverManager(t), NewInMemoryResolutionPersistence()).WithTransportBuilder(
		func(ServerSpec, LaunchResolution) transport.Transport {
			builds++
			if builds == 1 {
				// First connection: handshake succeeds, then the call crashes.
				return newFakeTransport(
					fakeStep{body: []byte(reconnectInitResp)},
					fakeStep{body: []byte(reconnectListResp)},
					fakeStep{err: transport.ErrTransportClosed},
				)
			}
			// Reconnect: fresh handshake, then the call succeeds.
			return newFakeTransport(
				fakeStep{body: []byte(reconnectInitResp)},
				fakeStep{body: []byte(reconnectListResp)},
				fakeStep{body: []byte(reconnectCallResp)},
			)
		},
	)

	spec := ServerSpec{ID: "srv", Name: "srv", Enabled: true, Transport: TransportStdio, Command: "node", Args: []string{"fake"}}
	if err := reg.Register(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	// Register connects asynchronously; wait for the first handshake.
	waitConnected(t, reg, "srv")

	res, err := reg.CallTool(context.Background(), "srv", "read_file", map[string]any{})
	if err != nil {
		t.Fatalf("CallTool after crash = %v, want transparent recovery", err)
	}
	if len(res.Content) != 1 || res.Content[0].Text != "ok" {
		t.Fatalf("content = %+v, want ok", res.Content)
	}
	if builds < 2 {
		t.Fatalf("transportBuilder calls = %d, want >= 2 (initial + reconnect)", builds)
	}
}

// TestRegistry_CallToolNoRecoveryWhenRPCError: an RPC-level error (not a
// transport break) must not trigger a reconnect loop.
func TestRegistry_CallToolNoRecoveryWhenRPCError(t *testing.T) {
	builds := 0
	reg := NewRegistry(noopResolverManager(t), NewInMemoryResolutionPersistence()).WithTransportBuilder(
		func(ServerSpec, LaunchResolution) transport.Transport {
			builds++
			return newFakeTransport(
				fakeStep{body: []byte(reconnectInitResp)},
				fakeStep{body: []byte(reconnectListResp)},
				fakeStep{body: []byte(`{"jsonrpc":"2.0","id":3,"error":{"code":-32603,"message":"boom"}}`)},
			)
		},
	)
	spec := ServerSpec{ID: "srv", Name: "srv", Enabled: true, Transport: TransportStdio, Command: "node"}
	if err := reg.Register(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	waitConnected(t, reg, "srv")

	if _, err := reg.CallTool(context.Background(), "srv", "read_file", nil); err == nil {
		t.Fatal("CallTool = nil err, want RPC error")
	}
	if builds != 1 {
		t.Fatalf("transportBuilder calls = %d, want 1 (RPC error must not reconnect)", builds)
	}
}

func waitConnected(t *testing.T, reg *Registry, id string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st, _ := reg.Get(id)
		if st.Connected {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("server %s did not connect in time", id)
}
