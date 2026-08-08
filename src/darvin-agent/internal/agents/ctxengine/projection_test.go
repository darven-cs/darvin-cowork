// Tests for the context projection CRUD registry.

package ctxengine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestProjection_CreateGetDelete exercises the basic CRUD round-trip:
// create, get, delete, get-returns-not-found.
func TestProjection_CreateGetDelete(t *testing.T) {
	a := newTestAssemblerForProj()
	ctx := context.Background()

	if err := a.ProjectionCreate(ctx, ContextProjection{ID: "p1", Type: "agent"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := a.ProjectionGet(ctx, "p1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != "p1" || got.Type != "agent" {
		t.Errorf("got = %+v, want ID=p1 Type=agent", got)
	}
	if got.CreatedAt.IsZero() {
		t.Errorf("CreatedAt should be auto-populated when caller leaves it zero")
	}

	if err := a.ProjectionDelete(ctx, "p1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := a.ProjectionGet(ctx, "p1"); !errors.Is(err, ErrProjectionNotFound) {
		t.Errorf("after delete, err = %v, want ErrProjectionNotFound", err)
	}
}

// TestProjection_Create_RejectsEmptyID verifies the empty-ID guard.
func TestProjection_Create_RejectsEmptyID(t *testing.T) {
	a := newTestAssemblerForProj()
	if err := a.ProjectionCreate(context.Background(), ContextProjection{Type: "agent"}); !errors.Is(err, ErrProjectionIDEmpty) {
		t.Errorf("err = %v, want ErrProjectionIDEmpty", err)
	}
}

// TestProjection_GetNotFound verifies the miss signal for a never-registered id.
func TestProjection_GetNotFound(t *testing.T) {
	a := newTestAssemblerForProj()
	_, err := a.ProjectionGet(context.Background(), "missing")
	if !errors.Is(err, ErrProjectionNotFound) {
		t.Errorf("err = %v, want ErrProjectionNotFound", err)
	}
}

// TestProjection_DeleteIsIdempotent verifies that deleting a missing id is
// not an error (caller can drive cleanup without checking existence first).
func TestProjection_DeleteIsIdempotent(t *testing.T) {
	a := newTestAssemblerForProj()
	if err := a.ProjectionDelete(context.Background(), "nope"); err != nil {
		t.Errorf("delete missing: err = %v, want nil", err)
	}
}

// TestProjection_ListSortedAndExcludesExpired verifies List returns the
// non-expired projections in ID order.
func TestProjection_ListSortedAndExcludesExpired(t *testing.T) {
	a := newTestAssemblerForProj()
	ctx := context.Background()

	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)

	if err := a.ProjectionCreate(ctx, ContextProjection{ID: "b", Type: "tool"}); err != nil {
		t.Fatal(err)
	}
	if err := a.ProjectionCreate(ctx, ContextProjection{ID: "a", Type: "agent"}); err != nil {
		t.Fatal(err)
	}
	if err := a.ProjectionCreate(ctx, ContextProjection{
		ID:        "c",
		Type:      "memory",
		ExpiresAt: &past, // already expired
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.ProjectionCreate(ctx, ContextProjection{
		ID:        "d",
		Type:      "agent",
		ExpiresAt: &future, // not expired
	}); err != nil {
		t.Fatal(err)
	}

	list := a.ProjectionList(ctx)
	if len(list) != 3 {
		t.Fatalf("list len = %d, want 3 (excluding expired c)", len(list))
	}
	want := []string{"a", "b", "d"}
	for i, p := range list {
		if p.ID != want[i] {
			t.Errorf("list[%d].ID = %q, want %q", i, p.ID, want[i])
		}
	}
}

// TestProjection_ExpiredGetReturnsNotFound verifies that an expired entry
// (ExpiresAt in the past) is treated as a miss even though it still
// occupies the map.
func TestProjection_ExpiredGetReturnsNotFound(t *testing.T) {
	a := newTestAssemblerForProj()
	past := time.Now().Add(-time.Minute)
	if err := a.ProjectionCreate(context.Background(), ContextProjection{
		ID:        "exp",
		Type:      "tool",
		ExpiresAt: &past,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := a.ProjectionGet(context.Background(), "exp")
	if !errors.Is(err, ErrProjectionNotFound) {
		t.Errorf("err = %v, want ErrProjectionNotFound (entry expired)", err)
	}
}

// TestProjection_ContextCancelled verifies all 4 methods respect ctx.Err().
func TestProjection_ContextCancelled(t *testing.T) {
	a := newTestAssemblerForProj()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := a.ProjectionCreate(ctx, ContextProjection{ID: "x"}); !errors.Is(err, context.Canceled) {
		t.Errorf("create: err = %v, want context.Canceled", err)
	}
	if _, err := a.ProjectionGet(ctx, "x"); !errors.Is(err, context.Canceled) {
		t.Errorf("get: err = %v, want context.Canceled", err)
	}
	if list := a.ProjectionList(ctx); list != nil {
		t.Errorf("list: got %v, want nil on cancelled ctx", list)
	}
	if err := a.ProjectionDelete(ctx, "x"); !errors.Is(err, context.Canceled) {
		t.Errorf("delete: err = %v, want context.Canceled", err)
	}
}

// TestProjection_ConcurrentAccess is a smoke test for the projectionsMu
// guard under concurrent Create/Get/Delete/List calls.
func TestProjection_ConcurrentAccess(t *testing.T) {
	a := newTestAssemblerForProj()
	ctx := context.Background()

	const goroutines = 16
	const opsPerGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				id := "p" + string(rune('A'+gid)) + string(rune('a'+i%26))
				switch i % 4 {
				case 0:
					_ = a.ProjectionCreate(ctx, ContextProjection{ID: id, Type: "agent"})
				case 1:
					_, _ = a.ProjectionGet(ctx, id)
				case 2:
					_ = a.ProjectionDelete(ctx, id)
				case 3:
					_ = a.ProjectionList(ctx)
				}
			}
		}(g)
	}
	wg.Wait()
}

// newTestAssemblerForProj returns a DefaultAssembler wired to a no-op
// logger (the in-memory projection map needs no Provider).
func newTestAssemblerForProj() *DefaultAssembler {
	return NewDefaultAssembler(Config{}, fakeDeps{logger: zap.NewNop()})
}
