// Subagent run persistence: interface + GORM-backed SQLite implementation.
// A Subagent row corresponds to one delegate_subagent / parallel_subagents
// tool invocation; the parent session keeps the master view while the
// renderer queries by parentSessionId to populate the Subagents special
// tab in the artifact panel.

package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

var ErrSubagentNotFound = errors.New("store: subagent not found")

// SubagentStore persists Subagent rows. Insert / Update are upsert-style:
// callers treat the row as the source of truth and only flip individual
// fields between writes.
type SubagentStore interface {
	Insert(ctx context.Context, run Subagent) error
	Update(ctx context.Context, run Subagent) error
	Get(ctx context.Context, id string) (Subagent, error)
	ListByParent(ctx context.Context, parentID string) ([]Subagent, error)
	ListStaleRunning(ctx context.Context, before time.Time) ([]Subagent, error)
	Delete(ctx context.Context, id string) error
	DeleteByParent(ctx context.Context, parentID string) error
}

// SQLiteSubagentStore is the GORM-backed SubagentStore. It shares the
// connection pool with the other SQLite-backed stores; opening the same
// DSN twice would race on SQLite's writer lock.
type SQLiteSubagentStore struct {
	db *gorm.DB
}

// NewSQLiteSubagentStore wraps the *gorm.DB returned by database.Get().
func NewSQLiteSubagentStore(db *gorm.DB) *SQLiteSubagentStore {
	return &SQLiteSubagentStore{db: db}
}

// Insert writes a new Subagent row by primary key. Re-inserting the same
// id updates the row in place; callers wanting strict "create or fail"
// semantics should check Get first.
func (s *SQLiteSubagentStore) Insert(ctx context.Context, run Subagent) error {
	if run.ID == "" {
		return errors.New("subagent insert: empty id")
	}
	return s.db.WithContext(ctx).Save(&run).Error
}

// Update replaces an existing Subagent row by primary key. Returns
// ErrSubagentNotFound when the id has no row yet — callers should use
// Insert for the first write.
func (s *SQLiteSubagentStore) Update(ctx context.Context, run Subagent) error {
	if run.ID == "" {
		return errors.New("subagent update: empty id")
	}
	res := s.db.WithContext(ctx).Exec(
		`UPDATE subagent_runs SET
			status = ?,
			description = ?,
			scope_json = ?,
			model = ?,
			tool_call_id = ?,
			ended_at = ?,
			result_text = ?,
			full_result_path = ?,
			tool_calls = ?,
			error_msg = ?
		WHERE id = ?`,
		run.Status, run.Description, run.ScopeJSON, run.Model, run.ToolCallID,
		run.EndedAt, run.ResultText, run.FullResultPath, run.ToolCalls, run.ErrorMsg,
		run.ID,
	)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrSubagentNotFound
	}
	return nil
}

// Get fetches one Subagent row by primary key.
func (s *SQLiteSubagentStore) Get(ctx context.Context, id string) (Subagent, error) {
	var row Subagent
	if err := s.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Subagent{}, ErrSubagentNotFound
		}
		return Subagent{}, err
	}
	return row, nil
}

// ListByParent returns every Subagent row for a parent session, ordered
// started_at desc so the renderer can render the most recent run first
// without resorting.
func (s *SQLiteSubagentStore) ListByParent(ctx context.Context, parentID string) ([]Subagent, error) {
	var rows []Subagent
	if err := s.db.WithContext(ctx).
		Where("parent_id = ?", parentID).
		Order("started_at desc").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ListStaleRunning returns rows still in 'running' whose StartedAt is
// older than `before`. Used by subagent.Manager at startup to flip
// leftover rows from a previous process lifetime to 'error'.
func (s *SQLiteSubagentStore) ListStaleRunning(ctx context.Context, before time.Time) ([]Subagent, error) {
	var rows []Subagent
	if err := s.db.WithContext(ctx).
		Where("status = ? AND started_at < ?", "running", before).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// Delete removes one Subagent row by primary key.
func (s *SQLiteSubagentStore) Delete(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Where("id = ?", id).Delete(&Subagent{}).Error
}

// DeleteByParent removes every Subagent row for a parent session. Used
// when the parent session is hard-deleted.
func (s *SQLiteSubagentStore) DeleteByParent(ctx context.Context, parentID string) error {
	return s.db.WithContext(ctx).Where("parent_id = ?", parentID).Delete(&Subagent{}).Error
}
