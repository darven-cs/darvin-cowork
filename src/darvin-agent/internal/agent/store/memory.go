package store

import (
	"context"
	"sort"
	"sync"

	"darvin-cowork/backend/internal/agent/session"
)

// MemoryStore is the in-memory SessionStore. Goroutine-safe.
type MemoryStore struct {
	mu       sync.RWMutex
	sessions map[string]*session.Session
}

// NewMemoryStore constructs an empty MemoryStore implementing SessionStore.
func NewMemoryStore() SessionStore {
	return &MemoryStore{sessions: map[string]*session.Session{}}
}

// Save stores a deep-copied snapshot of s so future mutations to the
// caller's Session don't leak into storage.
func (m *MemoryStore) Save(_ context.Context, s *session.Session) error {
	if s == nil {
		return ErrNilSession
	}
	clone := session.NewSession(s.ID)
	clone.Status = s.Status
	clone.ReplaceAll(s.Messages())
	clone.ReplaceAllMeta(s.Key, s.AgentID, s.Status, s.CreatedAt, s.UpdatedAt())

	m.mu.Lock()
	m.sessions[s.ID] = clone
	m.mu.Unlock()
	return nil
}

// Load returns a deep copy of the stored session, or ErrNotFound.
func (m *MemoryStore) Load(_ context.Context, id string) (*session.Session, error) {
	m.mu.RLock()
	src, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	out := session.NewSession(src.ID)
	out.ReplaceAll(src.Messages())
	out.ReplaceAllMeta(src.Key, src.AgentID, src.Status, src.CreatedAt, src.UpdatedAt())
	return out, nil
}

// List returns metadata for every session, sorted by UpdatedAt desc.
func (m *MemoryStore) List(_ context.Context) ([]session.SessionMeta, error) {
	m.mu.RLock()
	out := make([]session.SessionMeta, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s.Meta())
	}
	m.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

// Delete removes the session, no-op if not found.
func (m *MemoryStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
	return nil
}
