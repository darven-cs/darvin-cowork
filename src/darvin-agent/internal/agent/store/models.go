package store

import "time"

// Session is the GORM row representation of a session.Session. The
// store.SQLiteStore maps between session.Session and this struct on
// every Save / Load. Messages are NOT stored here — see Message below.
type Session struct {
	ID        string    `gorm:"primaryKey"`
	Key       string    `gorm:"index"`
	AgentID   string    `gorm:"index"`
	Status    string    `gorm:"default:active"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

// TableName pins the SQL table name; GORM's default would be the struct
// name (sessions already pluralises correctly but pinning keeps the name
// stable across Go-side renames).
func (Session) TableName() string { return "sessions" }

// Message is one persisted LLM turn. Writes are owned by the ACP loop's
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
