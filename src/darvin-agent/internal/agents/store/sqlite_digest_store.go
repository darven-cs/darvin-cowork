package store

import (
	"context"
	"errors"
	"strings"
	"sync"

	"gorm.io/gorm"
)

// seqAllocMaxEntries caps the in-memory seqAlloc map. Beyond this
// size the map is cleared wholesale so long-lived processes
// (test runners, CI loops) do not leak session entries.
const seqAllocMaxEntries = 1000

// SQLiteDigestStore is the GORM-backed DigestStore. Per-session
// sequence allocation is serialised by an in-process mutex and the
// SQLite UNIQUE(session_id, sequence) constraint is the second line
// of defence for cache↔DB divergence.
type SQLiteDigestStore struct {
	db *gorm.DB

	allocsMu  sync.Mutex
	seqAllocs map[string]*seqAlloc
}

// seqAlloc is the per-session sequence allocator.
type seqAlloc struct {
	mu    sync.Mutex
	value int // last confirmed Sequence for the session
}

// NewSQLiteDigestStore wraps the same *gorm.DB the session/message
// stores share. Callers must AutoMigrate(&SessionDigest{}) once at
// startup.
func NewSQLiteDigestStore(db *gorm.DB) *SQLiteDigestStore {
	return &SQLiteDigestStore{
		db:        db,
		seqAllocs: map[string]*seqAlloc{},
	}
}

// Save persists d. When d.Sequence == 0 the store allocates the next
// sequence internally; d.Sequence > 0 is honoured verbatim.
func (s *SQLiteDigestStore) Save(ctx context.Context, d *SessionDigest) error {
	if d == nil {
		return errors.New("digest store: nil SessionDigest")
	}
	if d.SessionID == "" {
		return errors.New("digest store: SessionID required")
	}
	if d.ID == "" {
		return errors.New("digest store: ID required")
	}

	if d.Sequence <= 0 {
		seq, err := s.nextSequence(ctx, d.SessionID)
		if err != nil {
			return err
		}
		d.Sequence = seq
	}

	if err := s.db.WithContext(ctx).Save(d).Error; err != nil {
		if !isUniqueConstraintErr(err) {
			return err
		}
		// UNIQUE conflict — cache and DB have diverged. Invalidate
		// and retry with a freshly allocated sequence.
		s.invalidateAlloc(d.SessionID)
		d.Sequence = 0
		seq, seqErr := s.nextSequence(ctx, d.SessionID)
		if seqErr != nil {
			return seqErr
		}
		d.Sequence = seq
		if err := s.db.WithContext(ctx).Save(d).Error; err != nil {
			if isUniqueConstraintErr(err) {
				return ErrDigestSequenceConflict
			}
			return err
		}
	}
	s.confirmAlloc(d.SessionID, d.Sequence)
	return nil
}

// List returns every digest for sessionID in ascending Sequence order.
func (s *SQLiteDigestStore) List(ctx context.Context, sessionID string) ([]SessionDigest, error) {
	if sessionID == "" {
		return nil, errors.New("digest store: sessionID required")
	}
	var rows []SessionDigest
	if err := s.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Order("sequence asc").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// Latest returns the largest-Sequence row for sessionID, or nil when
// the session has no digests.
func (s *SQLiteDigestStore) Latest(ctx context.Context, sessionID string) (*SessionDigest, error) {
	if sessionID == "" {
		return nil, errors.New("digest store: sessionID required")
	}
	var row SessionDigest
	err := s.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Order("sequence desc").
		Limit(1).
		Find(&row).Error
	if err != nil {
		return nil, err
	}
	if row.ID == "" {
		return nil, nil
	}
	return &row, nil
}

// DeleteBySession removes every digest for sessionID and clears the
// cached allocator so the next Save starts fresh from MAX(sequence).
func (s *SQLiteDigestStore) DeleteBySession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return errors.New("digest store: sessionID required")
	}
	if err := s.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Delete(&SessionDigest{}).Error; err != nil {
		return err
	}
	s.invalidateAlloc(sessionID)
	return nil
}

// nextSequence returns the next sequence number for sessionID.
// Cached per-session after the first call; falls back to a DB scan
// when the entry is missing or has been invalidated.
func (s *SQLiteDigestStore) nextSequence(ctx context.Context, sessionID string) (int, error) {
	alloc := s.getOrCreateAlloc(sessionID)
	alloc.mu.Lock()
	defer alloc.mu.Unlock()

	if alloc.value > 0 {
		next := alloc.value + 1
		alloc.value = next
		return next, nil
	}

	var maxSeq int
	if err := s.db.WithContext(ctx).
		Model(&SessionDigest{}).
		Where("session_id = ?", sessionID).
		Select("COALESCE(MAX(sequence), 0)").
		Scan(&maxSeq).Error; err != nil {
		return 0, err
	}
	alloc.value = maxSeq + 1
	return alloc.value, nil
}

// confirmAlloc records a successful persistence so subsequent Save
// calls skip the MAX(sequence) re-query.
func (s *SQLiteDigestStore) confirmAlloc(sessionID string, seq int) {
	alloc := s.getOrCreateAlloc(sessionID)
	alloc.mu.Lock()
	if seq > alloc.value {
		alloc.value = seq
	}
	alloc.mu.Unlock()
}

// getOrCreateAlloc returns the per-session allocator, creating it on
// demand. When the cache exceeds seqAllocMaxEntries the map is wiped
// to bound long-lived process memory; the next Save falls through
// to the DB MAX(sequence) lookup.
func (s *SQLiteDigestStore) getOrCreateAlloc(sessionID string) *seqAlloc {
	s.allocsMu.Lock()
	defer s.allocsMu.Unlock()
	if len(s.seqAllocs) >= seqAllocMaxEntries {
		s.seqAllocs = map[string]*seqAlloc{}
	}
	if a, ok := s.seqAllocs[sessionID]; ok {
		return a
	}
	a := &seqAlloc{}
	s.seqAllocs[sessionID] = a
	return a
}

// invalidateAlloc removes the per-session cache entry. Called from the
// UNIQUE-conflict retry path and from DeleteBySession so the next
// Save re-reads MAX(sequence) from the DB.
func (s *SQLiteDigestStore) invalidateAlloc(sessionID string) {
	s.allocsMu.Lock()
	delete(s.seqAllocs, sessionID)
	s.allocsMu.Unlock()
}

// isUniqueConstraintErr matches the "UNIQUE constraint failed" wording
// GORM/glebarez emits (no typed error to depend on).
func isUniqueConstraintErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
