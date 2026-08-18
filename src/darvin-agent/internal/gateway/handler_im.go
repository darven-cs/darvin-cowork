// JSON-RPC handlers for the IM-channel subsystem. The dispatcher
// delegates agent.im.<op> here; each handler unpacks params, delegates to
// im.Handlers, and wraps the result in a JSON-RPC Response.

package gateway

import (
	"context"
	"encoding/json"
)

// handleIMList returns every IM instance plus its status.
func handleIMList(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.IMHandlers == nil {
		return errorResp(id, CodeInternalError, "im handlers not wired", nil)
	}
	out, err := h.IMHandlers.HandleIMList(ctx, params)
	if err != nil {
		return errorResp(id, CodeInternalError, "im list", err)
	}
	return successResp(id, out)
}

// handleIMGet returns one IM instance.
func handleIMGet(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.IMHandlers == nil {
		return errorResp(id, CodeInternalError, "im handlers not wired", nil)
	}
	out, err := h.IMHandlers.HandleIMGet(ctx, params)
	if err != nil {
		return errorResp(id, CodeInvalidParams, "im get", err)
	}
	return successResp(id, out)
}

// handleIMCreate inserts a new instance.
func handleIMCreate(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.IMHandlers == nil {
		return errorResp(id, CodeInternalError, "im handlers not wired", nil)
	}
	out, err := h.IMHandlers.HandleIMCreate(ctx, params)
	if err != nil {
		return errorResp(id, CodeInvalidParams, "im create", err)
	}
	return successResp(id, out)
}

// handleIMUpdate applies a config patch.
func handleIMUpdate(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.IMHandlers == nil {
		return errorResp(id, CodeInternalError, "im handlers not wired", nil)
	}
	out, err := h.IMHandlers.HandleIMUpdate(ctx, params)
	if err != nil {
		return errorResp(id, CodeInvalidParams, "im update", err)
	}
	return successResp(id, out)
}

// handleIMDelete removes an instance.
func handleIMDelete(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.IMHandlers == nil {
		return errorResp(id, CodeInternalError, "im handlers not wired", nil)
	}
	out, err := h.IMHandlers.HandleIMDelete(ctx, params)
	if err != nil {
		return errorResp(id, CodeInternalError, "im delete", err)
	}
	return successResp(id, out)
}

// handleIMSetEnabled flips an instance enabled/disabled.
func handleIMSetEnabled(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.IMHandlers == nil {
		return errorResp(id, CodeInternalError, "im handlers not wired", nil)
	}
	out, err := h.IMHandlers.HandleIMSetEnabled(ctx, params)
	if err != nil {
		return errorResp(id, CodeInternalError, "im set_enabled", err)
	}
	return successResp(id, out)
}

// handleIMTest probes connectivity for a candidate config.
func handleIMTest(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.IMHandlers == nil {
		return errorResp(id, CodeInternalError, "im handlers not wired", nil)
	}
	out, err := h.IMHandlers.HandleIMTest(ctx, params)
	if err != nil {
		return errorResp(id, CodeInternalError, "im test", err)
	}
	return successResp(id, out)
}

// handleIMLoginStart begins a QR login session.
func handleIMLoginStart(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.IMHandlers == nil {
		return errorResp(id, CodeInternalError, "im handlers not wired", nil)
	}
	out, err := h.IMHandlers.HandleIMLoginStart(ctx, params)
	if err != nil {
		return errorResp(id, CodeInternalError, "im login_start", err)
	}
	return successResp(id, out)
}

// handleIMLoginPoll advances a QR login session.
func handleIMLoginPoll(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.IMHandlers == nil {
		return errorResp(id, CodeInternalError, "im handlers not wired", nil)
	}
	out, err := h.IMHandlers.HandleIMLoginPoll(ctx, params)
	if err != nil {
		return errorResp(id, CodeInternalError, "im login_poll", err)
	}
	return successResp(id, out)
}
