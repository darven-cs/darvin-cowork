// JSON-RPC handlers for the scheduled-task subsystem. The dispatcher
// delegates agent.schedule.<op> here; each handler unpacks params,
// delegates to scheduledtask.Handlers, and wraps the result in a
// JSON-RPC Response.

package gateway

import (
	"context"
	"encoding/json"

	"darvin-cowork/backend/internal/scheduledtask"
)

// handleScheduleList returns every schedule for the workspace.
func handleScheduleList(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.ScheduleHandlers == nil {
		return errorResp(id, CodeInternalError, "schedule handlers not wired", nil)
	}
	out, err := h.ScheduleHandlers.HandleScheduleList(ctx, params)
	if err != nil {
		return errorResp(id, CodeInternalError, "schedule list", err)
	}
	return successResp(id, out)
}

// handleScheduleGet returns one schedule.
func handleScheduleGet(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.ScheduleHandlers == nil {
		return errorResp(id, CodeInternalError, "schedule handlers not wired", nil)
	}
	out, err := h.ScheduleHandlers.HandleScheduleGet(ctx, params)
	if err != nil {
		return errorResp(id, CodeInternalError, "schedule get", err)
	}
	return successResp(id, out)
}

// handleScheduleCreate validates the inbound shape and inserts the row.
func handleScheduleCreate(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.ScheduleHandlers == nil {
		return errorResp(id, CodeInternalError, "schedule handlers not wired", nil)
	}
	out, err := h.ScheduleHandlers.HandleScheduleCreate(ctx, params)
	if err != nil {
		return errorResp(id, CodeInvalidParams, "schedule create", err)
	}
	return successResp(id, out)
}

// handleScheduleUpdate applies a partial patch.
func handleScheduleUpdate(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.ScheduleHandlers == nil {
		return errorResp(id, CodeInternalError, "schedule handlers not wired", nil)
	}
	out, err := h.ScheduleHandlers.HandleScheduleUpdate(ctx, params)
	if err != nil {
		return errorResp(id, CodeInvalidParams, "schedule update", err)
	}
	return successResp(id, out)
}

// handleScheduleDelete removes a schedule.
func handleScheduleDelete(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.ScheduleHandlers == nil {
		return errorResp(id, CodeInternalError, "schedule handlers not wired", nil)
	}
	out, err := h.ScheduleHandlers.HandleScheduleDelete(ctx, params)
	if err != nil {
		return errorResp(id, CodeInternalError, "schedule delete", err)
	}
	return successResp(id, out)
}

// handleScheduleToggle flips enabled.
func handleScheduleToggle(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.ScheduleHandlers == nil {
		return errorResp(id, CodeInternalError, "schedule handlers not wired", nil)
	}
	out, err := h.ScheduleHandlers.HandleScheduleToggle(ctx, params)
	if err != nil {
		return errorResp(id, CodeInternalError, "schedule toggle", err)
	}
	return successResp(id, out)
}

// handleScheduleRunNow fires a schedule immediately.
func handleScheduleRunNow(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.ScheduleHandlers == nil {
		return errorResp(id, CodeInternalError, "schedule handlers not wired", nil)
	}
	out, err := h.ScheduleHandlers.HandleScheduleRunNow(ctx, params)
	if err != nil {
		return errorResp(id, CodeInternalError, "schedule run_now", err)
	}
	return successResp(id, out)
}

// handleScheduleAbort cancels an in-flight run.
func handleScheduleAbort(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.ScheduleHandlers == nil {
		return errorResp(id, CodeInternalError, "schedule handlers not wired", nil)
	}
	out, err := h.ScheduleHandlers.HandleScheduleAbort(ctx, params)
	if err != nil {
		return errorResp(id, CodeInternalError, "schedule abort", err)
	}
	return successResp(id, out)
}

// handleScheduleListRuns returns run history for one schedule.
func handleScheduleListRuns(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.ScheduleHandlers == nil {
		return errorResp(id, CodeInternalError, "schedule handlers not wired", nil)
	}
	out, err := h.ScheduleHandlers.HandleScheduleListRuns(ctx, params)
	if err != nil {
		return errorResp(id, CodeInternalError, "schedule list_runs", err)
	}
	return successResp(id, out)
}

// handleScheduleListAllRuns returns run history across the workspace.
func handleScheduleListAllRuns(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.ScheduleHandlers == nil {
		return errorResp(id, CodeInternalError, "schedule handlers not wired", nil)
	}
	out, err := h.ScheduleHandlers.HandleScheduleListAllRuns(ctx, params)
	if err != nil {
		return errorResp(id, CodeInternalError, "schedule list_all_runs", err)
	}
	return successResp(id, out)
}

// compile-time guard that scheduledtask.Handlers is wired through this file.
var _ = (*scheduledtask.Handlers)(nil)