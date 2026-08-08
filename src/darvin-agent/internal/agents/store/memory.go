// In-memory SessionStore implementation.

package store

import (
	"context"
	"sort"
	"strings"
	"sync"

	"darvin-cowork/backend/internal/agents/session"
)

// MemoryStore is the in-memory SessionStore. Goroutine-safe.
type MemoryStore struct {
	mu       sync.RWMutex
	sessions map[string]*session.Session
	// titles / claude mirror the renderer-facing columns that the RPC
	// handlers own. Kept separate from *session.Session because the domain
	// model carries no Title / ClaudeSessionID (same split as SQLiteStore).
	titles map[string]string
	claude map[string]*string
}

// NewMemoryStore constructs an empty MemoryStore implementing SessionStore.
func NewMemoryStore() SessionStore {
	return &MemoryStore{
		sessions: map[string]*session.Session{},
		titles:   map[string]string{},
		claude:   map[string]*string{},
	}
}

// toRow projects a stored session onto the store.Session wire row.
// Callers must hold m.mu.
func (m *MemoryStore) toRow(s *session.Session) Session {
	return Session{
		ID:              s.ID,
		Key:             s.Key,
		AgentID:         s.AgentID,
		Title:           m.titles[s.ID],
		ClaudeSessionID: m.claude[s.ID],
		Status:          string(s.Status),
		CreatedAt:       s.CreatedAt,
		UpdatedAt:       s.UpdatedAt(),
	}
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
	delete(m.titles, id)
	delete(m.claude, id)
	m.mu.Unlock()
	return nil
}

// ListAll returns every session row sorted by updated_at desc, including
// the renderer-facing Title.
func (m *MemoryStore) ListAll(_ context.Context) ([]Session, error) {
	m.mu.RLock()
	out := make([]Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, m.toRow(s))
	}
	m.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

// GetByID returns one session row, or ErrNotFound.
func (m *MemoryStore) GetByID(_ context.Context, id string) (Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	if !ok {
		return Session{}, ErrNotFound
	}
	return m.toRow(s), nil
}

// UpdateTitle sets the renderer-facing title.
func (m *MemoryStore) UpdateTitle(_ context.Context, id, title string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[id]; !ok {
		return ErrNotFound
	}
	m.titles[id] = title
	return nil
}

// UpdateStatus sets the lifecycle status.
func (m *MemoryStore) UpdateStatus(_ context.Context, id, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return ErrNotFound
	}
	s.Status = session.Status(status)
	return nil
}

// SetClaudeSessionID stores (or clears) the optional backend bridge id.
func (m *MemoryStore) SetClaudeSessionID(_ context.Context, id string, claudeID *string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[id]; !ok {
		return ErrNotFound
	}
	m.claude[id] = claudeID
	return nil
}

// Touch refreshes the session's updated_at by re-saving it (Save clones
// and advances UpdatedAt to now).
func (m *MemoryStore) Touch(ctx context.Context, id string, _ int64) error {
	m.mu.RLock()
	s, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return ErrNotFound
	}
	return m.Save(ctx, s)
}

// SearchByTitle returns sessions whose title contains query. An empty /
// whitespace query returns an empty slice (handler-level contract).
func (m *MemoryStore) SearchByTitle(_ context.Context, query string) ([]Session, error) {
	if strings.TrimSpace(query) == "" {
		return []Session{}, nil
	}
	m.mu.RLock()
	out := make([]Session, 0)
	for _, s := range m.sessions {
		// substring match on the renderer-facing title
		if strings.Contains(m.titles[s.ID], query) {
			out = append(out, m.toRow(s))
		}
	}
	m.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

// SearchByContent returns message-content hits. MemoryStore holds no
// messages, so this always returns an empty slice.
func (m *MemoryStore) SearchByContent(_ context.Context, _ string, _ int) ([]SearchHitRow, error) {
	return []SearchHitRow{}, nil
}
