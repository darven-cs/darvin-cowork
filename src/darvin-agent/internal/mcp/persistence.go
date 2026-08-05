package mcp

import (
	"context"
	"sync"
)

// ResolutionPersistence is the storage contract for LaunchResolution
// records. The registry calls SaveResolution after every successful
// (or failed) resolve, and LoadAllResolutions on startup to recover
// the prior session's state. The SQLite implementation lives in a separate
// package; v0 ships with InMemoryResolutionPersistence so the registry can be
// exercised in unit tests without a database.
type ResolutionPersistence interface {
	SaveResolution(ctx context.Context, res LaunchResolution) error
	LoadAllResolutions(ctx context.Context) ([]LaunchResolution, error)
	DeleteResolution(ctx context.Context, serverID string) error
}

// InMemoryResolutionPersistence is a process-local store. It exists so
// tests and the v0 binary have a working ResolutionPersistence without
// pulling in the SQLite dependency.
type InMemoryResolutionPersistence struct {
	mu    sync.RWMutex
	store map[string]LaunchResolution
}

// NewInMemoryResolutionPersistence returns a ready-to-use in-memory
// store. The map is allocated eagerly so Save/Delete on a zero-value
// receiver cannot panic.
func NewInMemoryResolutionPersistence() *InMemoryResolutionPersistence {
	return &InMemoryResolutionPersistence{store: make(map[string]LaunchResolution)}
}

// SaveResolution overwrites any existing record for the same ServerID.
// The registry always passes the latest state, so dedupe is implicit.
func (p *InMemoryResolutionPersistence) SaveResolution(_ context.Context, res LaunchResolution) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.store[res.ServerID] = res
	return nil
}

// LoadAllResolutions returns the stored records in no guaranteed order.
// Callers that need a stable view (e.g. LoadStaleResolutions) sort by
// UpdatedAt themselves.
func (p *InMemoryResolutionPersistence) LoadAllResolutions(_ context.Context) ([]LaunchResolution, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]LaunchResolution, 0, len(p.store))
	for _, r := range p.store {
		out = append(out, r)
	}
	return out, nil
}

// DeleteResolution removes the record for serverID. A missing record
// is not an error — Unregister is allowed to race with a Save from a
// stale resolver goroutine.
func (p *InMemoryResolutionPersistence) DeleteResolution(_ context.Context, serverID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.store, serverID)
	return nil
}
