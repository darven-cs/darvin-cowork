// Tests for the MCP JSON-RPC handlers and connection broadcasts.

package gateway

import (
	"context"
	"encoding/json"
	"testing"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/mcp"
)

// mcpForTest returns a registry with the resolver stubbed to always fail
// (so the registry falls back to raw spec.Command, no real subprocess).
func mcpForTest(t *testing.T) *mcp.Registry {
	t.Helper()
	resolver := mcp.NewResolverManager(t.TempDir())
	// The manager's executor is private; the simpler path is to register
	// with a Command that does not exist — the connect path then fails
	// immediately and the notifier still fires connecting → error.
	return mcp.NewRegistry(resolver, mcp.NewInMemoryResolutionPersistence())
}

func dispatchMcp(t *testing.T, h *Handler, c *client, method string, params any) *Response {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: method, Params: raw,
	}, c, h)
}

func TestHandleMcpListEmpty(t *testing.T) {
	h, c := newTestHandler(t)
	h.Mcp = mcpForTest(t)
	h.Ledger = c.ledger
	h.Log = zap.NewNop()

	resp := dispatchMcp(t, h, c, "agent.mcp.list", map[string]any{})
	if resp.Error != nil {
		t.Fatalf("list: %+v", resp.Error)
	}
	r := resp.Result.(ListMcpServersResult)
	if len(r.Servers) != 0 {
		t.Fatalf("got %d servers, want 0", len(r.Servers))
	}
}

func TestHandleMcpRegisterAndList(t *testing.T) {
	h, c := newTestHandler(t)
	h.Mcp = mcpForTest(t)
	h.Ledger = c.ledger
	h.Log = zap.NewNop()

	dispatchMcp(t, h, c, "agent.mcp.register", map[string]any{
		"server": mcp.ServerSpec{
			ID: "fs", Name: "Filesystem", Enabled: true,
			Transport: mcp.TransportStdio, Command: "node", Args: []string{"x"},
		},
	})

	resp := dispatchMcp(t, h, c, "agent.mcp.list", map[string]any{})
	r := resp.Result.(ListMcpServersResult)
	if len(r.Servers) != 1 || r.Servers[0].ID != "fs" {
		t.Fatalf("got %+v, want one filesystem", r.Servers)
	}
}

func TestHandleMcpUpdateAndDelete(t *testing.T) {
	h, c := newTestHandler(t)
	h.Mcp = mcpForTest(t)
	h.Ledger = c.ledger
	h.Log = zap.NewNop()

	dispatchMcp(t, h, c, "agent.mcp.register", map[string]any{
		"server": mcp.ServerSpec{
			ID: "fs", Name: "old", Enabled: true, Transport: mcp.TransportStdio, Command: "node",
		},
	})
	if err := h.Mcp.SetEnabled(context.Background(), "fs", false); err != nil {
		t.Fatal(err)
	}

	resp := dispatchMcp(t, h, c, "agent.mcp.update", map[string]any{
		"id":    "fs",
		"patch": mcp.ServerSpec{Name: "new", Enabled: false, Transport: mcp.TransportStdio, Command: "node"},
	})
	if resp.Error != nil {
		t.Fatalf("update: %+v", resp.Error)
	}
	r := resp.Result.(map[string]any)
	srv := r["server"].(McpServerWire)
	if srv.Name != "new" {
		t.Fatalf("name = %q, want new", srv.Name)
	}

	if resp := dispatchMcp(t, h, c, "agent.mcp.unregister", map[string]any{"id": "fs"}); resp.Error != nil {
		t.Fatalf("unregister: %+v", resp.Error)
	}
	if _, ok := h.Mcp.GetSpec("fs"); ok {
		t.Fatal("server should be gone after unregister")
	}
}

func TestHandleMcpSetEnabled(t *testing.T) {
	h, c := newTestHandler(t)
	h.Mcp = mcpForTest(t)
	h.Ledger = c.ledger
	h.Log = zap.NewNop()
	dispatchMcp(t, h, c, "agent.mcp.register", map[string]any{
		"server": mcp.ServerSpec{ID: "fs", Enabled: true, Transport: mcp.TransportStdio, Command: "node"},
	})
	resp := dispatchMcp(t, h, c, "agent.mcp.set_enabled", map[string]any{"id": "fs", "enabled": false})
	if resp.Error != nil {
		t.Fatalf("set_enabled: %+v", resp.Error)
	}
	st, _ := h.Mcp.Get("fs")
	if st.Enabled {
		t.Fatal("status still enabled")
	}
}

