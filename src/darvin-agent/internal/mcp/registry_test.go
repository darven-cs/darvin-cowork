// Tests for the MCP server registry.

package mcp

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// noopResolverManager returns a manager whose executor always reports
// "npm view: network down". With the npm resolver in failed-state, the
// registry falls back to the raw spec command, which makes tests
// deterministic without a real npm on PATH.
func noopResolverManager(t *testing.T) *ResolverManager {
	t.Helper()
	rm := NewResolverManager(t.TempDir()).withExecutor(func(_ context.Context, _ string, _ ...string) ([]byte, []byte, error) {
		return nil, nil, errors.New("network down")
	})
	return rm
}

func TestRegistry_RegisterAndList(t *testing.T) {
	reg := NewRegistry(noopResolverManager(t), NewInMemoryResolutionPersistence())
	spec := ServerSpec{ID: "fs", Name: "filesystem", Enabled: true, Transport: TransportStdio, Command: "node", Args: []string{"server.js"}}
	if err := reg.Register(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	// Allow the async connect to settle; since the resolver is
	// stubbed to fail, the client never reaches Connected=true, but
	// the entry must be present in List().
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(reg.List()) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	list := reg.List()
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1", len(list))
	}
	if list[0].ServerID != "fs" {
		t.Fatalf("server id = %s, want fs", list[0].ServerID)
	}
}

func TestRegistry_GetAndGetToolsNotConnected(t *testing.T) {
	reg := NewRegistry(noopResolverManager(t), NewInMemoryResolutionPersistence())
	_ = reg.Register(context.Background(), ServerSpec{ID: "fs", Enabled: true, Transport: TransportStdio, Command: "node"})
	// The resolver fails so connected stays false; GetTools must
	// return nil in that case.
	if got := reg.GetTools("fs"); got != nil {
		t.Fatalf("GetTools = %v, want nil", got)
	}
	if _, ok := reg.Get("fs"); !ok {
		t.Fatal("Get should find fs")
	}
	if _, ok := reg.Get("nope"); ok {
		t.Fatal("Get should not find nope")
	}
}

func TestRegistry_SetEnabledDisableClosesClient(t *testing.T) {
	// No-op resolver, no real client — disable should still clear the
	// in-memory tools list (which is nil here, but the API contract is
	// what we care about).
	reg := NewRegistry(noopResolverManager(t), NewInMemoryResolutionPersistence())
	_ = reg.Register(context.Background(), ServerSpec{ID: "fs", Enabled: true, Transport: TransportStdio, Command: "node"})
	if err := reg.SetEnabled(context.Background(), "fs", false); err != nil {
		t.Fatal(err)
	}
	s, ok := reg.Get("fs")
	if !ok {
		t.Fatal("entry missing after disable")
	}
	if s.Enabled {
		t.Fatal("status still enabled")
	}
	if s.Connected {
		t.Fatal("status still connected")
	}
}

func TestRegistry_UnregisterRemovesEntry(t *testing.T) {
	reg := NewRegistry(noopResolverManager(t), NewInMemoryResolutionPersistence())
	_ = reg.Register(context.Background(), ServerSpec{ID: "fs", Enabled: true, Transport: TransportStdio, Command: "node"})
	if err := reg.Unregister(context.Background(), "fs"); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("fs"); ok {
		t.Fatal("entry should be gone")
	}
}

func TestRegistry_SetEnabledUnknownServer(t *testing.T) {
	reg := NewRegistry(noopResolverManager(t), NewInMemoryResolutionPersistence())
	err := reg.SetEnabled(context.Background(), "nope", true)
	if err == nil {
		t.Fatal("expected error for unknown server")
	}
}

func TestRegistry_FingerprintChangesInvalidateCache(t *testing.T) {
	// Two Registers with different args → different fingerprints → the
	// second Register replaces the entry's fingerprint.
	reg := NewRegistry(noopResolverManager(t), NewInMemoryResolutionPersistence())
	spec := ServerSpec{ID: "fs", Enabled: true, Transport: TransportStdio, Command: "node", Args: []string{"a"}}
	_ = reg.Register(context.Background(), spec)
	reg.mu.RLock()
	fp1 := reg.servers["fs"].fingerprint
	reg.mu.RUnlock()

	spec.Args = []string{"b"}
	_ = reg.Register(context.Background(), spec)
	reg.mu.RLock()
	fp2 := reg.servers["fs"].fingerprint
	reg.mu.RUnlock()

	if fp1 == fp2 {
		t.Fatal("fingerprint did not change after args edit")
	}
}

