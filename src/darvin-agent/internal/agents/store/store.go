// Package store defines the SessionStore interface and ships an in-memory
// implementation. Additional backends (SQLite) plug in by satisfying the
// same interface.
package store

import (
	"context"
	"errors"

	"darvin-cowork/backend/internal/agents/session"
)

var (
	ErrNotFound    = errors.New("store: session not found")
	ErrNilSession  = errors.New("store: nil session")
)

// SessionStore persists and retrieves Sessions by id.
type SessionStore interface {
	Save(ctx context.Context, s *session.Session) error
	Load(ctx context.Context, id string) (*session.Session, error)
	List(ctx context.Context) ([]session.SessionMeta, error)
	Delete(ctx context.Context, id string) error
	ListAll(ctx context.Context) ([]Session, error)
	GetByID(ctx context.Context, id string) (Session, error)
	UpdateTitle(ctx context.Context, id, title string) error
	UpdateStatus(ctx context.Context, id, status string) error
	SetClaudeSessionID(ctx context.Context, id string, claudeID *string) error
	Touch(ctx context.Context, id string, ts int64) error
	SearchByTitle(ctx context.Context, query string) ([]Session, error)
	SearchByContent(ctx context.Context, query string, limit int) ([]SearchHitRow, error)
}

// SearchHitRow is one message-content search hit.
type SearchHitRow struct {
	Message      Message
	SessionTitle string
}

type searchHitRow struct {
	ID           string
	SessionID    string
	Role         string
	Content      string
	ToolCalls    string
	Timestamp    int64
	StopReason   string
	ParentID     string
	Done         bool
	Error        *string
	ToolLabel    *string
	SessionTitle string
}