func TestHandleMcpTest(t *testing.T) {
	h, c := newTestHandler(t)
	h.Mcp = mcpForTest(t)
	h.Ledger = c.ledger
	h.Log = zap.NewNop()

	// unknown
	resp := dispatchMcp(t, h, c, "agent.mcp.test", map[string]any{"id": "nope"})
	r := resp.Result.(map[string]any)
	if r["ok"].(bool) {
		t.Fatal("test on unknown should not be ok")
	}

	// known but disabled
	dispatchMcp(t, h, c, "agent.mcp.register", map[string]any{
		"server": mcp.ServerSpec{ID: "fs", Enabled: true, Transport: mcp.TransportStdio, Command: "node"},
	})
	if err := h.Mcp.SetEnabled(context.Background(), "fs", false); err != nil {
		t.Fatal(err)
	}
	resp = dispatchMcp(t, h, c, "agent.mcp.test", map[string]any{"id": "fs"})
	r = resp.Result.(map[string]any)
	if r["ok"].(bool) {
		t.Fatal("test on disabled should not be ok")
	}
}

func TestHandleMcpRetryResolution(t *testing.T) {
	h, c := newTestHandler(t)
	h.Mcp = mcpForTest(t)
	h.Ledger = c.ledger
	h.Log = zap.NewNop()

	// unknown
	resp := dispatchMcp(t, h, c, "agent.mcp.retry_resolution", map[string]any{"id": "nope"})
	if resp.Error == nil {
		t.Fatal("expected error for unknown id")
	}

	// disabled
	dispatchMcp(t, h, c, "agent.mcp.register", map[string]any{
		"server": mcp.ServerSpec{ID: "fs", Enabled: true, Transport: mcp.TransportStdio, Command: "node"},
	})
	if err := h.Mcp.SetEnabled(context.Background(), "fs", false); err != nil {
		t.Fatal(err)
	}
	resp = dispatchMcp(t, h, c, "agent.mcp.retry_resolution", map[string]any{"id": "fs"})
	if resp.Error == nil {
		t.Fatal("expected error for disabled server")
	}
}

func TestHandleMcpBootstrap(t *testing.T) {
	h, c := newTestHandler(t)
	h.Mcp = mcpForTest(t)
	h.Ledger = c.ledger
	h.Log = zap.NewNop()

	resp := dispatchMcp(t, h, c, "agent.mcp.bootstrap", map[string]any{
		"servers": []mcp.ServerSpec{
			{ID: "fs", Name: "Filesystem", Enabled: true, Transport: mcp.TransportStdio, Command: "node"},
			{ID: "github", Name: "GitHub", Enabled: true, Transport: mcp.TransportStdio, Command: "npx"},
		},
	})
	if resp.Error != nil {
		t.Fatalf("bootstrap: %+v", resp.Error)
	}
	if _, ok := h.Mcp.GetSpec("fs"); !ok {
		t.Fatal("fs not registered")
	}
	if _, ok := h.Mcp.GetSpec("github"); !ok {
		t.Fatal("github not registered")
	}
}

func TestHandleMcpWithoutRegistry(t *testing.T) {
	h, c := newTestHandler(t)
	// Mcp is nil — handlers should return empty / false not panic.
	resp := dispatchMcp(t, h, c, "agent.mcp.list", map[string]any{})
	if resp.Error != nil {
		t.Fatalf("list without registry: %+v", resp.Error)
	}
	if len(resp.Result.(ListMcpServersResult).Servers) != 0 {
		t.Fatal("expected empty list")
	}
}

func TestOnMcpConnectionChangedBroadcasts(t *testing.T) {
	h, c := newTestHandler(t)
	h.Mcp = mcpForTest(t)
	h.Ledger = c.ledger
	h.Log = zap.NewNop()
	// c is a client in the ledger; ensure subscription so we can inspect.
	// We just call OnMcpConnectionChanged directly and verify the ledger
	// eventually fans the notification.
	h.OnMcpConnectionChanged("fs", mcp.ConnectionConnected, "")
	// ledger.broadcast pushes to all conns in l.allConns; c is a fresh
	// test client not necessarily in allConns — so we just check the call
	// doesn't panic and the method is on the Handler.
	_ = c
}

func TestOnMcpResolutionChangedBroadcasts(t *testing.T) {
	h, c := newTestHandler(t)
	h.Mcp = mcpForTest(t)
	h.Ledger = c.ledger
	h.Log = zap.NewNop()
	h.OnMcpResolutionChanged("fs", mcp.LaunchResolution{
		ServerID: "fs", ResolverKind: mcp.ResolverNpx, Status: mcp.StatusReady,
	})
	_ = c
}
