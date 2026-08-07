package store

import "time"

// Session is the GORM row representation of a session.Session. The
// store.SQLiteStore maps between session.Session and this struct on
// every Save / Load. Messages are NOT stored here — see Message below.
//
// Title / ClaudeSessionID are renderer-facing metadata owned by the RPC
// handlers (rename / claude bridge), NOT by the agent's session.Session
// domain model. SQLiteStore.Save preserves them from the existing row so
// a prompt's metadata save never clobbers a user rename.
type Session struct {
	ID              string `gorm:"primaryKey"`
	Key             string `gorm:"index"`
	AgentID         string `gorm:"index"`
	Title           string `gorm:"default:'新建会话'"`
	ClaudeSessionID *string
	Status          string    `gorm:"default:active"`
	CreatedAt       time.Time `gorm:"autoCreateTime"`
	UpdatedAt       time.Time `gorm:"autoUpdateTime"`
}

// TableName pins the SQL table name; GORM's default would be the struct
// name (sessions already pluralises correctly but pinning keeps the name
// stable across Go-side renames).
func (Session) TableName() string { return "sessions" }

// Message is one persisted LLM turn. Writes are owned by the agent loop's
// MessageStore implementation.
type Message struct {
	ID         string `gorm:"primaryKey"`
	SessionID  string `gorm:"index"`
	Role       string `gorm:"index"`
	Content    string `gorm:"type:text"`
	ToolCalls  string `gorm:"type:text"`
	Timestamp  int64  `gorm:"index"`
	StopReason string `gorm:"default:stop"`
	ParentID   string `gorm:"index"`
	// Done / Error / ToolLabel 是 renderer 依赖的"封口"字段：Done 把
	// streaming→done 状态切开、Error 画错误泡、ToolLabel 画工具标签。
	// 由 dispatcher 的 MarkDone / MarkError 和 get_messages 落 / 读。
	Done      bool `gorm:"default:false"`
	Error     *string
	ToolLabel *string
}

func (Message) TableName() string { return "messages" }

// CompactionCheckpoint records one compact() pass — how many tokens
// were saved, the summary text, and the id of the first message
// preserved verbatim after the cut.
type CompactionCheckpoint struct {
	ID           string    `gorm:"primaryKey"`
	SessionID    string    `gorm:"index"`
	Summary      string    `gorm:"type:text"`
	TokensBefore int       `gorm:"not null"`
	TokensAfter  int       `gorm:"not null"`
	FirstKeptID  string    `gorm:"not null"`
	CreatedAt    time.Time `gorm:"autoCreateTime"`
}

func (CompactionCheckpoint) TableName() string { return "compaction_checkpoints" }

// SkillSnapshot is a row written each time a Skill is materialised into
// a session's prompt. The table is created now so AutoMigrate covers it
// ahead of the Skills implementation.
type SkillSnapshot struct {
	ID        string    `gorm:"primaryKey"`
	SessionID string    `gorm:"index"`
	SkillName string    `gorm:"not null"`
	LoadedAt  time.Time `gorm:"autoCreateTime"`
	Source    string    `gorm:"not null"`
}

func (SkillSnapshot) TableName() string { return "skill_snapshots" }

// AppState holds a small key/value store for process-scoped state that
// must survive restarts. Currently only `active_session_id` is written
// (see AppStateStore); the schema is intentionally open for future keys.
type AppState struct {
	Key   string `gorm:"primaryKey"`
	Value string `gorm:"type:text"`
}

func (AppState) TableName() string { return "app_state" }

// ImportedFile is one file the user imported into a session's workspace.
// The row tracks only the workspace-relative path (never the user's source
// path) so the agent can address the file but never learns where it came
// from. RelativePath is unique per session.
type ImportedFile struct {
	ID           string `gorm:"primaryKey"`
	SessionID    string `gorm:"index;not null;uniqueIndex:idx_session_relpath"`
	OriginalName string `gorm:"not null"` // basename only
	RelativePath string `gorm:"not null;uniqueIndex:idx_session_relpath"`
	Size         int64  `gorm:"not null"`
	MimeType     *string
	Sha256       string    `gorm:"not null"`
	ImportedAt   time.Time `gorm:"autoCreateTime"`
}

func (ImportedFile) TableName() string { return "imported_files" }

// SessionUsage is the per-session usage snapshot persisted across restarts.
// One row per session_id; written at every successful LLM turn so the
// renderer can rehydrate contextUsageBySessionId on session switch without
// waiting for a live context_usage event. PRIMARY KEY on session_id makes
// upsert a single Save call and read a single First call — no extra
// index needed.
type SessionUsage struct {
	SessionID         string `gorm:"primaryKey;column:session_id"`
	LastUsedTokens    int    `gorm:"column:last_used_tokens"`
	LastPromptTokens  int    `gorm:"column:last_prompt_tokens"`
	LastCompletion    int    `gorm:"column:last_completion_tokens"`
	LastCacheRead     int    `gorm:"column:last_cache_read_tokens"`
	LastCacheWrite    int    `gorm:"column:last_cache_write_tokens"`
	LastCacheWrite1h  int    `gorm:"column:last_cache_write_1h_tokens"`
	// LastContextTokens is the model context window the most recent turn
	// was sized against. Cached so the renderer hydrates the context ring
	// on session switch without a model-registry lookup.
	LastContextTokens int    `gorm:"column:last_context_tokens"`
	// LastPercent is 0–100 — the rendered fill percent the live
	// context_usage event emitted. Same source-of-truth so the indicator
	// never recomputes a different number from the same data.
	LastPercent       int    `gorm:"column:last_percent"`
	LastModel         string `gorm:"column:last_model"`
	RequestCount      int    `gorm:"column:request_count"`
	TotalPromptTokens int    `gorm:"column:total_prompt_tokens"`
	TotalCompletion   int    `gorm:"column:total_completion_tokens"`
	TotalCacheRead    int    `gorm:"column:total_cache_read_tokens"`
	// SnapshotAt is unix ms the caller chose to write. Renamed away from
	// "UpdatedAt" because GORM auto-detects that name (any type) and
	// overwrites it at Save time, which would defeat the deterministic
	// timestamp the persistence layer wants to persist.
	SnapshotAt int64 `gorm:"column:updated_at"`
}

func (SessionUsage) TableName() string { return "session_usages" }
