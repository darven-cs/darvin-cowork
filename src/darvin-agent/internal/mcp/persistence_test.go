// Tests for launch resolution persistence.

package mcp

import (
	"context"
	"sort"
	"testing"
	"time"
)

// TestPersistence_SaveAndLoad: a record saved in this process must come
// back out unchanged on Load.
func TestPersistence_SaveAndLoad(t *testing.T) {
	p := NewInMemoryResolutionPersistence()
	ctx := context.Background()
	res := LaunchResolution{
		ServerID:          "filesystem",
		ResolverKind:      ResolverNpx,
		SourceFingerprint: "abc123",
		Status:            StatusReady,
		PackageName:       "@modelcontextprotocol/server-filesystem",
		ResolvedVersion:   "0.5.0",
		ResolvedAt:        time.Now(),
		UpdatedAt:         time.Now(),
	}
	if err := p.SaveResolution(ctx, res); err != nil {
		t.Fatal(err)
	}
	all, err := p.LoadAllResolutions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("len = %d, want 1", len(all))
	}
	if all[0].ServerID != res.ServerID || all[0].Status != res.Status {
		t.Fatalf("roundtrip mismatch: %+v", all[0])
	}
}

// TestPersistence_SaveOverwrites: SaveResolution is a put, not an append.
// Two saves for the same ServerID must collapse to one record, with the
// later one winning.
func TestPersistence_SaveOverwrites(t *testing.T) {
	p := NewInMemoryResolutionPersistence()
	ctx := context.Background()
	now := time.Now()
	if err := p.SaveResolution(ctx, LaunchResolution{ServerID: "github", Status: StatusPending, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := p.SaveResolution(ctx, LaunchResolution{ServerID: "github", Status: StatusReady, UpdatedAt: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	all, _ := p.LoadAllResolutions(ctx)
	if len(all) != 1 {
		t.Fatalf("len = %d, want 1 after overwrite", len(all))
	}
	if all[0].Status != StatusReady {
		t.Fatalf("status = %s, want ready (later write should win)", all[0].Status)
	}
}

// TestPersistence_DeleteRemoves: after Delete, LoadAll must not return
// the deleted record.
func TestPersistence_DeleteRemoves(t *testing.T) {
	p := NewInMemoryResolutionPersistence()
	ctx := context.Background()
	if err := p.SaveResolution(ctx, LaunchResolution{ServerID: "github", Status: StatusReady}); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteResolution(ctx, "github"); err != nil {
		t.Fatal(err)
	}
	all, _ := p.LoadAllResolutions(ctx)
	if len(all) != 0 {
		t.Fatalf("len = %d, want 0 after delete", len(all))
	}
}

// TestPersistence_DeleteMissingIsNotAnError: Unregister races with stale
// resolver goroutines. Deleting a record that was never saved (or was
// already deleted) must not fail.
func TestPersistence_DeleteMissingIsNotAnError(t *testing.T) {
	p := NewInMemoryResolutionPersistence()
	if err := p.DeleteResolution(context.Background(), "ghost"); err != nil {
		t.Fatalf("delete missing should be no-op, got %v", err)
	}
}

// TestPersistence_LoadAllSortedByServerID: LoadAllResolutions is
// documented as unordered; this test sorts before comparing so a
// future map-iteration change cannot flake the suite.
func TestPersistence_LoadAllSortedByServerID(t *testing.T) {
	p := NewInMemoryResolutionPersistence()
	ctx := context.Background()
	for _, id := range []string{"zeta", "alpha", "mu"} {
		if err := p.SaveResolution(ctx, LaunchResolution{ServerID: id, Status: StatusReady}); err != nil {
			t.Fatal(err)
		}
	}
	all, _ := p.LoadAllResolutions(ctx)
	sort.Slice(all, func(i, j int) bool { return all[i].ServerID < all[j].ServerID })
	want := []string{"alpha", "mu", "zeta"}
	for i, w := range want {
		if all[i].ServerID != w {
			t.Fatalf("idx %d: got %s, want %s", i, all[i].ServerID, w)
		}
	}
}
