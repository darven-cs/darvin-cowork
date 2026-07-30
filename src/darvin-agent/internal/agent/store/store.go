// Package store defines the SessionStore interface and ships an in-memory
// implementation. Additional backends (e.g. SQLite) plug in by satisfying
// the same interface; see MemoryStore for the in-memory reference.
package store

import (
	"context"
	"errors"

	"darvin-cowork/backend/internal/agent/session"
)

// ErrNotFound is returned by Load / Delete when no session with the given
// id exists.
var ErrNotFound = errors.New("store: session not found")

// ErrNilSession is returned by Save when called with a nil *session.Session.
// All SessionStore implementations must surface this — earlier the
// MemoryStore silently swallowed nil inputs, which made SQLite and
// in-memory backends disagree on the same call.
var ErrNilSession = errors.New("store: nil session")

// SessionStore persists and retrieves Sessions by id.
type SessionStore interface {
	// Save persists s. If a session with s.ID already exists it is replaced.
	Save(ctx context.Context, s *session.Session) error

	// Load returns a freshly-allocated *session.Session (deep-copied from
	// storage) or ErrNotFound.
	Load(ctx context.Context, id string) (*session.Session, error)

	// List returns metadata for every known session, sorted by UpdatedAt desc.
	List(ctx context.Context) ([]session.SessionMeta, error)

	// Delete removes the session with the given id. No-op if not found.
	Delete(ctx context.Context, id string) error
}
