package store

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// MessageRecord is the wire-shape callers see when reading from / writing
// to a MessageStore. It is intentionally distinct from store.Message
// (the GORM row) so callers don't depend on the persistence type. Mapping
// between MessageRecord and Message happens inside SQLiteMessageStore.Save.
//
// JSON tags are the darvin-api wire shape: Timestamp serialises as
// `createdAt` (matching renderer DarvinMessage) while the Go field keeps
// its internal name so the dispatcher / Agent code isn't renamed.
type MessageRecord struct {
	ID         string  `json:"id"`
	SessionID  string  `json:"sessionId"`
	Role       string  `json:"role"`
	Content    string  `json:"content"`
	ToolCalls  string  `json:"toolCalls,omitempty"` // JSON string; empty for non-tool messages
	Timestamp  int64   `json:"createdAt"`           // unix milliseconds
	StopReason string  `json:"stopReason,omitempty"`
	ParentID   string  `json:"parentId,omitempty"`
	Done       bool    `json:"done"`
	Error      *string `json:"error,omitempty"`
	ToolLabel  *string `json:"toolLabel,omitempty"`
}

// MessageStore persists per-turn message rows. Implementations must be
// safe for concurrent use from the agent's main loop and the dispatch
// goroutines. A nil MessageStore is treated as "do not persist" by the
// dispatcher (hook guards with `if a.msgStore != nil`).
type MessageStore interface {
	// Save persists one message row. Save uses INSERT … ON CONFLICT
	// semantics — calling Save twice with the same MessageRecord.ID is
	// allowed and replaces the previous row.
	Save(ctx context.Context, m *MessageRecord) error

	// List returns messages for sessionID ordered by timestamp ascending.
	// limit + offset support pagination; pass limit<=0 to default to 1000.
	List(ctx context.Context, sessionID string, limit, offset int) ([]MessageRecord, error)

	// Count returns the number of messages stored under sessionID. Used by
	// the list_sessions handler to populate a MessageCount field without
	// issuing a full List per row.
	Count(ctx context.Context, sessionID string) (int, error)

	// AppendContent atomically appends delta to the content of the row
	// with the given id (streaming accumulation). No-op when the row does
	// not exist — the caller (TextDeltaHook) treats a missing row as a
	// warn-and-continue, the final persistAssistantMessages upsert is the
	// source of truth for a completed run.
	AppendContent(ctx context.Context, messageID, delta string) error

	// MarkDone flips done=true on the row, sealing a completed turn.
	MarkDone(ctx context.Context, messageID string) error

	// MarkError flips done=true and stores the error message, sealing a
	// failed turn with a renderer-visible error.
	MarkError(ctx context.Context, messageID, errMsg string) error

	// DeleteBySession removes every message row belonging to sessionID.
	// Called when a session is deleted so orphaned messages do not
	// accumulate in sessions.db.
	DeleteBySession(ctx context.Context, sessionID string) error
}

// SQLiteMessageStore is the GORM-backed MessageStore. The same *gorm.DB
// connection pool is shared with SQLiteStore; opening the same DSN twice
// would race on SQLite's writer lock, so callers should hold exactly one
// *gorm.DB across both stores.
type SQLiteMessageStore struct {
	db *gorm.DB
}

// NewSQLiteMessageStore wraps the *gorm.DB returned by database.Get().
// Sharing the *gorm.DB with NewSQLiteStore is the intended use; both
// stores participate in AutoMigrate of the messages table.
func NewSQLiteMessageStore(db *gorm.DB) *SQLiteMessageStore {
	return &SQLiteMessageStore{db: db}
}

// Save inserts (or replaces on PK conflict) one message row. Returns
// the underlying GORM error verbatim so the caller can log it; callers
// in the dispatch path treat errors as warn-and-continue.
func (s *SQLiteMessageStore) Save(ctx context.Context, m *MessageRecord) error {
	if m == nil {
		return errors.New("store: nil MessageRecord")
	}
	if m.ID == "" {
		return errors.New("store: MessageRecord.ID is required")
	}
	if m.SessionID == "" {
		return errors.New("store: MessageRecord.SessionID is required")
	}
	row := Message{
		ID:         m.ID,
		SessionID:  m.SessionID,
		Role:       m.Role,
		Content:    m.Content,
		ToolCalls:  m.ToolCalls,
		Timestamp:  m.Timestamp,
		StopReason: m.StopReason,
		ParentID:   m.ParentID,
		Done:       m.Done,
		Error:      m.Error,
		ToolLabel:  m.ToolLabel,
	}
	return s.db.WithContext(ctx).Save(&row).Error
}

// List returns messages for sessionID ordered by timestamp ascending
// (chronological — what the renderer needs to replay a conversation).
// limit <= 0 defaults to 1000; offset < 0 is treated as 0.
func (s *SQLiteMessageStore) List(ctx context.Context, sessionID string, limit, offset int) ([]MessageRecord, error) {
	if limit <= 0 {
		limit = 1000
	}
	if offset < 0 {
		offset = 0
	}
	var rows []Message
	if err := s.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Order("timestamp asc").
		Limit(limit).
		Offset(offset).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]MessageRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, MessageRecord{
			ID:         r.ID,
			SessionID:  r.SessionID,
			Role:       r.Role,
			Content:    r.Content,
			ToolCalls:  r.ToolCalls,
			Timestamp:  r.Timestamp,
			StopReason: r.StopReason,
			ParentID:   r.ParentID,
			Done:       r.Done,
			Error:      r.Error,
			ToolLabel:  r.ToolLabel,
		})
	}
	return out, nil
}

// AppendContent atomically appends delta to the content of the row with
// the given id. UPDATE ... content = content || ? is a single SQLite
// statement, so concurrent deltas for the same message accumulate without
// losing a chunk (SQLite serialises writers). A missing row is a no-op.
func (s *SQLiteMessageStore) AppendContent(ctx context.Context, messageID, delta string) error {
	return s.db.WithContext(ctx).
		Model(&Message{}).
		Where("id = ?", messageID).
		UpdateColumn("content", gorm.Expr("content || ?", delta)).Error
}

// MarkDone flips done=true on the row, sealing a completed turn.
func (s *SQLiteMessageStore) MarkDone(ctx context.Context, messageID string) error {
	return s.db.WithContext(ctx).
		Model(&Message{}).
		Where("id = ?", messageID).
		Update("done", true).Error
}

// MarkError flips done=true and stores the error message, sealing a
// failed turn with a renderer-visible error.
func (s *SQLiteMessageStore) MarkError(ctx context.Context, messageID, errMsg string) error {
	return s.db.WithContext(ctx).
		Model(&Message{}).
		Where("id = ?", messageID).
		Updates(map[string]any{"done": true, "error": errMsg}).Error
}

// DeleteBySession removes every message row for sessionID.
func (s *SQLiteMessageStore) DeleteBySession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return errors.New("store: sessionID is required")
	}
	return s.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Delete(&Message{}).Error
}

// Count returns the number of message rows attached to sessionID.
// Cheaper than List(ctx, sessionID, 0, 0) because it does not hydrate.
func (s *SQLiteMessageStore) Count(ctx context.Context, sessionID string) (int, error) {
	var n int64
	if err := s.db.WithContext(ctx).
		Model(&Message{}).
		Where("session_id = ?", sessionID).
		Count(&n).Error; err != nil {
		return 0, err
	}
	return int(n), nil
}
