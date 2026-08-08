// Tests for the run-lifecycle controller.

package runtime

import (
	"context"
	"sync"
	"testing"
)

var _ = context.Background

func TestTryStartOnce(t *testing.T) {
	c := NewController()
	if !c.TryStart() {
		t.Fatal("first TryStart on Idle controller returned false")
	}
	if c.TryStart() {
		t.Fatal("second TryStart on Running controller returned true")
	}
}

func TestEndResetsToIdleAndFiresCancel(t *testing.T) {
	c := NewController()
	c.TryStart()
	ctx, cancel := context.WithCancel(context.Background())
	c.SetCancel(cancel)

	c.End()

	if c.IsRunning() {
		t.Fatal("controller still Running after End")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("End did not fire the bound cancel")
	}
}

func TestEndOnIdleIsNoOp(t *testing.T) {
	c := NewController()
	c.End() // must not panic
	if c.IsRunning() {
		t.Fatal("controller Running after End on Idle")
	}
}

func TestAbortFiresCancelWithoutChangingState(t *testing.T) {
	c := NewController()
	c.TryStart()
	ctx, cancel := context.WithCancel(context.Background())
	c.SetCancel(cancel)

	c.Abort()

	select {
	case <-ctx.Done():
	default:
		t.Fatal("Abort did not fire the bound cancel")
	}
	if !c.IsRunning() {
		t.Fatal("Abort changed state; only End should")
	}
}

func TestAbortWithoutBindingIsNoOp(t *testing.T) {
	c := NewController()
	c.Abort()
}

func TestSetCancelReplacesBinding(t *testing.T) {
	c := NewController()
	c.TryStart()

	var firstCalls, secondCalls int
	first := func() { firstCalls++ }
	second := func() { secondCalls++ }
	c.SetCancel(first)
	c.SetCancel(second)

	c.Abort()

	if firstCalls != 0 {
		t.Fatalf("first cancel fired %d times, want 0 (it was overwritten)", firstCalls)
	}
	if secondCalls != 1 {
		t.Fatalf("second cancel fired %d times, want 1", secondCalls)
	}
}

func TestConcurrentTryStart(t *testing.T) {
	c := NewController()
	const workers = 32
	var wg sync.WaitGroup
	wins := make(chan struct{}, workers)
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			if c.TryStart() {
				wins <- struct{}{}
			}
		}()
	}
	wg.Wait()
	close(wins)
	if got := len(wins); got != 1 {
		t.Fatalf("TryStart won %d times, want exactly 1", got)
	}
}
