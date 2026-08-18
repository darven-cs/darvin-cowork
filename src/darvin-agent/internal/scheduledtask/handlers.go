// JSON-RPC handler layer for the scheduled-task subsystem. Backed by
// Engine + ScheduleStore; the gateway router dispatches
// agent.schedule.<op> here.

package scheduledtask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agents/store"
)

// Handlers wraps Engine + Store for the JSON-RPC dispatch layer.
type Handlers struct {
	engine *Engine
	store  *store.ScheduleStore
	log    *zap.Logger
}

// NewHandlers builds a Handlers bundle. Engine may be nil; the engine-
// backed ops (run_now / abort) then return an explicit "not running" error.
func NewHandlers(engine *Engine, s *store.ScheduleStore, log *zap.Logger) *Handlers {
	return &Handlers{engine: engine, store: s, log: log}
}

// ListParams is the wire shape for agent.schedule.list.
type ListParams struct {
	WorkspaceID string `json:"workspaceId"`
}

// GetParams is the wire shape for agent.schedule.get.
type GetParams struct {
	WorkspaceID string `json:"workspaceId"`
	ScheduleID  string `json:"scheduleId"`
}

// CreateParams is the wire shape for agent.schedule.create.
type CreateParams struct {
	WorkspaceID string                  `json:"workspaceId"`
	Schedule    ScheduleCreateWire      `json:"schedule"`
}

// ScheduleCreateWire is the wire shape for one inbound schedule payload.
type ScheduleCreateWire struct {
	AgentID      *string          `json:"agentId,omitempty"`
	Name         string           `json:"name"`
	Enabled      *bool            `json:"enabled,omitempty"`
	Kind         string           `json:"kind"`
	ScheduleBody store.ScheduleBody `json:"schedule"`
	Prompt       string           `json:"prompt"`
	SessionTitle *string          `json:"sessionTitle,omitempty"`
}

// UpdateParams is the wire shape for agent.schedule.update (patch mode).
type UpdateParams struct {
	WorkspaceID string                 `json:"workspaceId"`
	ScheduleID  string                 `json:"scheduleId"`
	Patch       map[string]any         `json:"patch"`
}

// DeleteParams is the wire shape for agent.schedule.delete.
type DeleteParams struct {
	WorkspaceID string `json:"workspaceId"`
	ScheduleID  string `json:"scheduleId"`
}

// ToggleParams is the wire shape for agent.schedule.toggle.
type ToggleParams struct {
	WorkspaceID string `json:"workspaceId"`
	ScheduleID  string `json:"scheduleId"`
	Enabled     bool   `json:"enabled"`
}

// RunNowParams is the wire shape for agent.schedule.run_now.
type RunNowParams struct {
	WorkspaceID string `json:"workspaceId"`
	ScheduleID  string `json:"scheduleId"`
}

// AbortParams is the wire shape for agent.schedule.abort.
type AbortParams struct {
	WorkspaceID string `json:"workspaceId"`
	ScheduleID  string `json:"scheduleId"`
	RunID       string `json:"runId"`
}

// ListRunsParams is the wire shape for agent.schedule.list_runs.
type ListRunsParams struct {
	WorkspaceID string `json:"workspaceId"`
	ScheduleID  string `json:"scheduleId"`
	Limit       int    `json:"limit,omitempty"`
	Offset      int    `json:"offset,omitempty"`
}

// ListAllRunsParams is the wire shape for agent.schedule.list_all_runs.
type ListAllRunsParams struct {
	WorkspaceID string `json:"workspaceId"`
	Limit       int    `json:"limit,omitempty"`
	Offset      int    `json:"offset,omitempty"`
}

// HandleScheduleList returns every schedule for the workspace.
func (h *Handlers) HandleScheduleList(ctx context.Context, params json.RawMessage) (any, error) {
	var p ListParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	rows, err := h.store.ListByWorkspace(ctx, p.WorkspaceID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"schedules": toSchedules(rows)}, nil
}

// HandleScheduleGet returns one schedule.
func (h *Handlers) HandleScheduleGet(ctx context.Context, params json.RawMessage) (any, error) {
	var p GetParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	row, err := h.store.Get(ctx, p.ScheduleID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"schedule": toSchedule(row)}, nil
}

