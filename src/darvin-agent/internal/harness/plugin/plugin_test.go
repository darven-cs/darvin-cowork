package plugin

import (
	"context"
	"errors"
	"sync"
	"testing"

	"darvin-cowork/backend/internal/agents/event"
	"darvin-cowork/backend/internal/harness"
)

type recordingBus struct {
	mu     sync.Mutex
	events []event.Event
}

func (r *recordingBus) Emit(ev event.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recordingBus) snapshot() []event.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]event.Event, len(r.events))
	copy(out, r.events)
	return out
}

func mkPlugin(id, version string, factory HarnessFactory, hooks *Hooks) *Plugin {
	return &Plugin{
		ID:             id,
		Version:        version,
		HarnessFactory: factory,
		Hooks:          hooks,
	}
}

// stubHarness returns a Harness whose ID is whatever the test asks for,
// since the embedded harness always reports EmbeddedID and the spec needs
// plugins to bring their own ids.
type stubHarness struct {
	id       string
	pluginID string
}

func (s *stubHarness) ID() string                         { return s.id }
func (s *stubHarness) Label() string                      { return s.id }
func (s *stubHarness) PluginID() string                   { return s.pluginID }
func (s *stubHarness) Capabilities() harness.Capabilities { return harness.Capabilities{Healthy: true} }
func (s *stubHarness) Supports(harness.SupportContext) harness.SupportResult {
	return harness.SupportResult{Supported: true, Priority: 1}
}
func (s *stubHarness) RunAttempt(context.Context, harness.RunAttemptParams) (*harness.AttemptResult, error) {
	return &harness.AttemptResult{Status: harness.AttemptOK}, nil
}
func (s *stubHarness) Reset(context.Context, harness.ResetParams) error { return nil }
func (s *stubHarness) Dispose(context.Context) error                    { return nil }

func mkHarness(id, pluginID string) harness.Harness {
	return &stubHarness{id: id, pluginID: pluginID}
}

func resetAll(t *testing.T) {
	t.Helper()
	harness.ResetRegistryForTests()
	harness.ResetLifecycleForTests()
	ResetForTests()
}

