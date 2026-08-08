// Package memory holds the long-term memory subsystem. The full
// MEMORY.md block-aware parsing + FTS5 index is owned by the
// memory-core spec; this file ships the bootstrap-file facade.
package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

const (
	BootstrapIdentity = "IDENTITY.md"
	BootstrapSoul     = "SOUL.md"
	BootstrapUser     = "USER.md"
)

// bootstrapNames is the closed set the Manager accepts.
var bootstrapNames = map[string]struct{}{
	BootstrapIdentity: {},
	BootstrapSoul:     {},
	BootstrapUser:     {},
}

// IsBootstrapName reports whether name is one of the whitelisted
// bootstrap files.
func IsBootstrapName(name string) bool {
	_, ok := bootstrapNames[name]
	return ok
}

// SearchResult is one MEMORY.md FTS hit.
type SearchResult struct {
	Text    string
	Section string
	Score   float64
}

// Manager is the long-term-memory facade. Disabled (empty
// workspaceDir) when the memory subsystem is not wired; every method
// returns "" / nil / nil-error in that case (FR-12 graceful degrade).
type Manager struct {
	workspaceDir string

	mu      sync.RWMutex
	hooks   map[string]func(name string)
	enabled bool
}

// New constructs a Manager rooted at workspaceDir.
func New(workspaceDir string) *Manager {
	m := &Manager{
		workspaceDir: workspaceDir,
		hooks:        map[string]func(name string){},
	}
	m.enabled = workspaceDir != ""
	return m
}

// Enabled reports whether the memory subsystem is wired.
func (m *Manager) Enabled() bool {
	return m != nil && m.enabled
}

// WorkspaceDir returns the bootstrap directory root.
func (m *Manager) WorkspaceDir() string {
	if m == nil {
		return ""
	}
	return m.workspaceDir
}

// ReadBootstrap returns the content of the named bootstrap file.
// Returns "" on any error: disabled Manager, unknown name, missing
// file, or read failure. Empty-string return is the FR-12 graceful
// degrade signal.
func (m *Manager) ReadBootstrap(_ context.Context, name string) string {
	if m == nil || !m.enabled {
		return ""
	}
	if !IsBootstrapName(name) {
		return ""
	}
	path := filepath.Join(m.workspaceDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// WriteBootstrap replaces the named bootstrap file's content and
// fans out to every registered hook. Returns an error for unknown
// names or disabled Manager; on-disk errors short-circuit the fan-out
// so observers never see a write that did not land.
func (m *Manager) WriteBootstrap(name string, content []byte) error {
	if m == nil || !m.enabled {
		return errors.New("memory: manager not enabled")
	}
	if !IsBootstrapName(name) {
		return errors.New("memory: unknown bootstrap name")
	}
	path := filepath.Join(m.workspaceDir, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return err
	}
	m.fanout(name)
	return nil
}

// RegisterBootstrapChanged installs hook under id. Last-write-wins;
// the owner must UnregisterBootstrapChanged in its destruction path
// so the registry does not leak stale closures.
func (m *Manager) RegisterBootstrapChanged(id string, hook func(name string)) {
	if m == nil || id == "" || hook == nil {
		return
	}
	m.mu.Lock()
	if m.hooks == nil {
		m.hooks = map[string]func(name string){}
	}
	m.hooks[id] = hook
	m.mu.Unlock()
}

// UnregisterBootstrapChanged removes the hook registered under id.
// Idempotent.
func (m *Manager) UnregisterBootstrapChanged(id string) {
	if m == nil || id == "" {
		return
	}
	m.mu.Lock()
	delete(m.hooks, id)
	m.mu.Unlock()
}

// fanout invokes hooks outside the manager lock so a hook that
// re-enters (e.g. via ReadBootstrap) does not deadlock.
func (m *Manager) fanout(name string) {
	m.mu.RLock()
	snapshot := make([]func(string), 0, len(m.hooks))
	for _, h := range m.hooks {
		snapshot = append(snapshot, h)
	}
	m.mu.RUnlock()
	for _, h := range snapshot {
		h(name)
	}
}

// Search is the MEMORY.md FTS hook. Returns nil until a real FTS
// implementation lands; downstream consumers degrade to "no MEMORY
// block".
func (m *Manager) Search(_ context.Context, _ string, _ int) []SearchResult {
	if m == nil || !m.enabled {
		return nil
	}
	return nil
}

// HookCount returns the number of registered hooks. Test-only
// inspection helper.
func (m *Manager) HookCount() int {
	if m == nil {
		return 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.hooks)
}
