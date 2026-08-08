// Persists and reloads MCP server launch resolution records.

package mcp

import (
	"context"
	"sync"
)

// ResolutionPersistence is the storage contract for LaunchResolution
// records. The registry calls SaveResolution after every resolve and
// LoadAllResolutions on startup.
type ResolutionPersistence interface {
	SaveResolution(ctx context.Context, res LaunchResolution) error
	LoadAllResolutions(ctx context.Context) ([]LaunchResolution, error)
	DeleteResolution(ctx context.Context, serverID string) error
}

// InMemoryResolutionPersistence is a process-local store so tests and
// v0 binaries have a working ResolutionPersistence without SQLite.
type InMemoryResolutionPersistence struct {
	mu    sync.RWMutex
	store map[string]LaunchResolution
}

func NewInMemoryResolutionPersistence() *InMemoryResolutionPersistence {
	return &InMemoryResolutionPersistence{store: make(map[string]LaunchResolution)}
}

func (p *InMemoryResolutionPersistence) SaveResolution(_ context.Context, res LaunchResolution) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.store[res.ServerID] = res
	return nil
}

func (p *InMemoryResolutionPersistence) LoadAllResolutions(_ context.Context) ([]LaunchResolution, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]LaunchResolution, 0, len(p.store))
	for _, r := range p.store {
		out = append(out, r)
	}
	return out, nil
}

// DeleteResolution removes the record for serverID; a missing record is
// not an error (Unregister may race with a stale Save).
func (p *InMemoryResolutionPersistence) DeleteResolution(_ context.Context, serverID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.store, serverID)
	return nil
}
