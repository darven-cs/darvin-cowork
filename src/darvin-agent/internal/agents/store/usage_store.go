package store

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"darvin-cowork/backend/internal/agents/protocol"
)

// UsageRecord is the wire-shape callers see from a UsageStore, distinct
// from the GORM SessionUsage row (mapping happens inside Save). Last is
// the most recent per-turn Usage; Total the cumulative counters.
type UsageRecord struct {
	SessionID         string
	Last              *protocol.Usage
	Total             *protocol.Usage
	LastContextTokens int
	LastPercent       int
	LastModel         string
	RequestCount      int
	UpdatedAt         int64
}

// UsageStore persists per-session usage snapshots. Must be safe for
// concurrent use; a nil UsageStore means "do not persist" to the agent.
type UsageStore interface {
	Save(ctx context.Context, rec *UsageRecord) error
	Get(ctx context.Context, sessionID string) (*UsageRecord, error)
	DeleteBySession(ctx context.Context, sessionID string) error
}

// SQLiteUsageStore is the GORM-backed UsageStore. Shares the *gorm.DB
// pool with SQLiteStore / SQLiteMessageStore; opening the same DSN twice
// would race on SQLite's writer lock.
type SQLiteUsageStore struct {
	db *gorm.DB
}

// NewSQLiteUsageStore wraps the *gorm.DB returned by database.Get().
// Sharing the *gorm.DB with the other stores is the intended use; all
// three participate in AutoMigrate of their respective tables.
func NewSQLiteUsageStore(db *gorm.DB) *SQLiteUsageStore {
	return &SQLiteUsageStore{db: db}
}

// Save inserts (or replaces on PK conflict) one snapshot row.
func (s *SQLiteUsageStore) Save(ctx context.Context, rec *UsageRecord) error {
	if rec == nil {
		return errors.New("store: nil UsageRecord")
	}
	if rec.SessionID == "" {
		return errors.New("store: UsageRecord.SessionID is required")
	}
	row := SessionUsage{
		SessionID:         rec.SessionID,
		LastModel:         rec.LastModel,
		LastContextTokens: rec.LastContextTokens,
		LastPercent:       rec.LastPercent,
		RequestCount:      rec.RequestCount,
		SnapshotAt:        rec.UpdatedAt,
	}
	if rec.Last != nil {
		row.LastUsedTokens = rec.Last.PromptTokens + rec.Last.CompletionTokens
		row.LastPromptTokens = rec.Last.PromptTokens
		row.LastCompletion = rec.Last.CompletionTokens
		row.LastCacheRead = rec.Last.CacheReadTokens
		row.LastCacheWrite = rec.Last.CacheWriteTokens
		row.LastCacheWrite1h = rec.Last.CacheWrite1hTokens
	}
	if rec.Total != nil {
		row.TotalPromptTokens = rec.Total.PromptTokens
		row.TotalCompletion = rec.Total.CompletionTokens
		row.TotalCacheRead = rec.Total.CacheReadTokens
	}
	return s.db.WithContext(ctx).Save(&row).Error
}

// Get returns the snapshot for sessionID. Returns (nil, gorm.ErrRecordNotFound)
// when no row exists.
func (s *SQLiteUsageStore) Get(ctx context.Context, sessionID string) (*UsageRecord, error) {
	if sessionID == "" {
		return nil, errors.New("store: sessionID is required")
	}
	var row SessionUsage
	err := s.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		First(&row).Error
	if err != nil {
		return nil, err
	}
	return &UsageRecord{
		SessionID:         row.SessionID,
		LastModel:         row.LastModel,
		LastContextTokens: row.LastContextTokens,
		LastPercent:       row.LastPercent,
		RequestCount:      row.RequestCount,
		UpdatedAt:         row.SnapshotAt,
		Last: &protocol.Usage{
			PromptTokens:       row.LastPromptTokens,
			CompletionTokens:   row.LastCompletion,
			TotalTokens:        row.LastPromptTokens + row.LastCompletion,
			CacheReadTokens:    row.LastCacheRead,
			CacheWriteTokens:   row.LastCacheWrite,
			CacheWrite1hTokens: row.LastCacheWrite1h,
		},
		Total: &protocol.Usage{
			PromptTokens:     row.TotalPromptTokens,
			CompletionTokens: row.TotalCompletion,
			TotalTokens:      row.TotalPromptTokens + row.TotalCompletion,
			CacheReadTokens:  row.TotalCacheRead,
		},
	}, nil
}

// DeleteBySession removes the snapshot row for sessionID. No-op when no
// row exists — the dispatcher treats missing rows as warn-and-continue.
func (s *SQLiteUsageStore) DeleteBySession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return errors.New("store: sessionID is required")
	}
	return s.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Delete(&SessionUsage{}).Error
}
