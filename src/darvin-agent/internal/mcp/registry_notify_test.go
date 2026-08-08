// Tests for registry connection and resolution change notifications.

package mcp

import (
	"context"
	"sync"
	"testing"
	"time"
)

func nowMs() int64 { return time.Now().UnixMilli() }
func sleep(ms int) { time.Sleep(time.Duration(ms) * time.Millisecond) }

// recordingNotifier collects callback invocations so tests can assert
// on the order / payload without spinning a full ledger.
type recordingNotifier struct {
	mu    sync.Mutex
	conns []connEvent
	res   []LaunchResolution
}

type connEvent struct {
	serverID string
	status   ConnectionStatus
	errMsg   string
}

func (r *recordingNotifier) OnConnectionChanged(serverID string, status ConnectionStatus, errMsg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.conns = append(r.conns, connEvent{serverID, status, errMsg})
}

func (r *recordingNotifier) OnResolutionChanged(serverID string, res LaunchResolution) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.res = append(r.res, res)
}

func (r *recordingNotifier) ConnEvents() []connEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]connEvent, len(r.conns))
	copy(out, r.conns)
	return out
}

func (r *recordingNotifier) Resolutions() []LaunchResolution {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]LaunchResolution, len(r.res))
	copy(out, r.res)
	return out
}

func TestRegistry_SetNotifierNilFields(t *testing.T) {
	reg := NewRegistry(noopResolverManager(t), NewInMemoryResolutionPersistence())
	reg.SetNotifier(Notifier{}) // both fields nil → defaults applied
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	if reg.notifier.OnConnectionChanged == nil || reg.notifier.OnResolutionChanged == nil {
		t.Fatal("nil callbacks should default to no-op")
	}
}

func TestRegistry_GetSpec(t *testing.T) {
	reg := NewRegistry(noopResolverManager(t), NewInMemoryResolutionPersistence())
	_ = reg.Register(context.Background(), ServerSpec{
		ID: "fs", Name: "f", Description: "d", Enabled: true, IsBuiltIn: true,
		Transport: TransportStdio, Command: "npx", Args: []string{"-y", "pkg"},
	})
	spec, ok := reg.GetSpec("fs")
	if !ok {
		t.Fatal("GetSpec should find registered server")
	}
	if spec.Name != "f" {
		t.Fatalf("name = %q, want f", spec.Name)
	}
	if _, ok := reg.GetSpec("nope"); ok {
		t.Fatal("GetSpec should not find unknown server")
	}
}

func TestRegistry_TestReturns(t *testing.T) {
	reg := NewRegistry(noopResolverManager(t), NewInMemoryResolutionPersistence())
	// unknown
	if ok, msg, _ := reg.Test("nope"); ok || msg == "" {
		t.Fatalf("Test(unknown) = %v, %q", ok, msg)
	}
	// disabled
	_ = reg.Register(context.Background(), ServerSpec{ID: "fs", Enabled: true, Transport: TransportStdio})
	_ = reg.SetEnabled(context.Background(), "fs", false)
	if ok, _, _ := reg.Test("fs"); ok {
		t.Fatal("Test(disabled) should not be ok")
	}
	// re-enable so we can drive the connected / error paths
	if err := reg.SetEnabled(context.Background(), "fs", true); err != nil {
		t.Fatal(err)
	}
	// connected + tools (inject directly to avoid async connect race)
	reg.mu.Lock()
	reg.servers["fs"] = &serverEntry{
		spec:   ServerSpec{ID: "fs", Enabled: true},
		status: ServerStatus{ServerID: "fs", Enabled: true, Connected: true, Tools: []ToolDescriptor{{Name: "read"}}},
	}
	reg.mu.Unlock()
	ok, msg, tools := reg.Test("fs")
	if !ok || msg != "" || len(tools) != 1 || tools[0].Name != "read" {
		t.Fatalf("Test(connected) = %v, %q, %v", ok, msg, tools)
	}
	// not connected with error
	reg.mu.Lock()
	reg.servers["fs"].status.Connected = false
	reg.servers["fs"].status.ConnectionError = "boom"
	reg.servers["fs"].status.Tools = nil
	reg.mu.Unlock()
	ok, msg, tools = reg.Test("fs")
	if ok || msg != "boom" || len(tools) != 0 {
		t.Fatalf("Test(error) = %v, %q, %v", ok, msg, tools)
	}
}

func TestRegistry_RetryResolutionErrors(t *testing.T) {
	reg := NewRegistry(noopResolverManager(t), NewInMemoryResolutionPersistence())
	if err := reg.RetryResolution("nope"); err == nil {
		t.Fatal("expected error for unknown server")
	}
	_ = reg.Register(context.Background(), ServerSpec{ID: "fs", Enabled: false, Transport: TransportStdio})
	if err := reg.RetryResolution("fs"); err == nil {
		t.Fatal("expected error for disabled server")
	}
}

