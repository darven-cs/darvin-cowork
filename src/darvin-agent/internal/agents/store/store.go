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
	ErrNotFound   = errors.New("store: session not found")
	ErrNilSession = errors.New("store: nil session")
)

// SessionStore persists and retrieves Sessions by id.
type SessionStore interface {
	Save(ctx context.Context, s *session.Session) error
	Load(ctx context.Context, id string) (*session.Session, error)
	List(ctx context.Context) ([]session.SessionMeta, error)
	Delete(ctx context.Context, id string) error
	ListAll(ctx context.Context) ([]Session, error)
	ListByWorkspace(ctx context.Context, workspaceID string) ([]Session, error)
	GetByID(ctx context.Context, id string) (Session, error)
	UpdateTitle(ctx context.Context, id, title string) error
	UpdateStatus(ctx context.Context, id, status string) error
	BindWorkspace(ctx context.Context, id, workspaceID string) error
	SetClaudeSessionID(ctx context.Context, id string, claudeID *string) error
	Touch(ctx context.Context, id string, ts int64) error
	SearchByTitle(ctx context.Context, query string) ([]Session, error)
	SearchByContent(ctx context.Context, query string, limit int) ([]SearchHitRow, error)
}

// AgentStore persists and retrieves Agent rows. Rows are scoped per
// workspace; the same PresetID may exist in many workspaces (each with a
// distinct row id of the form "{workspaceID}/{presetID}").
type AgentStore interface {
	Create(ctx context.Context, agent Agent) (Agent, error)
	Update(ctx context.Context, agent Agent) error
	Delete(ctx context.Context, id string) error
	GetByID(ctx context.Context, id string) (Agent, error)
	ListByWorkspace(ctx context.Context, workspaceID string) ([]Agent, error)
	GetDefaultForWorkspace(ctx context.Context, workspaceID string) (Agent, error)
	// EnsureDefaultForWorkspace creates the Main Agent default when the
	// workspace has none; otherwise it returns the existing default.
	EnsureDefaultForWorkspace(ctx context.Context, workspaceID string) (Agent, error)
	// SeedPresets inserts the 9 expert presets scoped to workspaceID,
	// skipping rows whose (workspace_id, preset_id) pair already exists.
	SeedPresets(ctx context.Context, workspaceID string) error
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
