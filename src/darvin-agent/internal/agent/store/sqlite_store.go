package store

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"darvin-cowork/backend/internal/agent/session"
)

// SQLiteStore is the GORM-backed SessionStore. Save persists only the
// session metadata (one row in the sessions table); messages are NOT
// persisted by this implementation — see spec §FR-6 P1-1 contract.
// S4 will add a MessageStore path that fills the messages table.
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

// Save upserts the session row by primary key.
func (s *SQLiteStore) Save(ctx context.Context, sess *session.Session) error {
	if sess == nil {
		return ErrNilSession
	}
	row := Session{
		ID:        sess.ID,
		Key:       sess.Key,
		AgentID:   sess.AgentID,
		Status:    string(sess.Status),
		CreatedAt: sess.CreatedAt,
		UpdatedAt: sess.UpdatedAt(),
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
	return out
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
