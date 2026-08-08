// Tests for the memory manager's bootstrap file facade.

package memory

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestNewEmptyManagerIsDisabled(t *testing.T) {
	m := New("")
	if m.Enabled() {
		t.Fatalf("empty workspace → Enabled() = true, want false")
	}
	if got := m.ReadBootstrap(context.Background(), BootstrapUser); got != "" {
		t.Fatalf("ReadBootstrap on disabled = %q, want \"\"", got)
	}
}

func TestReadWriteBootstrapRoundtrip(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)
	if !m.Enabled() {
		t.Fatalf("Manager(workspaceDir) Enabled() = false, want true")
	}

	if err := m.WriteBootstrap(BootstrapUser, []byte("user content")); err != nil {
		t.Fatalf("WriteBootstrap: %v", err)
	}
	got := m.ReadBootstrap(context.Background(), BootstrapUser)
	if got != "user content" {
		t.Fatalf("ReadBootstrap = %q, want %q", got, "user content")
	}

	// File landed under <workspaceDir>/<name>.
	if _, err := os.Stat(filepath.Join(dir, BootstrapUser)); err != nil {
		t.Fatalf("expected file on disk: %v", err)
	}
}

func TestReadBootstrapMissingFile(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)
	got := m.ReadBootstrap(context.Background(), BootstrapUser)
	if got != "" {
		t.Fatalf("missing file → %q, want \"\"", got)
	}
}

func TestWriteBootstrapRejectsUnknownName(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)
	if err := m.WriteBootstrap("../etc/passwd", []byte("oops")); err == nil {
		t.Fatalf("WriteBootstrap with traversal name = nil, want error")
	}
	if err := m.WriteBootstrap("MEMORY.md", []byte("oops")); err == nil {
		t.Fatalf("WriteBootstrap with non-whitelisted name = nil, want error")
	}
}

func TestReadBootstrapRejectsUnknownName(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)
	if got := m.ReadBootstrap(context.Background(), "anything.txt"); got != "" {
		t.Fatalf("unknown name → %q, want \"\"", got)
	}
}

func TestRegisterBootstrapChangedFires(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)

	var called int32
	var lastName string
	var mu sync.Mutex
	var hooks []string

	m.RegisterBootstrapChanged("hook-a", func(name string) {
		atomic.AddInt32(&called, 1)
		mu.Lock()
		lastName = name
		hooks = append(hooks, "a")
		mu.Unlock()
	})
	m.RegisterBootstrapChanged("hook-b", func(name string) {
		atomic.AddInt32(&called, 1)
		mu.Lock()
		hooks = append(hooks, "b")
		mu.Unlock()
	})

	if err := m.WriteBootstrap(BootstrapUser, []byte("x")); err != nil {
		t.Fatalf("WriteBootstrap: %v", err)
	}
	if atomic.LoadInt32(&called) != 2 {
		t.Fatalf("call count = %d, want 2", called)
	}
	if lastName != BootstrapUser {
		t.Fatalf("last name = %q, want %q", lastName, BootstrapUser)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(hooks) != 2 {
		t.Fatalf("hooks fired = %v, want both", hooks)
	}
}

func TestUnregisterBootstrapChangedStopsCalls(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)

	var calls int32
	m.RegisterBootstrapChanged("hook", func(string) {
		atomic.AddInt32(&calls, 1)
	})

	_ = m.WriteBootstrap(BootstrapUser, []byte("first"))
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("after first write: calls = %d, want 1", calls)
	}

	m.UnregisterBootstrapChanged("hook")
	_ = m.WriteBootstrap(BootstrapUser, []byte("second"))
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("after unregister + write: calls = %d, want 1 (unchanged)", calls)
	}
}

func TestUnregisterBootstrapChangedIdempotent(t *testing.T) {
	m := New(t.TempDir())
	m.UnregisterBootstrapChanged("never-registered")
	m.UnregisterBootstrapChanged("")
}

func TestRegisterBootstrapChangedLastWins(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)

	var seenA, seenB int32
	m.RegisterBootstrapChanged("dup", func(string) { atomic.AddInt32(&seenA, 1) })
	m.RegisterBootstrapChanged("dup", func(string) { atomic.AddInt32(&seenB, 1) })

	_ = m.WriteBootstrap(BootstrapUser, []byte("x"))
	if atomic.LoadInt32(&seenA) != 0 {
		t.Fatalf("seenA = %d, want 0 (overwritten)", seenA)
	}
	if atomic.LoadInt32(&seenB) != 1 {
		t.Fatalf("seenB = %d, want 1", seenB)
	}
}

func TestRegisterBootstrapChangedIgnoresEmptyOrNil(t *testing.T) {
	m := New(t.TempDir())
	m.RegisterBootstrapChanged("", func(string) {})
	m.RegisterBootstrapChanged("id", nil)
	if m.HookCount() != 0 {
		t.Fatalf("HookCount = %d, want 0", m.HookCount())
	}
}

func TestHookCount(t *testing.T) {
	m := New(t.TempDir())
	if m.HookCount() != 0 {
		t.Fatalf("initial HookCount = %d, want 0", m.HookCount())
	}
	m.RegisterBootstrapChanged("a", func(string) {})
	m.RegisterBootstrapChanged("b", func(string) {})
	if m.HookCount() != 2 {
		t.Fatalf("after two registers HookCount = %d, want 2", m.HookCount())
	}
	m.UnregisterBootstrapChanged("a")
	if m.HookCount() != 1 {
		t.Fatalf("after one unregister HookCount = %d, want 1", m.HookCount())
	}
}

func TestSearchNoopUntilMemoryCore(t *testing.T) {
	m := New(t.TempDir())
	hits := m.Search(context.Background(), "anything", 5)
	if hits != nil {
		t.Fatalf("Search placeholder = %v, want nil", hits)
	}
}

func TestNilManagerSafe(t *testing.T) {
	var m *Manager
	if m.Enabled() {
		t.Fatalf("nil.Enabled() = true")
	}
	if got := m.ReadBootstrap(context.Background(), BootstrapUser); got != "" {
		t.Fatalf("nil.ReadBootstrap = %q", got)
	}
	if err := m.WriteBootstrap(BootstrapUser, []byte("x")); err == nil {
		t.Fatalf("nil.WriteBootstrap = nil err")
	}
	m.RegisterBootstrapChanged("x", func(string) {})
	m.UnregisterBootstrapChanged("x")
}
