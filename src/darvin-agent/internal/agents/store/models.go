// GORM row models for sessions, messages, usage, and imported files.

package store

import "time"

// Session is the GORM row representation of a session.Session. Messages
// are not stored here — see Message below. Title / ClaudeSessionID are
// renderer-facing metadata owned by the RPC handlers, NOT by the agent's
// session.Session domain model; SQLiteStore.Save preserves them from the
// existing row.
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

func (Session) TableName() string { return "sessions" }

// Message is one persisted LLM turn.
type Message struct {
	ID         string `gorm:"primaryKey"`
	SessionID  string `gorm:"index"`
	Role       string `gorm:"index"`
	Content    string `gorm:"type:text"`
	ToolCalls  string `gorm:"type:text"`
	Timestamp  int64  `gorm:"index"`
	StopReason string `gorm:"default:stop"`
	ParentID   string `gorm:"index"`
	// Done / Error / ToolLabel are the renderer-facing sealing fields.
	Done      bool `gorm:"default:false"`
	Error     *string
	ToolLabel *string
}

func (Message) TableName() string { return "messages" }

// SessionDigest records one compact() pass. The latest sequence row
// per session is the current compaction checkpoint.
type SessionDigest struct {
	ID                 string `gorm:"primaryKey"`
	SessionID          string `gorm:"index;uniqueIndex:idx_session_sequence"`
	Sequence           int    `gorm:"not null;uniqueIndex:idx_session_sequence"`
	Summary            string `gorm:"type:text"`
	TokensBefore       int    `gorm:"not null"`
	TokensAfter        int    `gorm:"not null"`
	FirstKeptID        string
	FirstKeptTimestamp int64
	CompactReason      string `gorm:"not null"`
	SourceCompactID    string `gorm:"not null"`
	CreatedAt          int64  `gorm:"not null"`
}

func (SessionDigest) TableName() string { return "session_digests" }

// SkillSnapshot is a row written each time a Skill is materialised into
// a session's prompt.
type SkillSnapshot struct {
	ID        string    `gorm:"primaryKey"`
	SessionID string    `gorm:"index"`
	SkillName string    `gorm:"not null"`
	LoadedAt  time.Time `gorm:"autoCreateTime"`
	Source    string    `gorm:"not null"`
}

func (SkillSnapshot) TableName() string { return "skill_snapshots" }

// AppState holds a small key/value store for process-scoped state that
// must survive restarts.
type AppState struct {
	Key   string `gorm:"primaryKey"`
	Value string `gorm:"type:text"`
}

func (AppState) TableName() string { return "app_state" }

// ImportedFile is one file the user imported into a session's workspace.
// Tracks only the workspace-relative path so the agent can address the
// file but never learns where it came from.
type ImportedFile struct {
	ID           string `gorm:"primaryKey"`
	SessionID    string `gorm:"index;not null;uniqueIndex:idx_session_relpath"`
	OriginalName string `gorm:"not null"`
	RelativePath string `gorm:"not null;uniqueIndex:idx_session_relpath"`
	Size         int64  `gorm:"not null"`
	MimeType     *string
	Sha256       string    `gorm:"not null"`
	ImportedAt   time.Time `gorm:"autoCreateTime"`
}

func (ImportedFile) TableName() string { return "imported_files" }

// SessionUsage is the per-session usage snapshot persisted across restarts.
// One row per session_id; PRIMARY KEY makes upsert a single Save call.
type SessionUsage struct {
	SessionID         string `gorm:"primaryKey;column:session_id"`
	LastUsedTokens    int    `gorm:"column:last_used_tokens"`
	LastPromptTokens  int    `gorm:"column:last_prompt_tokens"`
	LastCompletion    int    `gorm:"column:last_completion_tokens"`
	LastCacheRead     int    `gorm:"column:last_cache_read_tokens"`
	LastCacheWrite    int    `gorm:"column:last_cache_write_tokens"`
	LastCacheWrite1h  int    `gorm:"column:last_cache_write_1h_tokens"`
	LastContextTokens int    `gorm:"column:last_context_tokens"`
	LastPercent       int    `gorm:"column:last_percent"`
	LastModel         string `gorm:"column:last_model"`
	RequestCount      int    `gorm:"column:request_count"`
	TotalPromptTokens int    `gorm:"column:total_prompt_tokens"`
	TotalCompletion   int    `gorm:"column:total_completion_tokens"`
	TotalCacheRead    int    `gorm:"column:total_cache_read_tokens"`
	// SnapshotAt is unix ms; renamed from UpdatedAt because GORM
	// auto-detects UpdatedAt and overwrites it at Save time.
	SnapshotAt int64 `gorm:"column:updated_at"`
}

func (SessionUsage) TableName() string { return "session_usages" }

// Subagent is one sub-agent run spawned by a parent session via the
// delegate_subagent / parallel_subagents tools. ID is namespaced
// "<parentSessionID>/sub/<rand>"; ParentID is the parent session id and
// is indexed for ListByParent queries. The full final assistant text is
// kept in ResultText up to a truncation threshold; FullResultPath is
// reserved for an off-by-default file dump and stays empty in this
// implementation.
type Subagent struct {
	ID             string `gorm:"primaryKey"`
	ParentID       string `gorm:"index"`
	Status         string `gorm:"default:'pending'"`
	Prompt         string
	Description    string
	ScopeJSON      string
	Model          string
	ToolCallID     string    `gorm:"index"`
	StartedAt      time.Time `gorm:"autoCreateTime"`
	EndedAt        time.Time
	ResultText     string `gorm:"type:text"`
	FullResultPath string `gorm:"type:text"`
	ToolCalls      int
	ErrorMsg       string `gorm:"type:text"`
	Depth          int    `gorm:"default:0"`
	TimeoutMs      int    `gorm:"default:0"`
}

func (Subagent) TableName() string { return "subagent_runs" }
