package gateway

import (
	"context"
	"encoding/json"
	"strings"

	"darvin-cowork/backend/internal/acp"
)

// PromptParams is the JSON-RPC params for agent.prompt. sessionId is
// optional; when omitted the gateway allocates the default session.
type PromptParams struct {
	Content   string `json:"content"`
	SessionID string `json:"sessionId,omitempty"`
}

// PromptResult is the JSON-RPC result for agent.prompt. sessionId and
// messageId are 21-char nanoids with no prefix (the spec rejected the
// s-/m- scheme because the WS frame is opaque to the renderer).
type PromptResult struct {
	SessionID string `json:"sessionId"`
	MessageID string `json:"messageId"`
}

// AbortParams is the JSON-RPC params for agent.abort.
type AbortParams struct {
	SessionID string `json:"sessionId"`
}

// AbortResult is the JSON-RPC result for agent.abort.
type AbortResult struct {
	Aborted   bool   `json:"aborted"`
	SessionID string `json:"sessionId"`
}

// SubscribeEventsParams is the JSON-RPC params for agent.subscribe_events.
// sessionId must reference an existing session (i.e. one created earlier
// on the same WS connection); unknown ids are rejected with -32602.
type SubscribeEventsParams struct {
	SessionID string `json:"sessionId"`
}

// SubscribeEventsResult is the JSON-RPC result for agent.subscribe_events.
type SubscribeEventsResult struct {
	Subscribed bool `json:"subscribed"`
}

// SteerParams is the JSON-RPC params for agent.steer. v0 only exposes
// Steer (Redirect is reserved for a future spec).
type SteerParams struct {
	Content string `json:"content"`
}

// SteerResult is the JSON-RPC result for agent.steer.
type SteerResult struct {
	Steered bool `json:"steered"`
}

// Handler bundles the shared dependencies the JSON-RPC dispatch layer
// needs. Each per-connection *client carries a reference alongside its
// own write state; the read loop pulls handler into dispatchRequest.
type Handler struct {
	Sessions *SessionManager
	Ledger   *EventLedger
	Loop     *acp.Loop
	Steer    acp.SteerControl
}

// NewHandler wires the four dependencies. main.go passes the singleton
// SessionManager / EventLedger / acp.Loop / acp.SteerControl instances.
func NewHandler(s *SessionManager, l *EventLedger, loop *acp.Loop, steer acp.SteerControl) *Handler {
	return &Handler{Sessions: s, Ledger: l, Loop: loop, Steer: steer}
}

// dispatchRequest routes one parsed JSON-RPC request to the matching
// handler. Nil return signals "notification — do not reply"; v0 accepts
// no notifications and the switch always returns a value.
func dispatchRequest(ctx context.Context, req *Request, c *client, h *Handler) *Response {
	switch req.Method {
	case "agent.prompt":
		return handlePrompt(ctx, req.ID, req.Params, c, h)
	case "agent.abort":
		return handleAbort(ctx, req.ID, req.Params, c, h)
	case "agent.subscribe_events":
		return handleSubscribeEvents(ctx, req.ID, req.Params, c, h)
	case "agent.steer":
		return handleSteer(ctx, req.ID, req.Params, c, h)
	default:
		return errorResp(req.ID, CodeMethodNotFound,
			"Method not found: "+req.Method, nil)
	}
}

func handlePrompt(_ context.Context, id json.RawMessage, params json.RawMessage, c *client, h *Handler) *Response {
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	var p PromptParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if strings.TrimSpace(p.Content) == "" {
		return errorResp(id, CodeInvalidParams, "content is required", nil)
	}

	// Single-session guard: only the default session may drive the Agent,
	// because the Agent is constructed against exactly one session and the
	// ledger routes events by that session's id. Empty SessionID resolves
	// to the default inside CreateOrGet; any other value is rejected
	// before we touch the Loop.
	if p.SessionID != "" && p.SessionID != c.sessions.DefaultID() {
		return errorResp(id, CodeInvalidParams, "session not active", nil)
	}

	sess, _, _ := c.sessions.CreateOrGet(p.SessionID)
	// The messageId CreateOrGet returns is discarded: Loop.Prompt mints the
	// id that the run's events actually carry, and the renderer correlates
	// notifications against that one.
	msgID, err := h.Loop.Prompt(context.Background(), p.Content)
	if err != nil {
		return errorResp(id, CodeInternalError, "loop prompt", err)
	}
	return successResp(id, PromptResult{SessionID: sess.ID, MessageID: msgID})
}

func handleAbort(ctx context.Context, id json.RawMessage, params json.RawMessage, _ *client, h *Handler) *Response {
	var p AbortParams
	if len(params) > 0 {
		_ = json.Unmarshal(params, &p)
	}
	if err := h.Loop.Abort(ctx); err != nil {
		return errorResp(id, CodeInternalError, "loop abort", err)
	}
	return successResp(id, AbortResult{Aborted: true, SessionID: p.SessionID})
}

func handleSubscribeEvents(_ context.Context, id json.RawMessage, params json.RawMessage, c *client, _ *Handler) *Response {
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	var p SubscribeEventsParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if p.SessionID == "" {
		return errorResp(id, CodeInvalidParams, "sessionId is required", nil)
	}
	if !c.sessions.Has(p.SessionID) {
		return errorResp(id, CodeInvalidParams, "unknown sessionId", nil)
	}
	c.ledger.Subscribe(p.SessionID, c)
	return successResp(id, SubscribeEventsResult{Subscribed: true})
}

func handleSteer(ctx context.Context, id json.RawMessage, params json.RawMessage, _ *client, h *Handler) *Response {
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	var p SteerParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if strings.TrimSpace(p.Content) == "" {
		return errorResp(id, CodeInvalidParams, "content is required", nil)
	}
	if err := h.Steer.Steer(ctx, p.Content); err != nil {
		return errorResp(id, CodeInternalError, "loop steer", err)
	}
	return successResp(id, SteerResult{Steered: true})
}
