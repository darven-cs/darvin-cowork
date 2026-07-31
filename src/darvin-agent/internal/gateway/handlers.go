package gateway

import (
	"context"
	"encoding/json"
	"strings"

	"darvin-cowork/backend/internal/acp"
	"darvin-cowork/backend/internal/agent/store"
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

// SteerParams is the JSON-RPC params for agent.steer. Only Steer is
// currently exposed (Redirect remains reserved).
type SteerParams struct {
	Content string `json:"content"`
}

// SteerResult is the JSON-RPC result for agent.steer.
type SteerResult struct {
	Steered bool `json:"steered"`
}

// ListSessionsResult is the JSON-RPC result for agent.list_sessions.
// Matches DarvinListSessionsResponse in src/shared/darvin-api.ts:
// `sessions: [{id, title, updatedAt}]`.
type ListSessionsResult struct {
	Sessions []SessionSummary `json:"sessions"`
}

// SessionSummary is the wire-shape the renderer consumes. Title is
// generated client-side from the first user message when empty.
type SessionSummary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	UpdatedAt int64  `json:"updatedAt"`
}

// GetMessagesParams is the JSON-RPC params for agent.get_messages.
// sessionId selects which conversation to replay.
type GetMessagesParams struct {
	SessionID string `json:"sessionId"`
	Limit     int    `json:"limit,omitempty"`
	Offset    int    `json:"offset,omitempty"`
}

// GetMessagesResult is the JSON-RPC result for agent.get_messages.
// Matches DarvinGetMessagesResponse in src/shared/darvin-api.ts.
type GetMessagesResult struct {
	Messages []MessageSummary `json:"messages"`
}

// MessageSummary is one persisted turn replayed to the renderer. CreatedAt
// is unix milliseconds; Done is always true for rows loaded from disk
// (the live stream keeps updating in-memory state for the active turn).
type MessageSummary struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	Done      bool   `json:"done"`
	CreatedAt int64  `json:"createdAt"`
	ToolCalls string `json:"toolCalls,omitempty"`
}

// Handler bundles the shared dependencies the JSON-RPC dispatch layer
// needs. Each per-connection *client carries a reference alongside its
// own write state; the read loop pulls handler into dispatchRequest.
type Handler struct {
	Sessions     *SessionManager
	Ledger       *EventLedger
	Loop         *acp.Loop
	Steer        acp.SteerControl
	SessionStore store.SessionStore
	MessageStore store.MessageStore
}

// NewHandler wires the six dependencies. main.go passes the singleton
// SessionManager / EventLedger / acp.Loop / acp.SteerControl instances
// plus the two stores that back list_sessions / get_messages.
func NewHandler(
	s *SessionManager,
	l *EventLedger,
	loop *acp.Loop,
	steer acp.SteerControl,
	sessStore store.SessionStore,
	msgStore store.MessageStore,
) *Handler {
	return &Handler{
		Sessions:     s,
		Ledger:       l,
		Loop:         loop,
		Steer:        steer,
		SessionStore: sessStore,
		MessageStore: msgStore,
	}
}

// dispatchRequest routes one parsed JSON-RPC request to the matching
// handler. Nil return signals "notification — do not reply".
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
	case "agent.list_sessions":
		return handleListSessions(ctx, req.ID, h)
	case "agent.get_messages":
		return handleGetMessages(ctx, req.ID, req.Params, h)
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

// handleListSessions returns every session known to SessionStore,
// newest first (the SessionStore contract sorts by updated_at desc).
// Sessions with no messages are still returned so the renderer's empty
// state has a row to highlight. Title is left empty here — the renderer
// derives a title from the first user message.
func handleListSessions(ctx context.Context, id json.RawMessage, h *Handler) *Response {
	if h.SessionStore == nil {
		return successResp(id, ListSessionsResult{Sessions: []SessionSummary{}})
	}
	rows, err := h.SessionStore.List(ctx)
	if err != nil {
		return errorResp(id, CodeInternalError, "session list", err)
	}
	out := make([]SessionSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, SessionSummary{
			ID:        r.ID,
			Title:     "",
			UpdatedAt: r.UpdatedAt.UnixMilli(),
		})
	}
	return successResp(id, ListSessionsResult{Sessions: out})
}

// handleGetMessages returns the messages for one session. Pagination is
// supported via params.limit / params.offset (default 1000 / 0). An
// unknown sessionId returns an empty list rather than an error so the
// renderer's initial-load path doesn't choke on a stale id.
func handleGetMessages(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	var p GetMessagesParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return errorResp(id, CodeInvalidParams, "params must be an object", nil)
		}
	}
	if p.SessionID == "" {
		return errorResp(id, CodeInvalidParams, "sessionId is required", nil)
	}
	if h.MessageStore == nil {
		return successResp(id, GetMessagesResult{Messages: []MessageSummary{}})
	}
	rows, err := h.MessageStore.List(ctx, p.SessionID, p.Limit, p.Offset)
	if err != nil {
		return errorResp(id, CodeInternalError, "message list", err)
	}
	out := make([]MessageSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, MessageSummary{
			ID:        r.ID,
			SessionID: r.SessionID,
			Role:      r.Role,
			Content:   r.Content,
			Done:      true,
			CreatedAt: r.Timestamp,
			ToolCalls: r.ToolCalls,
		})
	}
	return successResp(id, GetMessagesResult{Messages: out})
}
