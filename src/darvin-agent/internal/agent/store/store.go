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

	// 以下方法是统一数据库 spec 新增的 RPC-facing 入口。ListAll /
	// GetByID 返回带 Title 的完整 store.Session 行（renderer 需要
	// title 排序 / 显示 / 重命名），与 agent 内部用的 SessionMeta 区分。

	// ListAll returns every session row sorted by updated_at desc,
	// including renderer-facing Title.
	ListAll(ctx context.Context) ([]Session, error)

	// GetByID returns one session row. Returns ErrNotFound when absent.
	GetByID(ctx context.Context, id string) (Session, error)

	// UpdateTitle sets the renderer-facing title. Empty titles are NOT
	// validated here — the handler applies the '新建会话' fallback.
	UpdateTitle(ctx context.Context, id, title string) error

	// UpdateStatus sets the lifecycle status.
	UpdateStatus(ctx context.Context, id, status string) error

	// SetClaudeSessionID stores the optional backend bridge id.
	SetClaudeSessionID(ctx context.Context, id string, claudeID *string) error

	// Touch refreshes updated_at to ts so list ordering advances. The
	// caller passes an explicit ts so renderer-driven switches share the
	// same clock as the handler.
	Touch(ctx context.Context, id string, ts int64) error

	// SearchByTitle returns sessions whose title contains query.
	SearchByTitle(ctx context.Context, query string) ([]Session, error)

	// SearchByContent returns message rows whose content contains query,
	// each carrying the owning session's title, newest first.
	SearchByContent(ctx context.Context, query string, limit int) ([]SearchHitRow, error)
}

// SearchHitRow is one message-content search hit, decorated with the
// owning session's title so the renderer can group results by session.
type SearchHitRow struct {
	Message      Message
	SessionTitle string
}

// searchHitRow is the flat scan target for the SearchByContent JOIN
// (see sqlite_store.go). It mirrors Message's columns plus the joined
// session title so GORM Scan maps raw columns directly.
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
