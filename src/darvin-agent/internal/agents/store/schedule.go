// GORM row models for the scheduled-task subsystem. Schedules live in
// the same sessions.db as Session / Message; the scheduler tick query
// (enabled + next_fire_at) is the only hot path and runs against the
// (enabled, next_fire_at) composite index.

package store

// Schedule is one user-defined timer. kind discriminates the schedule
// payload (at / every / cron); schedule_json carries the kind-specific
// shape so the schema does not grow when new kinds land.
type Schedule struct {
	ID                string  `gorm:"primaryKey"`
	WorkspaceID       string  `gorm:"index;default:''"`
	AgentID           *string
	Name              string
	Enabled           bool   `gorm:"index;default:true"`
	Kind              string // 'at' | 'every' | 'cron'
	ScheduleJSON      string `gorm:"type:text"`
	Prompt            string `gorm:"type:text"`
	SessionTitle      *string
	CreatedAt         int64 `gorm:"not null"`
	UpdatedAt         int64 `gorm:"not null"`
	LastFiredAt       *int64 `gorm:"index"`
	NextFireAt        *int64 `gorm:"index"`
	ConsecutiveErrors int    `gorm:"not null;default:0"`
}

func (Schedule) TableName() string { return "schedules" }

// ScheduleRun is one historical (or in-flight) execution of a Schedule.
// Status moves pending -> running -> done/failed/aborted. Cascade-deleted
// with the parent schedule via the FK.
type ScheduleRun struct {
	ID          string `gorm:"primaryKey"`
	ScheduleID  string `gorm:"index;not null"`
	TriggeredAt int64  `gorm:"not null;index"`
	TriggerKind string // 'scheduled' | 'manual'
	SessionID   *string
	RunID       *string
	StartedAt   *int64
	EndedAt     *int64
	Status      string  `gorm:"not null;default:'pending'"`
	Error       *string `gorm:"type:text"`
	Attempts    int     `gorm:"not null;default:0"`
}

func (ScheduleRun) TableName() string { return "schedule_runs" }