// HandleScheduleCreate validates the inbound shape and inserts the row.
func (h *Handlers) HandleScheduleCreate(ctx context.Context, params json.RawMessage) (any, error) {
	var p CreateParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Schedule.Name == "" || p.Schedule.Prompt == "" {
		return nil, errors.New("name and prompt are required")
	}
	if err := validateScheduleBody(p.Schedule.ScheduleBody); err != nil {
		return nil, err
	}
	body, err := store.EncodeScheduleBody(p.Schedule.ScheduleBody)
	if err != nil {
		return nil, err
	}
	enabled := true
	if p.Schedule.Enabled != nil {
		enabled = *p.Schedule.Enabled
	}
	row := &store.Schedule{
		ID:           newScheduleID(),
		WorkspaceID:  p.WorkspaceID,
		AgentID:      p.Schedule.AgentID,
		Name:         p.Schedule.Name,
		Enabled:      enabled,
		Kind:         p.Schedule.Kind,
		ScheduleJSON: body,
		Prompt:       p.Schedule.Prompt,
		SessionTitle: p.Schedule.SessionTitle,
		CreatedAt:    time.Now().UnixMilli(),
		UpdatedAt:    time.Now().UnixMilli(),
	}
	next, err := computeNextFire(p.Schedule.ScheduleBody, time.Now())
	if err != nil {
		return nil, err
	}
	nextMs := next.UnixMilli()
	row.NextFireAt = &nextMs
	if err := h.store.Create(ctx, row); err != nil {
		return nil, err
	}
	return map[string]any{"schedule": toSchedule(*row)}, nil
}

// HandleScheduleUpdate applies a partial patch.
func (h *Handlers) HandleScheduleUpdate(ctx context.Context, params json.RawMessage) (any, error) {
	var p UpdateParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	existing, err := h.store.Get(ctx, p.ScheduleID)
	if err != nil {
		return nil, err
	}
	body, err := store.DecodeScheduleBody(existing.ScheduleJSON)
	if err != nil {
		return nil, err
	}
	if rawBody, ok := p.Patch["schedule"]; ok {
		newBody, ok := rawBody.(map[string]any)
		if !ok {
			return nil, errors.New("schedule must be an object")
		}
		body = mergeBody(body, newBody)
		if err := validateScheduleBody(body); err != nil {
			return nil, err
		}
		encoded, err := store.EncodeScheduleBody(body)
		if err != nil {
			return nil, err
		}
		p.Patch["schedule_json"] = encoded
		p.Patch["kind"] = body.Kind
	}
	if _, ok := p.Patch["schedule"]; ok {
		next, err := computeNextFire(body, time.Now())
		if err != nil {
			return nil, err
		}
		nextMs := next.UnixMilli()
		p.Patch["next_fire_at"] = nextMs
	}
	p.Patch["updated_at"] = time.Now().UnixMilli()
	if err := h.store.Update(ctx, p.ScheduleID, p.Patch); err != nil {
		return nil, err
	}
	row, err := h.store.Get(ctx, p.ScheduleID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"schedule": toSchedule(row)}, nil
}

// HandleScheduleDelete removes a schedule.
func (h *Handlers) HandleScheduleDelete(ctx context.Context, params json.RawMessage) (any, error) {
	var p DeleteParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if err := h.store.Delete(ctx, p.ScheduleID); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}

// HandleScheduleToggle flips enabled.
func (h *Handlers) HandleScheduleToggle(ctx context.Context, params json.RawMessage) (any, error) {
	var p ToggleParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	row, err := h.store.Toggle(ctx, p.ScheduleID, p.Enabled)
	if err != nil {
		return nil, err
	}
	return map[string]any{"schedule": toSchedule(row)}, nil
}

// HandleScheduleRunNow fires a schedule immediately.
func (h *Handlers) HandleScheduleRunNow(ctx context.Context, params json.RawMessage) (any, error) {
	if h.engine == nil {
		return nil, errors.New("scheduler engine not running")
	}
	var p RunNowParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	run, err := h.engine.TriggerNow(ctx, p.ScheduleID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"run": toRun(run)}, nil
}

// HandleScheduleAbort cancels an in-flight run.
func (h *Handlers) HandleScheduleAbort(ctx context.Context, params json.RawMessage) (any, error) {
	if h.engine == nil {
		return nil, errors.New("scheduler engine not running")
	}
	var p AbortParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	aborted, err := h.engine.Abort(ctx, p.ScheduleID, p.RunID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"aborted": aborted}, nil
}

