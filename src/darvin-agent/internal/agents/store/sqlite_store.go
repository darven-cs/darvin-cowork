// GORM-backed SessionStore for session metadata.

package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"darvin-cowork/backend/internal/agents/session"
)

// SQLiteStore is the GORM-backed SessionStore. Save persists only the
// session metadata (one row in the sessions table); messages are NOT
// persisted by this implementation.
type SQLiteStore struct {
	db *gorm.DB
}

// NewSQLiteStore wraps the *gorm.DB returned by database.Get(). Sharing
// the same connection pool across multiple SQLiteStore instances is
// fine; opening the same DSN twice is not (would race on the SQLite
// write lock).
func NewSQLiteStore(db *gorm.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

// Save upserts the session row by primary key. Title / ClaudeSessionID /
// WorkspaceID are preserved from the existing row: they are
// renderer-facing metadata owned by the RPC handlers, and the agent's
// session.Session domain model carries no Title, so a prompt's metadata
// save must not clobber a rename. SystemPrompt / Identity are carried by
// the in-memory session and written verbatim.
func (s *SQLiteStore) Save(ctx context.Context, sess *session.Session) error {
	if sess == nil {
		return ErrNilSession
	}
	systemPrompt, identity := sess.Prompt()
	row := Session{
		ID:           sess.ID,
		Key:          sess.Key,
		AgentID:      sess.AgentID,
		Status:       string(sess.Status),
		CreatedAt:    sess.CreatedAt,
		UpdatedAt:    sess.UpdatedAt(),
		SystemPrompt: systemPrompt,
		Identity:     identity,
	}
	// One extra SELECT per metadata save; sessions table is tiny and the
	// trade keeps rename / claude-bridge writes authoritative.
	if existing, err := s.GetByID(ctx, sess.ID); err == nil {
		row.Title = existing.Title
		row.ClaudeSessionID = existing.ClaudeSessionID
		row.WorkspaceID = existing.WorkspaceID
	}
	return s.db.WithContext(ctx).Save(&row).Error
}

// Load reads one session row by id. The returned *session.Session has
// its metadata populated via ReplaceAllMeta; its messages slice stays
// empty per the P1-1 contract.
func (s *SQLiteStore) Load(ctx context.Context, id string) (*session.Session, error) {
	var row Session
	if err := s.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return row.toSession(), nil
}

// List returns metadata for every session sorted by updated_at desc.
// MessageCount is always 0 per the P1-1 contract.
func (s *SQLiteStore) List(ctx context.Context) ([]session.SessionMeta, error) {
	var rows []Session
	if err := s.db.WithContext(ctx).Order("updated_at desc").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]session.SessionMeta, 0, len(rows))
	for _, r := range rows {
		out = append(out, session.SessionMeta{
			ID:           r.ID,
			Key:          r.Key,
			AgentID:      r.AgentID,
			Status:       session.Status(r.Status),
			CreatedAt:    r.CreatedAt,
			UpdatedAt:    r.UpdatedAt,
			MessageCount: 0,
		})
	}
	return out, nil
}

// Delete removes the session row. GORM returns nil error even when no
// row matches, so Delete is idempotent.
func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Where("id = ?", id).Delete(&Session{}).Error
}

// toSession hydrates a session.Session from a Session row. Messages
// are intentionally left empty.
func (r *Session) toSession() *session.Session {
	out := session.NewSession(r.ID)
	out.ReplaceAllMeta(r.Key, r.AgentID, session.Status(r.Status), r.CreatedAt, r.UpdatedAt)
	out.SetPrompt(r.SystemPrompt, r.Identity)
	return out
}

// ListAll returns every session row (renderer-facing shape with Title),
// sorted by updated_at desc.
func (s *SQLiteStore) ListAll(ctx context.Context) ([]Session, error) {
	var rows []Session
	if err := s.db.WithContext(ctx).Order("updated_at desc").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ListByWorkspace returns every session row bound to workspaceID sorted by
// updated_at desc.
func (s *SQLiteStore) ListByWorkspace(ctx context.Context, workspaceID string) ([]Session, error) {
	var rows []Session
	if err := s.db.WithContext(ctx).
		Where("workspace_id = ?", workspaceID).
		Order("updated_at desc").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// GetByID returns one session row by id, or ErrNotFound.
func (s *SQLiteStore) GetByID(ctx context.Context, id string) (Session, error) {
	var row Session
	if err := s.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Session{}, ErrNotFound
		}
		return Session{}, err
	}
	return row, nil
}

