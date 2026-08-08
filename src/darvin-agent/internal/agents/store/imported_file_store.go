// Persists imported workspace files per session with a size cap.

package store

import (
	"context"
	"errors"
	"sync"

	"gorm.io/gorm"
)

// MaxWorkspaceBytes is the soft cap on one session's imported workspace
// content; further imports are rejected past it (no proactive GC).
const MaxWorkspaceBytes int64 = 500 * 1024 * 1024 // 500 MiB

var (
	// ErrWorkspaceFull is returned when an Insert would push the session's
	// imported workspace over MaxWorkspaceBytes.
	ErrWorkspaceFull = errors.New("store: workspace would exceed limit")
	// ErrDuplicate is returned when a file with the same sha256 is already
	// imported into the session.
	ErrDuplicate = errors.New("store: file with same sha256 already imported")
)

// ImportedFileStore persists ImportedFile rows and enforces the
// per-session workspace capacity limit inside Insert.
type ImportedFileStore struct {
	db *gorm.DB
	// mu serialises Insert so capacity check + dedupe + create are one
	// unit; SQLite's deferred transactions alone would let two
	// in-flight Inserts both read a stale SUM and race past the cap.
	mu sync.Mutex
}

// NewImportedFileStore wraps the shared *gorm.DB.
func NewImportedFileStore(db *gorm.DB) *ImportedFileStore {
	return &ImportedFileStore{db: db}
}

// Insert stores one imported file. Capacity check + sha256 dedupe run in
// one mutex-guarded transaction so concurrent imports cannot race past
// the workspace cap. Returns ErrDuplicate or ErrWorkspaceFull on failure.
func (s *ImportedFileStore) Insert(ctx context.Context, rec ImportedFile) (ImportedFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing ImportedFile
		if err := tx.Where("session_id = ? AND sha256 = ?", rec.SessionID, rec.Sha256).
			First(&existing).Error; err == nil {
			return ErrDuplicate
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var sum int64
		if err := tx.Model(&ImportedFile{}).
			Where("session_id = ?", rec.SessionID).
			Select("COALESCE(SUM(size), 0)").Scan(&sum).Error; err != nil {
			return err
		}
		if sum+rec.Size > MaxWorkspaceBytes {
			return ErrWorkspaceFull
		}
		return tx.Create(&rec).Error
	})
	if err != nil {
		return ImportedFile{}, err
	}
	return rec, nil
}

// Delete removes one imported file row for sessionID.
func (s *ImportedFileStore) Delete(ctx context.Context, sessionID, relPath string) error {
	return s.db.WithContext(ctx).
		Where("session_id = ? AND relative_path = ?", sessionID, relPath).
		Delete(&ImportedFile{}).Error
}

// List returns the session's imported files, newest first.
func (s *ImportedFileStore) List(ctx context.Context, sessionID string) ([]ImportedFile, error) {
	var rows []ImportedFile
	err := s.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Order("imported_at DESC").Find(&rows).Error
	return rows, err
}

// SumBytes returns the total imported size for sessionID.
func (s *ImportedFileStore) SumBytes(ctx context.Context, sessionID string) (int64, error) {
	var sum int64
	err := s.db.WithContext(ctx).Model(&ImportedFile{}).
		Where("session_id = ?", sessionID).
		Select("COALESCE(SUM(size), 0)").Scan(&sum).Error
	return sum, err
}

// DeleteBySession removes every imported file row for sessionID. Called
// when a session is deleted so orphaned rows do not accumulate.
func (s *ImportedFileStore) DeleteBySession(ctx context.Context, sessionID string) error {
	return s.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Delete(&ImportedFile{}).Error
}
