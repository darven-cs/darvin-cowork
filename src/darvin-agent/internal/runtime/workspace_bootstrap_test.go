package runtime

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/memory"
)

func writeBootstrapFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestWorkspaceBootstrapRefreshAll(t *testing.T) {
	dir := t.TempDir()
	writeBootstrapFile(t, dir, memory.BootstrapIdentity, "I-am-the-agent")
	writeBootstrapFile(t, dir, memory.BootstrapUser, "user prefs")
	memMgr := memory.New(dir)
	wb := NewWorkspaceBootstrap(memMgr, zap.NewNop())
	defer wb.Dispose()

	if got := wb.Get(memory.BootstrapIdentity); got != "I-am-the-agent" {
		t.Errorf("identity = %q", got)
	}
	if got := wb.Get(memory.BootstrapUser); got != "user prefs" {
		t.Errorf("user = %q", got)
	}
	if got := wb.Get(memory.BootstrapSoul); got != "" {
		t.Errorf("missing soul = %q, want empty", got)
	}
}

func TestWorkspaceBootstrapGetReturnsEmptyForMissing(t *testing.T) {
	memMgr := memory.New(t.TempDir())
	wb := NewWorkspaceBootstrap(memMgr, zap.NewNop())
	defer wb.Dispose()

	for _, name := range []string{memory.BootstrapIdentity, memory.BootstrapSoul, memory.BootstrapUser} {
		if got := wb.Get(name); got != "" {
			t.Errorf("missing %s = %q, want empty", name, got)
		}
	}
}

func TestWorkspaceBootstrapInvalidateReloads(t *testing.T) {
	dir := t.TempDir()
	writeBootstrapFile(t, dir, memory.BootstrapUser, "v1")
	memMgr := memory.New(dir)
	wb := NewWorkspaceBootstrap(memMgr, zap.NewNop())
	defer wb.Dispose()

	if got := wb.Get(memory.BootstrapUser); got != "v1" {
		t.Fatalf("initial = %q, want v1", got)
	}

	// Simulate the bootstrap.write RPC: rewrite via memMgr.
	if err := memMgr.WriteBootstrap(memory.BootstrapUser, []byte("v2")); err != nil {
		t.Fatalf("WriteBootstrap: %v", err)
	}

	if got := wb.Get(memory.BootstrapUser); got != "v2" {
		t.Fatalf("after write = %q, want v2 (hook should have re-primed cache)", got)
	}
}

func TestWorkspaceBootstrapInvalidateDirectCall(t *testing.T) {
	dir := t.TempDir()
	writeBootstrapFile(t, dir, memory.BootstrapUser, "v1")
	memMgr := memory.New(dir)
	wb := NewWorkspaceBootstrap(memMgr, zap.NewNop())
	defer wb.Dispose()

	wb.Invalidate(memory.BootstrapUser)
	// After direct Invalidate without a re-read, Get returns "" until
	// next Refresh / onBootstrapChanged re-read.
	if got := wb.Get(memory.BootstrapUser); got != "" {
		t.Fatalf("after Invalidate Get = %q, want empty", got)
	}
	wb.RefreshAll(context.Background())
	if got := wb.Get(memory.BootstrapUser); got != "v1" {
		t.Fatalf("after RefreshAll = %q, want v1", got)
	}
}

func TestWorkspaceBootstrapDisposeUnregisters(t *testing.T) {
	dir := t.TempDir()
	writeBootstrapFile(t, dir, memory.BootstrapUser, "v1")
	memMgr := memory.New(dir)
	wb := NewWorkspaceBootstrap(memMgr, zap.NewNop())

	if memMgr.HookCount() != 1 {
		t.Fatalf("after New: HookCount = %d, want 1", memMgr.HookCount())
	}
	wb.Dispose()
	if memMgr.HookCount() != 0 {
		t.Fatalf("after Dispose: HookCount = %d, want 0", memMgr.HookCount())
	}

	// Subsequent writes must not panic on the disposed receiver — the
	// hook has been unregistered, so the manager fanouts to nobody.
	if err := memMgr.WriteBootstrap(memory.BootstrapUser, []byte("v2")); err != nil {
		t.Fatalf("post-dispose write: %v", err)
	}
}

func TestWorkspaceBootstrapDisabledManager(t *testing.T) {
	memMgr := memory.New("") // disabled
	wb := NewWorkspaceBootstrap(memMgr, zap.NewNop())
	defer wb.Dispose()

	for _, name := range []string{memory.BootstrapIdentity, memory.BootstrapSoul, memory.BootstrapUser} {
		if got := wb.Get(name); got != "" {
			t.Errorf("disabled Get(%s) = %q, want empty", name, got)
		}
	}
}

func TestWorkspaceBootstrapNilSafe(t *testing.T) {
	var wb *WorkspaceBootstrap
	if got := wb.Get("anything"); got != "" {
		t.Errorf("nil Get = %q", got)
	}
	wb.Invalidate("anything")
	wb.RefreshAll(context.Background())
	wb.Dispose()
}

func TestWorkspaceBootstrapSharedAcrossSessions(t *testing.T) {
	dir := t.TempDir()
	writeBootstrapFile(t, dir, memory.BootstrapUser, "shared")
	memMgr := memory.New(dir)
	wb := NewWorkspaceBootstrap(memMgr, zap.NewNop())
	defer wb.Dispose()

	// Simulate two sessions sharing the same WorkspaceBootstrap pointer.
	var wg sync.WaitGroup
	const N = 16
	wg.Add(N)
	var mismatches int32
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			if got := wb.Get(memory.BootstrapUser); got != "shared" {
				atomic.AddInt32(&mismatches, 1)
			}
		}()
	}
	wg.Wait()
	if atomic.LoadInt32(&mismatches) != 0 {
		t.Fatalf("concurrent Get mismatches = %d", mismatches)
	}
}

func TestWorkspaceBootstrapConcurrentGetInvalidate(t *testing.T) {
	dir := t.TempDir()
	writeBootstrapFile(t, dir, memory.BootstrapUser, "v1")
	memMgr := memory.New(dir)
	wb := NewWorkspaceBootstrap(memMgr, zap.NewNop())
	defer wb.Dispose()

	var wg sync.WaitGroup
	const N = 64
	wg.Add(N * 2)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			_ = wb.Get(memory.BootstrapUser)
		}()
		go func() {
			defer wg.Done()
			wb.Invalidate(memory.BootstrapUser)
		}()
	}
	wg.Wait()
}
