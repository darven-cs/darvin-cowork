package harness

import (
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

func TestGetEmptyIDPicksHighestPriorityHealthy(t *testing.T) {
	resetGlobals(t)

	low := autoStub{newStub("aaa")}
	low.auto = &AutoSelectionHint{Priority: 1}
	high := autoStub{newStub("zzz")}
	high.auto = &AutoSelectionHint{Priority: 9}

	if err := Register(low, ""); err != nil {
		t.Fatalf("Register low: %v", err)
	}
	if err := Register(high, ""); err != nil {
		t.Fatalf("Register high: %v", err)
	}

	got, ok := Get("")
	if !ok {
		t.Fatal("Get(\"\") found nothing")
	}
	if got.ID() != "zzz" {
		t.Fatalf("Get(\"\") = %q, want the higher-priority zzz", got.ID())
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

func TestListOrderIsDeterministic(t *testing.T) {
	resetGlobals(t)

	for _, id := range []string{"delta", "alpha", "charlie", "bravo"} {
		if err := Register(newStub(id), ""); err != nil {
			t.Fatalf("Register %s: %v", id, err)
		}
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
