package store

import (
	"context"
	"errors"
)

// DigestStore persists compression digests separate from the messages
// table. The largest-Sequence row for a session is the current
// compaction checkpoint.
type DigestStore interface {
	// Save persists a new digest. d.Sequence == 0 → the store allocates
	// the next sequence internally (per-session mutex + SQLite UNIQUE
	// constraint; see SQLiteDigestStore.nextSequence). d.Sequence > 0
	// is honoured verbatim — useful for tests / data-repair scripts.
	Save(ctx context.Context, d *SessionDigest) error

	// List returns every digest for sessionID ordered by Sequence asc.
	List(ctx context.Context, sessionID string) ([]SessionDigest, error)

	// Latest returns the digest row with the largest Sequence for the
	// session, or nil when the session has no digests yet.
	Latest(ctx context.Context, sessionID string) (*SessionDigest, error)

	// DeleteBySession removes every digest for sessionID.
	DeleteBySession(ctx context.Context, sessionID string) error
}

// ErrDigestSequenceConflict is returned by Save when the internal
// per-session mutex + retry path still fails. Production callers
// treat this as a warn-and-continue signal — the live digest is
// already in-memory; only the persistence step failed.
var ErrDigestSequenceConflict = errors.New("digest store: sequence allocation conflict")