func TestRegistry_UpdateReplaces(t *testing.T) {
	reg := NewRegistry(noopResolverManager(t), NewInMemoryResolutionPersistence())
	_ = reg.Register(context.Background(), ServerSpec{
		ID: "fs", Name: "old", Enabled: true, Transport: TransportStdio, Command: "npx",
	})
	// Disable first so connectServer doesn't kick off — Update with disabled
	// still replaces the spec, no client churn.
	_ = reg.SetEnabled(context.Background(), "fs", false)
	if err := reg.Update(context.Background(), "fs", ServerSpec{
		Name: "new", Enabled: false, Transport: TransportStdio, Command: "node",
	}); err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.GetSpec("fs")
	if spec.Name != "new" {
		t.Fatalf("name = %q, want new", spec.Name)
	}
	if spec.Command != "node" {
		t.Fatalf("command = %q, want node", spec.Command)
	}
}

func TestRegistry_UpdateUnknown(t *testing.T) {
	reg := NewRegistry(noopResolverManager(t), NewInMemoryResolutionPersistence())
	if err := reg.Update(context.Background(), "nope", ServerSpec{}); err == nil {
		t.Fatal("expected error for unknown server")
	}
}

func TestRegistry_UnregisterFiresDisconnected(t *testing.T) {
	notif := &recordingNotifier{}
	reg := NewRegistry(noopResolverManager(t), NewInMemoryResolutionPersistence())
	reg.SetNotifier(Notifier{
		OnConnectionChanged: notif.OnConnectionChanged,
		OnResolutionChanged: notif.OnResolutionChanged,
	})
	// Enabled:false keeps the async connectServer goroutine from firing a
	// Connecting event that would race this exact-count assertion.
	_ = reg.Register(context.Background(), ServerSpec{ID: "fs", Enabled: false, Transport: TransportStdio, Command: "node"})
	if err := reg.Unregister(context.Background(), "fs"); err != nil {
		t.Fatal(err)
	}
	ev := notif.ConnEvents()
	if len(ev) != 1 || ev[0].status != ConnectionDisconnected {
		t.Fatalf("conn events = %+v, want one disconnected", ev)
	}
}

func TestRegistry_SetEnabledFiresDisconnected(t *testing.T) {
	notif := &recordingNotifier{}
	reg := NewRegistry(noopResolverManager(t), NewInMemoryResolutionPersistence())
	reg.SetNotifier(Notifier{
		OnConnectionChanged: notif.OnConnectionChanged,
		OnResolutionChanged: notif.OnResolutionChanged,
	})
	// Enabled:false keeps the async connectServer goroutine from firing a
	// Connecting event that would race this exact-count assertion.
	_ = reg.Register(context.Background(), ServerSpec{ID: "fs", Enabled: false, Transport: TransportStdio, Command: "node"})
	if err := reg.SetEnabled(context.Background(), "fs", false); err != nil {
		t.Fatal(err)
	}
	ev := notif.ConnEvents()
	if len(ev) != 1 || ev[0].status != ConnectionDisconnected {
		t.Fatalf("conn events = %+v, want one disconnected", ev)
	}
}

func TestRegistry_SetEnabledUnknownErrors(t *testing.T) {
	reg := NewRegistry(noopResolverManager(t), NewInMemoryResolutionPersistence())
	if err := reg.SetEnabled(context.Background(), "nope", true); err == nil {
		t.Fatal("expected error for unknown server")
	}
}

func TestRegistry_ConnectServerFiresConnectingThenError(t *testing.T) {
	notif := &recordingNotifier{}
	reg := NewRegistry(noopResolverManager(t), NewInMemoryResolutionPersistence())
	reg.SetNotifier(Notifier{
		OnConnectionChanged: notif.OnConnectionChanged,
		OnResolutionChanged: notif.OnResolutionChanged,
	})
	// Command "node" routes to stubResolver (not npx) → status unsupported
	// → connectServer fallback to raw spec.Command, which then fails
	// because /bin/true doesn't speak MCP. The point of this test is
	// that connecting → error notifications fire in order.
	_ = reg.Register(context.Background(), ServerSpec{
		ID: "fs", Enabled: true, Transport: TransportStdio, Command: "/no/such/binary",
	})
	// Wait up to 2s for at least 2 events (connecting + terminal).
	deadline := nowMs() + 2000
	for nowMs() < deadline {
		if len(notif.ConnEvents()) >= 2 {
			break
		}
		sleep(20)
	}
	ev := notif.ConnEvents()
	if len(ev) < 2 {
		t.Fatalf("expected >=2 events, got %d: %+v", len(ev), ev)
	}
	if ev[0].status != ConnectionConnecting {
		t.Fatalf("first event = %v, want connecting", ev[0].status)
	}
	last := ev[len(ev)-1]
	if last.status != ConnectionError {
		t.Fatalf("last event = %v, want error (msg=%q)", last.status, last.errMsg)
	}
}
