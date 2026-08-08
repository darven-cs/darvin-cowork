package store

import (
	"context"
	"sort"
	"sync"
)

// MemoryDigestStore is the in-memory DigestStore used by handler tests
// and any code path that prefers a zero-disk stand-in.
type MemoryDigestStore struct {
	mu      sync.RWMutex
	rows    map[string][]SessionDigest // sessionID → rows sorted by sequence asc
	counter map[string]int             // sessionID → next sequence to allocate
}

// NewMemoryDigestStore constructs an empty MemoryDigestStore.
func NewMemoryDigestStore() DigestStore {
	return &MemoryDigestStore{
		rows:    map[string][]SessionDigest{},
		counter: map[string]int{},
	}
}

// Save appends d. d.Sequence == 0 → allocate from the per-session
// counter; d.Sequence > 0 → honoured verbatim. Append-only; mirrors
// the SQLite UNIQUE constraint by rejecting duplicate Sequence values
// with ErrDigestSequenceConflict.
func (m *MemoryDigestStore) Save(_ context.Context, d *SessionDigest) error {
	if d == nil {
		return ErrDigestSequenceConflict
	}
	if d.SessionID == "" || d.ID == "" {
		return ErrDigestSequenceConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if d.Sequence <= 0 {
		m.counter[d.SessionID]++
		d.Sequence = m.counter[d.SessionID]
	} else if d.Sequence > m.counter[d.SessionID] {
		m.counter[d.SessionID] = d.Sequence
	}
	for _, existing := range m.rows[d.SessionID] {
		if existing.Sequence == d.Sequence {
			return ErrDigestSequenceConflict
		}
	}
	clone := *d
	m.rows[d.SessionID] = append(m.rows[d.SessionID], clone)
	sort.Slice(m.rows[d.SessionID], func(i, j int) bool {
		return m.rows[d.SessionID][i].Sequence < m.rows[d.SessionID][j].Sequence
	})
	return nil
}

// List returns every digest for sessionID in ascending Sequence order.
func (m *MemoryDigestStore) List(_ context.Context, sessionID string) ([]SessionDigest, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	src := m.rows[sessionID]
	if len(src) == 0 {
		return []SessionDigest{}, nil
	}
	out := make([]SessionDigest, len(src))
	copy(out, src)
	return out, nil
}

// Latest returns the row with the largest Sequence, or nil when empty.
func (m *MemoryDigestStore) Latest(_ context.Context, sessionID string) (*SessionDigest, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	src := m.rows[sessionID]
	if len(src) == 0 {
		return nil, nil
	}
	latest := src[len(src)-1]
	return &latest, nil
}

// DeleteBySession removes every digest for sessionID and resets the
// counter so the next Save starts from 1.
func (m *MemoryDigestStore) DeleteBySession(_ context.Context, sessionID string) error {
	m.mu.Lock()
	delete(m.rows, sessionID)
	delete(m.counter, sessionID)
	m.mu.Unlock()
	return nil
}