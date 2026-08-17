// Key-value app-state store backing active_session_id persistence.

package store

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// AppStateStore reads / writes the app_state key-value table. It backs
// the `active_session_id` persistence so the current session survives a
// process restart.
type AppStateStore struct {
	db *gorm.DB
}

// NewAppStateStore wraps the shared *gorm.DB (same handle as
// NewSQLiteStore / NewSQLiteMessageStore).
func NewAppStateStore(db *gorm.DB) *AppStateStore {
	return &AppStateStore{db: db}
}

// GetActiveSession returns the persisted active session id, or "" when no
// row exists (fresh install — the caller falls back to empty state).
func (s *AppStateStore) GetActiveSession(ctx context.Context) (string, error) {
	var row AppState
	err := s.db.WithContext(ctx).First(&row, "key = ?", "active_session_id").Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return row.Value, nil
}

// SetActiveSession persists the active session id (upsert by key).
func (s *AppStateStore) SetActiveSession(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).
		Save(&AppState{Key: "active_session_id", Value: id}).Error
}

// GetActiveWorkspace returns the persisted active workspace id, or "" when
// no row exists (fresh install — the caller falls back to empty state).
func (s *AppStateStore) GetActiveWorkspace(ctx context.Context) (string, error) {
	var row AppState
	err := s.db.WithContext(ctx).First(&row, "key = ?", "active_workspace_id").Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return row.Value, nil
}

// SetActiveWorkspace persists the active workspace id (upsert by key).
func (s *AppStateStore) SetActiveWorkspace(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).
		Save(&AppState{Key: "active_workspace_id", Value: id}).Error
}
