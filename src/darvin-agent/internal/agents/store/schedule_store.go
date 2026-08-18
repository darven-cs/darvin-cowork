// SQLite-backed DAO for Schedule and ScheduleRun. Sharing the *gorm.DB
// with sessions / messages is intentional — see internal/database for the
// singleton rationale.

package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
)

// ScheduleBody is the kind-discriminated payload Schedule.ScheduleJSON
// carries. Mirrored on the Go side instead of imported from
// shared/darvin-api.ts so the scheduler tick stays zero-RPC.
type ScheduleBody struct {
	Kind     string `json:"kind"`
	At       string `json:"at,omitempty"`
	EveryMs  int64  `json:"everyMs,omitempty"`
	AnchorMs *int64 `json:"anchorMs,omitempty"`
	Expr     string `json:"expr,omitempty"`
	TZ       string `json:"tz,omitempty"`
}

// ErrScheduleNotFound is returned by Get / Delete / Toggle when no row
// matches the requested id.
var ErrScheduleNotFound = errors.New("schedule not found")

// ErrScheduleRunNotFound is returned by GetRun when no row matches.
var ErrScheduleRunNotFound = errors.New("schedule run not found")

// ScheduleStore wraps GORM CRUD for Schedule / ScheduleRun.
type ScheduleStore struct {
	db *gorm.DB
}

// NewScheduleStore builds a ScheduleStore against db. Caller owns db
// lifecycle; ScheduleStore does not Close it.
func NewScheduleStore(db *gorm.DB) *ScheduleStore {
	return &ScheduleStore{db: db}
}

// Create inserts a schedule row.
func (s *ScheduleStore) Create(ctx context.Context, sched *Schedule) error {
	return s.db.WithContext(ctx).Create(sched).Error
}

// Get returns one schedule by id; ErrScheduleNotFound when absent.
func (s *ScheduleStore) Get(ctx context.Context, id string) (Schedule, error) {
	var row Schedule
	if err := s.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Schedule{}, ErrScheduleNotFound
		}
		return Schedule{}, err
	}
	return row, nil
}

// ListByWorkspace returns schedules for the workspace, ordered by
// next_fire_at asc so the renderer can render soonest-first.
func (s *ScheduleStore) ListByWorkspace(ctx context.Context, workspaceID string) ([]Schedule, error) {
	var rows []Schedule
	if err := s.db.WithContext(ctx).
		Where("workspace_id = ?", workspaceID).
		Order("next_fire_at asc").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// Update applies a partial patch keyed by column name.
func (s *ScheduleStore) Update(ctx context.Context, id string, patch map[string]any) error {
	res := s.db.WithContext(ctx).
		Model(&Schedule{}).
		Where("id = ?", id).
		Updates(patch)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrScheduleNotFound
	}
	return nil
}

