// GORM-backed WorkspaceStore for the first-class workspace rows.

package store

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// SQLiteWorkspaceStore persists Workspace rows. One workspace owns one
// physical directory and can be referenced by many sessions.
type SQLiteWorkspaceStore struct {
	db *gorm.DB
}

// NewSQLiteWorkspaceStore wraps the shared *gorm.DB.
func NewSQLiteWorkspaceStore(db *gorm.DB) *SQLiteWorkspaceStore {
	return &SQLiteWorkspaceStore{db: db}
}

// Create upserts a workspace row by primary key.
func (s *SQLiteWorkspaceStore) Create(ctx context.Context, w Workspace) error {
	return s.db.WithContext(ctx).Save(&w).Error
}

// GetByID returns one workspace row, or ErrNotFound.
func (s *SQLiteWorkspaceStore) GetByID(ctx context.Context, id string) (Workspace, error) {
	var row Workspace
	if err := s.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Workspace{}, ErrNotFound
		}
		return Workspace{}, err
	}
	return row, nil
}

// GetByRoot returns the workspace owning rootPath, or ErrNotFound.
// Used by the one-time migration to reuse an existing directory.
func (s *SQLiteWorkspaceStore) GetByRoot(ctx context.Context, rootPath string) (Workspace, error) {
	var row Workspace
	if err := s.db.WithContext(ctx).First(&row, "root_path = ?", rootPath).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Workspace{}, ErrNotFound
		}
		return Workspace{}, err
	}
	return row, nil
}

// List returns every workspace row sorted by updated_at desc.
func (s *SQLiteWorkspaceStore) List(ctx context.Context) ([]Workspace, error) {
	var rows []Workspace
	if err := s.db.WithContext(ctx).Order("updated_at desc").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// UpdateName sets the user-visible workspace name.
func (s *SQLiteWorkspaceStore) UpdateName(ctx context.Context, id, name string) error {
	return s.db.WithContext(ctx).
		Model(&Workspace{}).
		Where("id = ?", id).
		Update("name", name).Error
}

// UpdateRoot relocates the workspace to a new root path and bumps UpdatedAt.
// The directory itself is expected to already exist; callers that want a
// mkdir-on-update behavior call os.MkdirAll before invoking this.
func (s *SQLiteWorkspaceStore) UpdateRoot(ctx context.Context, id, rootPath string) error {
	return s.db.WithContext(ctx).
		Model(&Workspace{}).
		Where("id = ?", id).
		Updates(map[string]any{"root_path": rootPath, "updated_at": gorm.Expr("CURRENT_TIMESTAMP")}).Error
}

// Delete removes the workspace row by id.
func (s *SQLiteWorkspaceStore) Delete(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Where("id = ?", id).Delete(&Workspace{}).Error
}

// CountSessions returns the number of sessions bound to the workspace.
func (s *SQLiteWorkspaceStore) CountSessions(ctx context.Context, id string) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&Session{}).
		Where("workspace_id = ?", id).
		Count(&count).Error
	return count, err
}

// ListSessionIDs returns the ids of every session bound to the workspace,
// newest first. Used to compute the next-active session on workspace switch.
func (s *SQLiteWorkspaceStore) ListSessionIDs(ctx context.Context, id string) ([]string, error) {
	var rows []Session
	if err := s.db.WithContext(ctx).
		Where("workspace_id = ?", id).
		Order("updated_at desc").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out, nil
}