func TestLoadRegistersAndEmits(t *testing.T) {
	resetAll(t)
	bus := &recordingBus{}
	mgr := NewManager(bus)

	err := mgr.Load(context.Background(), mkPlugin("acpx", "1.0.0",
		func() (harness.Harness, error) { return mkHarness("acpx-h", "acpx"), nil },
		nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	h, ok := harness.Get("acpx-h")
	if !ok {
		t.Fatal("harness not registered after Load")
	}
	if h.PluginID() != "acpx" {
		t.Fatalf("PluginID = %q, want acpx", h.PluginID())
	}

	events := bus.snapshot()
	if len(events) != 1 {
		t.Fatalf("events len = %d, want 1", len(events))
	}
	loaded, ok := events[0].(event.PluginLoadedEvent)
	if !ok {
		t.Fatalf("event = %T, want PluginLoadedEvent", events[0])
	}
	if loaded.PluginID != "acpx" || loaded.Version != "1.0.0" || loaded.HarnessID != "acpx-h" {
		t.Fatalf("event = %+v", loaded)
	}
}

func TestUnloadEmitsAndUnregisters(t *testing.T) {
	resetAll(t)
	bus := &recordingBus{}
	mgr := NewManager(bus)

	var unloadHookCalled int
	if err := mgr.Load(context.Background(), mkPlugin("acpx", "1.0.0",
		func() (harness.Harness, error) { return mkHarness("acpx-h", "acpx"), nil },
		&Hooks{OnUnload: func(context.Context) error {
			unloadHookCalled++
			return nil
		}},
	)); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := mgr.Unload(context.Background(), "acpx"); err != nil {
		t.Fatalf("Unload: %v", err)
	}
	if unloadHookCalled != 1 {
		t.Fatalf("OnUnload called %d times, want 1", unloadHookCalled)
	}
	if _, ok := harness.Get("acpx-h"); ok {
		t.Fatal("harness still registered after Unload")
	}

	events := bus.snapshot()
	if len(events) != 2 {
		t.Fatalf("events len = %d, want 2 (load + unload)", len(events))
	}
	if _, ok := events[1].(event.PluginUnloadedEvent); !ok {
		t.Fatalf("second event = %T, want PluginUnloadedEvent", events[1])
	}
}

func TestLoadFactoryError(t *testing.T) {
	resetAll(t)
	mgr := NewManager(nil)

	err := mgr.Load(context.Background(), mkPlugin("acpx", "1.0.0",
		func() (harness.Harness, error) { return nil, errors.New("factory exploded") },
		nil))
	if err == nil {
		t.Fatal("Load accepted a failing factory")
	}
	if _, ok := harness.Get("acpx-h"); ok {
		t.Fatal("failed Load registered a harness anyway")
	}
	if got := mgr.ListLoaded(); len(got) != 0 {
		t.Fatalf("ListLoaded len = %d, want 0", len(got))
	}
}

func TestLoadDuplicateIDReplaces(t *testing.T) {
	resetAll(t)
	bus := &recordingBus{}
	mgr := NewManager(bus)

	// Both plugins own a harness with the same registry id; the harness's
	// PluginID matches the plugin id, so Register accepts both. The second
	// load replaces the first.
	if err := mgr.Load(context.Background(), mkPlugin("acpx", "1.0.0",
		func() (harness.Harness, error) { return mkHarness("acpx-h", "acpx"), nil }, nil)); err != nil {
		t.Fatalf("Load v1: %v", err)
	}
	if err := mgr.Load(context.Background(), mkPlugin("acpx", "2.0.0",
		func() (harness.Harness, error) { return mkHarness("acpx-h", "acpx"), nil }, nil)); err != nil {
		t.Fatalf("Load v2: %v", err)
	}

	if got := len(mgr.ListLoaded()); got != 1 {
		t.Fatalf("ListLoaded len = %d, want 1", got)
	}
	if _, ok := harness.Get("acpx-h"); !ok {
		t.Fatal("acpx-h not registered after second Load")
	}

	events := bus.snapshot()
	if len(events) != 2 {
		t.Fatalf("events len = %d, want 2 (load + load)", len(events))
	}
}

func TestOnLoadFailureDoesNotRegister(t *testing.T) {
	resetAll(t)
	mgr := NewManager(nil)

	err := mgr.Load(context.Background(), mkPlugin("acpx", "1.0.0",
		func() (harness.Harness, error) { return mkHarness("acpx-h", "acpx"), nil },
		&Hooks{OnLoad: func(context.Context) error { return errors.New("hook exploded") }},
	))
	if err == nil {
		t.Fatal("Load accepted a failing OnLoad")
	}
	if _, ok := harness.Get("acpx-h"); ok {
		t.Fatal("harness registered despite OnLoad failure")
	}
}

func TestListLoadedOrderStable(t *testing.T) {
	resetAll(t)
	mgr := NewManager(nil)

	for _, id := range []string{"zebra", "alpha", "mango", "bravo"} {
		if err := mgr.Load(context.Background(), mkPlugin(id, "1.0.0",
			func() (harness.Harness, error) { return mkHarness(id+"-h", id), nil }, nil)); err != nil {
			t.Fatalf("Load %s: %v", id, err)
		}
	}

	for i := 0; i < 10; i++ {
		got := mgr.ListLoaded()
		want := []string{"alpha", "bravo", "mango", "zebra"}
		if len(got) != len(want) {
			t.Fatalf("iter %d: len = %d, want %d", i, len(got), len(want))
		}
		for j, p := range got {
			if p.ID != want[j] {
				t.Fatalf("iter %d: ListLoaded[%d] = %q, want %q", i, j, p.ID, want[j])
			}
		}
	}
}

func TestGetLoaded(t *testing.T) {
	resetAll(t)
	mgr := NewManager(nil)

	if _, ok := mgr.Get("acpx"); ok {
		t.Fatal("Get on empty manager returned a plugin")
	}
	if err := mgr.Load(context.Background(), mkPlugin("acpx", "1.0.0",
		func() (harness.Harness, error) { return mkHarness("acpx-h", "acpx"), nil }, nil)); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := mgr.Get("acpx"); !ok {
		t.Fatal("Get did not return the loaded plugin")
	}
}

func TestUnloadUnknownIsNoOp(t *testing.T) {
	resetAll(t)
	mgr := NewManager(nil)
	if err := mgr.Unload(context.Background(), "missing"); err != nil {
		t.Fatalf("Unload unknown: %v", err)
	}
}

func TestValidation(t *testing.T) {
	resetAll(t)
	mgr := NewManager(nil)

	cases := []struct {
		name string
		p    *Plugin
	}{
		{"nil", nil},
		{"no id", &Plugin{Version: "1.0", HarnessFactory: func() (harness.Harness, error) { return mkHarness("x", "x"), nil }}},
		{"no version", &Plugin{ID: "x", HarnessFactory: func() (harness.Harness, error) { return mkHarness("x", "x"), nil }}},
		{"no factory", &Plugin{ID: "x", Version: "1.0"}},
	}
	for _, tc := range cases {
		if err := mgr.Load(context.Background(), tc.p); err == nil {
			t.Fatalf("%s: Load accepted %+v", tc.name, tc.p)
		}
	}
}