func TestRegistry_GetToolsByName(t *testing.T) {
	// Inject a fake status with tools on an entry directly so we can
	// exercise the cross-server lookup without standing up a real
	// transport.
	reg := NewRegistry(noopResolverManager(t), NewInMemoryResolutionPersistence())
	reg.mu.Lock()
	reg.servers["a"] = &serverEntry{
		spec:   ServerSpec{ID: "a"},
		status: ServerStatus{ServerID: "a", Connected: true, Tools: []ToolDescriptor{{Name: "read_file"}}},
	}
	reg.servers["b"] = &serverEntry{
		spec:   ServerSpec{ID: "b"},
		status: ServerStatus{ServerID: "b", Connected: true, Tools: []ToolDescriptor{{Name: "write_file"}, {Name: "list_dir"}}},
	}
	reg.mu.Unlock()

	id, tool, ok := reg.GetToolsByName("write_file")
	if !ok || id != "b" || tool == nil || tool.Name != "write_file" {
		t.Fatalf("got id=%s tool=%+v ok=%v", id, tool, ok)
	}
	if _, _, ok := reg.GetToolsByName("missing"); ok {
		t.Fatal("missing tool should not be found")
	}
}

func TestRegistry_ConcurrentRegisterAndGet(t *testing.T) {
	// Smoke-test the mutex: hammer Register + GetToolsByName from
	// multiple goroutines, confirm no panic and consistent reads.
	reg := NewRegistry(noopResolverManager(t), NewInMemoryResolutionPersistence())
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			_ = reg.Register(context.Background(), ServerSpec{
				ID: "server", Enabled: true, Transport: TransportStdio, Command: "node",
			})
		}(i)
		go func() {
			defer wg.Done()
			_ = reg.List()
		}()
	}
	wg.Wait()
	if _, ok := reg.Get("server"); !ok {
		t.Fatal("entry missing after concurrent registers")
	}
}

func TestRegistry_LoadStaleResolutionsRetries(t *testing.T) {
	// Seed persistence with an "installing" record that is older than
	// the 30-minute grace. LoadStaleResolutions should trigger a
	// resolver pass — verifiable by the executor running once.
	persistence := NewInMemoryResolutionPersistence()
	oldTime := time.Now().Add(-45 * time.Minute)
	_ = persistence.SaveResolution(context.Background(), LaunchResolution{
		ServerID:  "fs",
		Status:    StatusInstalling,
		UpdatedAt: oldTime,
	})

	var ran atomic.Int32
	rm := NewResolverManager(t.TempDir()).withExecutor(func(_ context.Context, _ string, _ ...string) ([]byte, []byte, error) {
		ran.Add(1)
		return nil, nil, errors.New("network down")
	})
	reg := NewRegistry(rm, persistence)
	_ = reg.Register(context.Background(), ServerSpec{
		ID: "fs", Enabled: true, Transport: TransportStdio, Command: "npx", Args: []string{"-y", "pkg"},
	})

	if err := reg.LoadStaleResolutions(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Resolve is async; wait until the executor runs.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) && ran.Load() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if ran.Load() == 0 {
		t.Fatal("stale installing was not retried")
	}
}
func TestRegistry_CallTool_Errors(t *testing.T) {
	reg := NewRegistry(noopResolverManager(t), NewInMemoryResolutionPersistence())
	if _, err := reg.CallTool(context.Background(), "nope", "read_file", nil); err == nil {
		t.Error("CallTool unknown server: want error")
	}
	// Registered but never connected (no client attached).
	reg.servers["fs"] = &serverEntry{
		spec:   ServerSpec{ID: "fs", Enabled: true},
		status: ServerStatus{ServerID: "fs", Enabled: true},
	}
	if _, err := reg.CallTool(context.Background(), "fs", "read_file", nil); err == nil {
		t.Error("CallTool not-connected server: want error")
	}
}