// UpdateTitle sets the renderer-facing title. No '新建会话' fallback here —
// the handler owns that decision.
func (s *SQLiteStore) UpdateTitle(ctx context.Context, id, title string) error {
	return s.db.WithContext(ctx).
		Model(&Session{}).
		Where("id = ?", id).
		Update("title", title).Error
}

// UpdateStatus sets the lifecycle status.
func (s *SQLiteStore) UpdateStatus(ctx context.Context, id, status string) error {
	return s.db.WithContext(ctx).
		Model(&Session{}).
		Where("id = ?", id).
		Update("status", status).Error
}

// SetClaudeSessionID stores (or clears, when claudeID is nil) the
// optional backend bridge id.
func (s *SQLiteStore) SetClaudeSessionID(ctx context.Context, id string, claudeID *string) error {
	return s.db.WithContext(ctx).
		Model(&Session{}).
		Where("id = ?", id).
		Update("claude_session_id", claudeID).Error
}

// BindWorkspace sets the session's workspace_id binding.
func (s *SQLiteStore) BindWorkspace(ctx context.Context, id, workspaceID string) error {
	return s.db.WithContext(ctx).
		Model(&Session{}).
		Where("id = ?", id).
		Update("workspace_id", workspaceID).Error
}

// Touch refreshes updated_at to ts so list ordering advances.
func (s *SQLiteStore) Touch(ctx context.Context, id string, ts int64) error {
	// GORM time columns map to time.Time; pass a time derived from ts so
	// the column type stays consistent with Save / Load.
	return s.db.WithContext(ctx).
		Model(&Session{}).
		Where("id = ?", id).
		Update("updated_at", time.UnixMilli(ts)).Error
}

// SearchByTitle returns sessions whose title contains query (substring,
// case-sensitive, same semantics the Electron SessionStore used). An
// empty / whitespace query returns an empty slice — the handler is the
// layer that decides "no query → no results".
func (s *SQLiteStore) SearchByTitle(ctx context.Context, query string) ([]Session, error) {
	if strings.TrimSpace(query) == "" {
		return []Session{}, nil
	}
	var rows []Session
	if err := s.db.WithContext(ctx).
		Where("title LIKE ?", "%"+query+"%").
		Order("updated_at desc").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// SearchByContent returns message rows whose content contains query,
// newest first, each decorated with the owning session title. An empty /
// whitespace query returns an empty slice.
func (s *SQLiteStore) SearchByContent(ctx context.Context, query string, limit int) ([]SearchHitRow, error) {
	if strings.TrimSpace(query) == "" {
		return []SearchHitRow{}, nil
	}
	if limit <= 0 {
		limit = 100
	}
	// Scan into a flat row first: GORM's Scan cannot map a joined result
	// onto a struct that embeds another struct (Message) as a relation.
	var rows []searchHitRow
	err := s.db.WithContext(ctx).
		Table("messages AS m").
		Select("m.id, m.session_id, m.role, m.content, m.tool_calls, m.timestamp, m.stop_reason, m.parent_id, m.done, m.error, m.tool_label, s.title AS session_title").
		Joins("JOIN sessions AS s ON s.id = m.session_id").
		Where("m.content LIKE ?", "%"+query+"%").
		Order("m.timestamp desc").
		Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	hits := make([]SearchHitRow, 0, len(rows))
	for _, r := range rows {
		hits = append(hits, SearchHitRow{
			Message: Message{
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
			},
			SessionTitle: r.SessionTitle,
		})
	}
	return hits, nil
}

// Close releases the underlying SQLite connection. main.go calls this
// during graceful shutdown as the final step (after Agent.Abort and the
// event.Bus subscription has been drained) so that in-flight writes have
// finished and the fd can be returned to the OS cleanly.
func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
