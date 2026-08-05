package harness

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestRegisterAndGet(t *testing.T) {
	resetGlobals(t)

	h := newStub("alpha")
	if err := Register(h, "plugin-a"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, ok := Get("alpha")
	if !ok || got != Harness(h) {
		t.Fatalf("Get(alpha) = %v, %v", got, ok)
	}
	reg, ok := Lookup("alpha")
	if !ok {
		t.Fatal("Lookup(alpha) missing")
	}
	if reg.OwnerPluginID != "plugin-a" {
		t.Fatalf("OwnerPluginID = %q", reg.OwnerPluginID)
	}
	if reg.RegisteredAt.IsZero() {
		t.Fatal("RegisteredAt not stamped")
	}
}

func TestRegisterRejectsBlankAndNil(t *testing.T) {
	resetGlobals(t)

	if err := Register(newStub("   "), ""); !errors.Is(err, ErrIDRequired) {
		t.Fatalf("blank id err = %v, want ErrIDRequired", err)
	}
	if err := Register(nil, ""); !errors.Is(err, ErrHarnessRequired) {
		t.Fatalf("nil harness err = %v, want ErrHarnessRequired", err)
	}
	if len(List()) != 0 {
		t.Fatalf("rejected harnesses were registered: %d entries", len(List()))
	}
}

func TestRegisterRejectsUndeclaredCapability(t *testing.T) {
	resetGlobals(t)

	h := newStub("alpha")
	h.caps.Compact = true

	err := Register(h, "")
	if err == nil {
		t.Fatal("Register accepted a harness declaring an unimplemented capability")
	}
	if _, ok := Get("alpha"); ok {
		t.Fatal("rejected harness reached the registry")
	}
}

func TestRegisterReplacesSameID(t *testing.T) {
	resetGlobals(t)

	first, second := newStub("alpha"), newStub("alpha")
	if err := Register(first, ""); err != nil {
		t.Fatalf("Register first: %v", err)
	}
	if err := Register(second, "plugin-b"); err != nil {
		t.Fatalf("Register second: %v", err)
	}

	if len(List()) != 1 {
		t.Fatalf("entries = %d, want 1", len(List()))
	}
	got, _ := Get("alpha")
	if got != Harness(second) {
		t.Fatal("re-registration did not replace the entry")
	}
}

func TestUnregister(t *testing.T) {
	resetGlobals(t)

	if err := Register(newStub("alpha"), ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	Unregister("alpha")
	if _, ok := Get("alpha"); ok {
		t.Fatal("harness still reachable after Unregister")
	}
	Unregister("does-not-exist")
}

func TestGetEmptyIDPicksLowestIDHealthy(t *testing.T) {
	resetGlobals(t)

	for _, id := range []string{"zzz", "aaa", "mmm"} {
		mustRegister(t, newStub(id))
	}

	got, ok := Get("")
	if !ok {
		t.Fatal("Get(\"\") found nothing")
	}
	if got.ID() != "aaa" {
		t.Fatalf("Get(\"\") = %q, want the lowest id aaa", got.ID())
	}
}

func TestGetEmptyIDIgnoresSupportsPriority(t *testing.T) {
	resetGlobals(t)

	// Priority belongs to Rank, not to the diagnostic empty-id lookup.
	high := newStub("zzz")
	high.supports = func(SupportContext) SupportResult {
		return SupportResult{Supported: true, Priority: 100}
	}
	mustRegister(t, newStub("aaa"))
	mustRegister(t, high)

	got, _ := Get("")
	if got.ID() != "aaa" {
		t.Fatalf("Get(\"\") = %q, want aaa; priority must not affect it", got.ID())
	}
	if best := Rank(SupportContext{}); best[0].Harness.ID() != "zzz" {
		t.Fatalf("Rank first = %q, want zzz", best[0].Harness.ID())
	}
}

func TestGetEmptyIDSkipsUnhealthy(t *testing.T) {
	resetGlobals(t)

	sick := newStub("aaa")
	sick.caps.Healthy = false
	if err := Register(sick, ""); err != nil {
		t.Fatalf("Register sick: %v", err)
	}
	if err := Register(newStub("bbb"), ""); err != nil {
		t.Fatalf("Register healthy: %v", err)
	}

	got, ok := Get("")
	if !ok || got.ID() != "bbb" {
		t.Fatalf("Get(\"\") = %v, %v; want bbb", got, ok)
	}
}

func TestGetUnknownID(t *testing.T) {
	resetGlobals(t)

	if _, ok := Get("nope"); ok {
		t.Fatal("Get returned an unregistered harness")
	}
	if _, ok := Get(""); ok {
		t.Fatal("Get on an empty registry returned a harness")
	}
}

func TestListOrdersByIDOnly(t *testing.T) {
	resetGlobals(t)

	// A high Supports priority must not reorder List; that is Rank's job.
	loud := newStub("delta")
	loud.supports = func(SupportContext) SupportResult {
		return SupportResult{Supported: true, Priority: 999}
	}
	mustRegister(t, loud)
	for _, id := range []string{"alpha", "charlie", "bravo"} {
		mustRegister(t, newStub(id))
	}

	want := []string{"alpha", "bravo", "charlie", "delta"}
	for i := 0; i < 20; i++ {
		got := List()
		if len(got) != len(want) {
			t.Fatalf("entries = %d, want %d", len(got), len(want))
		}
		for j, reg := range got {
			if reg.Harness.ID() != want[j] {
				t.Fatalf("List()[%d] = %q, want %q", j, reg.Harness.ID(), want[j])
			}
		}
	}
}

func TestRegisterRejectsPluginIDMismatch(t *testing.T) {
	resetGlobals(t)

	h := newStub("alpha")
	h.pluginID = "acpx"

	err := Register(h, "other-plugin")
	if !errors.Is(err, ErrPluginIDMismatch) {
		t.Fatalf("err = %v, want ErrPluginIDMismatch", err)
	}
	if _, ok := Get("alpha"); ok {
		t.Fatal("rejected harness reached the registry")
	}

	if err := Register(h, "acpx"); err != nil {
		t.Fatalf("matching owner rejected: %v", err)
	}
	reg, _ := Lookup("alpha")
	if reg.OwnerPluginID != "acpx" {
		t.Fatalf("OwnerPluginID = %q, want acpx", reg.OwnerPluginID)
	}
}

func TestRegisterAdoptsHarnessPluginIDWhenOwnerBlank(t *testing.T) {
	resetGlobals(t)

	h := newStub("alpha")
	h.pluginID = "acpx"
	if err := Register(h, ""); err != nil {
		t.Fatalf("Register: %v", err)
	}

	reg, _ := Lookup("alpha")
	if reg.OwnerPluginID != "acpx" {
		t.Fatalf("OwnerPluginID = %q, want the harness-reported acpx", reg.OwnerPluginID)
	}
}

func TestResetAllContinuesAfterFailure(t *testing.T) {
	resetGlobals(t)

	first, middle, last := newStub("aaa"), newStub("bbb"), newStub("ccc")
	middle.resetErr = errors.New("middle exploded")
	for _, h := range []*stubHarness{first, middle, last} {
		mustRegister(t, h)
	}

	err := ResetAll(context.Background(), ResetParams{SessionID: "s1"})
	if !errors.Is(err, middle.resetErr) {
		t.Fatalf("err = %v, want the middle harness error", err)
	}
	for _, h := range []*stubHarness{first, middle, last} {
		if h.resets != 1 {
			t.Fatalf("harness %q reset %d times, want 1", h.id, h.resets)
		}
	}
}

func TestDisposeAllJoinsErrors(t *testing.T) {
	resetGlobals(t)

	first, second, clean := newStub("aaa"), newStub("bbb"), newStub("ccc")
	first.disposeErr = errors.New("first exploded")
	second.disposeErr = errors.New("second exploded")
	for _, h := range []*stubHarness{first, second, clean} {
		mustRegister(t, h)
	}

	err := DisposeAll(context.Background())
	if !errors.Is(err, first.disposeErr) || !errors.Is(err, second.disposeErr) {
		t.Fatalf("err = %v, want both failures retrievable", err)
	}
	if clean.disposes != 1 {
		t.Fatalf("clean harness disposed %d times, want 1", clean.disposes)
	}
}

func TestResetAllAndDisposeAllSucceedWhenEmpty(t *testing.T) {
	resetGlobals(t)

	if err := ResetAll(context.Background(), ResetParams{}); err != nil {
		t.Fatalf("ResetAll on empty registry: %v", err)
	}
	if err := DisposeAll(context.Background()); err != nil {
		t.Fatalf("DisposeAll on empty registry: %v", err)
	}
}

func TestMustRegisterPanicsOnInvalid(t *testing.T) {
	resetGlobals(t)

	defer func() {
		if recover() == nil {
			t.Fatal("MustRegister did not panic on a blank id")
		}
	}()
	MustRegister(newStub(""), "")
}

func TestRegistryConcurrentAccess(t *testing.T) {
	resetGlobals(t)

	const workers = 16
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			id := string(rune('a' + i%4))
			_ = Register(newStub(id), "")
			_, _ = Get(id)
			_ = List()
			if i%3 == 0 {
				Unregister(id)
			}
		}(i)
	}
	wg.Wait()
}