// Delete removes a schedule; schedule_runs cascade-delete via FK.
func (s *ScheduleStore) Delete(ctx context.Context, id string) error {
	res := s.db.WithContext(ctx).Where("id = ?", id).Delete(&Schedule{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrScheduleNotFound
	}
	return nil
}

// Toggle flips enabled and stamps updated_at; returns the refreshed row.
func (s *ScheduleStore) Toggle(ctx context.Context, id string, enabled bool) (Schedule, error) {
	if err := s.db.WithContext(ctx).
		Model(&Schedule{}).
		Where("id = ?", id).
		Updates(map[string]any{"enabled": enabled, "updated_at": time.Now().UnixMilli()}).
		Error; err != nil {
		return Schedule{}, err
	}
	return s.Get(ctx, id)
}

// SelectDue returns enabled schedules whose next_fire_at <= now. Rows
// without a NextFireAt are excluded; Engine stamps every row's
// NextFireAt before its first tick.
func (s *ScheduleStore) SelectDue(ctx context.Context, now int64) ([]Schedule, error) {
	var rows []Schedule
	if err := s.db.WithContext(ctx).
		Where("enabled = ? AND next_fire_at IS NOT NULL AND next_fire_at <= ?", true, now).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// IncrementConsecutiveErrors bumps the counter and writes the next
// backoff fire time. Caller disables when the count crosses the
// auto-disable threshold.
func (s *ScheduleStore) IncrementConsecutiveErrors(ctx context.Context, id string, nextFireAt int64) error {
	return s.db.WithContext(ctx).
		Model(&Schedule{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"consecutive_errors": gorm.Expr("consecutive_errors + 1"),
			"last_fired_at":      time.Now().UnixMilli(),
			"next_fire_at":       nextFireAt,
			"updated_at":         time.Now().UnixMilli(),
		}).Error
}

// ResetConsecutiveErrors zeroes the counter after a successful run.
func (s *ScheduleStore) ResetConsecutiveErrors(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).
		Model(&Schedule{}).
		Where("id = ?", id).
		Update("consecutive_errors", 0).Error
}

// MarkFired stamps last_fired_at + next_fire_at on a successful trigger.
func (s *ScheduleStore) MarkFired(ctx context.Context, id string, nextFireAt int64) error {
	return s.db.WithContext(ctx).
		Model(&Schedule{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"last_fired_at": time.Now().UnixMilli(),
			"next_fire_at":  nextFireAt,
			"updated_at":    time.Now().UnixMilli(),
		}).Error
}

// CreateRun inserts a new schedule_runs row.
func (s *ScheduleStore) CreateRun(ctx context.Context, run *ScheduleRun) error {
	return s.db.WithContext(ctx).Create(run).Error
}

// GetRun returns one run by id.
func (s *ScheduleStore) GetRun(ctx context.Context, id string) (ScheduleRun, error) {
	var row ScheduleRun
	if err := s.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ScheduleRun{}, ErrScheduleRunNotFound
		}
		return ScheduleRun{}, err
	}
	return row, nil
}

// UpdateRun applies a partial patch.
func (s *ScheduleStore) UpdateRun(ctx context.Context, id string, patch map[string]any) error {
	return s.db.WithContext(ctx).
		Model(&ScheduleRun{}).
		Where("id = ?", id).
		Updates(patch).Error
}

// ListRunningRuns returns every run row still in status='running'.
// Engine reconciles these against the session's active run on each tick.
func (s *ScheduleStore) ListRunningRuns(ctx context.Context) ([]ScheduleRun, error) {
	var rows []ScheduleRun
	if err := s.db.WithContext(ctx).
		Where("status = ?", "running").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ListRunsBySchedule returns runs for one schedule, newest first.
func (s *ScheduleStore) ListRunsBySchedule(ctx context.Context, scheduleID string, limit, offset int) ([]ScheduleRun, error) {
	if limit <= 0 {
		limit = 50
	}
	var rows []ScheduleRun
	if err := s.db.WithContext(ctx).
		Where("schedule_id = ?", scheduleID).
		Order("triggered_at desc").
		Limit(limit).
		Offset(offset).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ListAllRuns returns runs across the workspace, joined through schedules.
func (s *ScheduleStore) ListAllRuns(ctx context.Context, workspaceID string, limit, offset int) ([]ScheduleRun, error) {
	if limit <= 0 {
		limit = 50
	}
	var rows []ScheduleRun
	if err := s.db.WithContext(ctx).
		Table("schedule_runs AS r").
		Joins("JOIN schedules AS s ON s.id = r.schedule_id").
		Where("s.workspace_id = ?", workspaceID).
		Order("r.triggered_at desc").
		Limit(limit).
		Offset(offset).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// EncodeScheduleBody marshals a ScheduleBody for storage.
func EncodeScheduleBody(b ScheduleBody) (string, error) {
	raw, err := json.Marshal(b)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// DecodeScheduleBody unmarshals a Schedule.ScheduleJSON value.
func DecodeScheduleBody(raw string) (ScheduleBody, error) {
	var b ScheduleBody
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		return ScheduleBody{}, err
	}
	return b, nil
}