// HandleScheduleListRuns returns run history for one schedule.
func (h *Handlers) HandleScheduleListRuns(ctx context.Context, params json.RawMessage) (any, error) {
	var p ListRunsParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	rows, err := h.store.ListRunsBySchedule(ctx, p.ScheduleID, p.Limit, p.Offset)
	if err != nil {
		return nil, err
	}
	return map[string]any{"runs": toRuns(rows)}, nil
}

// HandleScheduleListAllRuns returns run history across the workspace.
func (h *Handlers) HandleScheduleListAllRuns(ctx context.Context, params json.RawMessage) (any, error) {
	var p ListAllRunsParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	rows, err := h.store.ListAllRuns(ctx, p.WorkspaceID, p.Limit, p.Offset)
	if err != nil {
		return nil, err
	}
	return map[string]any{"runs": toRuns(rows)}, nil
}

func validateScheduleBody(b store.ScheduleBody) error {
	switch b.Kind {
	case "at":
		if b.At == "" {
			return errors.New("at: required")
		}
		if _, err := time.Parse(time.RFC3339, b.At); err != nil {
			return fmt.Errorf("at: %w", err)
		}
	case "every":
		if b.EveryMs <= 0 {
			return errors.New("everyMs must be > 0")
		}
	case "cron":
		if b.Expr == "" {
			return errors.New("cron: expr required")
		}
		if _, err := ParseCronExpr(b.Expr); err != nil {
			return fmt.Errorf("cron: %w", err)
		}
	default:
		return fmt.Errorf("unknown kind %q", b.Kind)
	}
	return nil
}

// mergeBody overlays patch's body-shaped fields onto base. Patch keys
// mirror ScheduleBody field names so the handler can route them via a
// generic map without losing the typed merge.
func mergeBody(base store.ScheduleBody, patch map[string]any) store.ScheduleBody {
	if v, ok := patch["kind"].(string); ok {
		base.Kind = v
	}
	if v, ok := patch["at"].(string); ok {
		base.At = v
	}
	if v, ok := patch["everyMs"].(float64); ok {
		base.EveryMs = int64(v)
	}
	if v, ok := patch["anchorMs"].(float64); ok {
		ms := int64(v)
		base.AnchorMs = &ms
	}
	if v, ok := patch["expr"].(string); ok {
		base.Expr = v
	}
	if v, ok := patch["tz"].(string); ok {
		base.TZ = v
	}
	return base
}

// toSchedule converts a GORM row to the wire shape renderer consumes.
func toSchedule(row store.Schedule) map[string]any {
	out := map[string]any{
		"id":                row.ID,
		"workspaceId":       row.WorkspaceID,
		"agentId":           row.AgentID,
		"name":              row.Name,
		"enabled":           row.Enabled,
		"kind":              row.Kind,
		"prompt":            row.Prompt,
		"sessionTitle":      row.SessionTitle,
		"createdAt":         row.CreatedAt,
		"updatedAt":         row.UpdatedAt,
		"lastFiredAt":       row.LastFiredAt,
		"nextFireAt":        row.NextFireAt,
		"consecutiveErrors": row.ConsecutiveErrors,
	}
	if body, err := store.DecodeScheduleBody(row.ScheduleJSON); err == nil {
		out["schedule"] = map[string]any{
			"kind":     body.Kind,
			"at":       body.At,
			"everyMs":  body.EveryMs,
			"anchorMs": body.AnchorMs,
			"expr":     body.Expr,
			"tz":       body.TZ,
		}
	}
	return out
}

func toSchedules(rows []store.Schedule) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, toSchedule(r))
	}
	return out
}

func toRun(row store.ScheduleRun) map[string]any {
	return map[string]any{
		"id":          row.ID,
		"scheduleId":  row.ScheduleID,
		"triggeredAt": row.TriggeredAt,
		"trigger":     row.TriggerKind,
		"sessionId":   row.SessionID,
		"runId":       row.RunID,
		"startedAt":   row.StartedAt,
		"endedAt":     row.EndedAt,
		"status":      row.Status,
		"error":       row.Error,
		"attempts":    row.Attempts,
	}
}

func toRuns(rows []store.ScheduleRun) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, toRun(r))
	}
	return out
}