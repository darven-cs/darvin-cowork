package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"darvin-cowork/backend/internal/acp"
	"darvin-cowork/backend/internal/agent/store"
)

// PromptParams is the JSON-RPC params for agent.prompt. sessionId is
// optional; when omitted the gateway allocates the default session.
// runId is optional; when omitted the gateway mints one so the result
// always carries a non-empty correlation token.
type PromptParams struct {
	Content   string `json:"content"`
	SessionID string `json:"sessionId,omitempty"`
	RunID     string `json:"runId,omitempty"`
}

// PromptResult is the JSON-RPC result for agent.prompt. sessionId and
// messageId are 21-char nanoids with no prefix (the spec rejected the
// s-/m- scheme because the WS frame is opaque to the renderer).
// Queued=true 表示该 turn 落在了同 session 的 followUpQueue,要等上一
// 条完成才会真正起跑。
type PromptResult struct {
	SessionID string `json:"sessionId"`
	RunID     string `json:"runId"`
	MessageID string `json:"messageId"`
	Queued    bool   `json:"queued,omitempty"`
}

// AbortParams is the JSON-RPC params for agent.abort. Both sessionId and
// runId identify the target turn; an unknown pair returns -32602.
type AbortParams struct {
	SessionID string `json:"sessionId"`
	RunID     string `json:"runId"`
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
//
// Loop 不再挂在 Handler 上 —— prompt 路径按 sessionID 路由到对应 entry 的
// per-session Loop(见 handlePrompt)。Steer 仍接全局 Agent(本期不迁,见
// spec §1.3 非目标)。
type Handler struct {
	Sessions     *SessionManager
	Ledger       *EventLedger
	Steer        acp.SteerControl
	SessionStore store.SessionStore
	MessageStore store.MessageStore
}

// NewHandler wires the dependencies. main.go 注入 SessionManager /
// EventLedger / SteerControl 与两个 store;Loop 不再入参。
func NewHandler(
	s *SessionManager,
	l *EventLedger,
	steer acp.SteerControl,
	sessStore store.SessionStore,
	msgStore store.MessageStore,
) *Handler {
	return &Handler{
		Sessions:     s,
		Ledger:       l,
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

// handlePrompt 把 prompt 路由到 sessionID 对应的 AcpSession.Loop。
// ErrSessionStalled(Stop 拒绝窗口)返 CodeSessionStalled;factory
// 构造失败返 CodeAgentInitFailed;handler 测试 stub 场景(entry.Acp
// 为 nil)返 CodeNoAcpSession。空 sessionId 默认走 DefaultSessionID,
// 与旧 CreateOrGet 行为一致。
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
	sessionID := p.SessionID
	if sessionID == "" {
		sessionID = DefaultSessionID
	}

	entry, err := c.sessions.GetOrCreateEntry(sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionStalled) {
			return errorResp(id, CodeSessionStalled, "session stalled", err)
		}
		return errorResp(id, CodeAgentInitFailed, "get session", err)
	}
	if entry.Acp == nil {
		// handler 测试 stub 没注入 factory 时走这里。
		return errorResp(id, CodeNoAcpSession, "no AcpSession bound", nil)
	}
	ticket, err := entry.Acp.Loop.Submit(acp.PromptRequest{RunID: p.RunID, Content: p.Content})
	if err != nil {
		return errorResp(id, CodeInternalError, "loop submit", err)
	}
	return successResp(id, PromptResult{
		SessionID: entry.Session.ID,
		RunID:     ticket.RunID,
		MessageID: ticket.MessageID,
		Queued:    ticket.Queued,
	})
}

func handleAbort(_ context.Context, id json.RawMessage, params json.RawMessage, _ *client, h *Handler) *Response {
	var p AbortParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return errorResp(id, CodeInvalidParams, "params must be an object", nil)
		}
	}
	if p.SessionID == "" || p.RunID == "" {
		return errorResp(id, CodeInvalidParams, "sessionId and runId are required", nil)
	}
	if err := h.Sessions.Stop(p.SessionID, p.RunID); err != nil {
		switch {
		case errors.Is(err, ErrSessionNotFound):
			return errorResp(id, CodeInvalidParams, "unknown sessionId", nil)
		case errors.Is(err, ErrRunMismatch):
			return errorResp(id, CodeInvalidParams, "runId does not match the active run", nil)
		default:
			return errorResp(id, CodeInternalError, "session stop", err)
		}
	}
	return successResp(id, AbortResult{Aborted: true, SessionID: p.SessionID})
}

// handleSubscribeEvents 走两阶段(FR-8):EnsureEntry 只建 SessionEntry
// 不触发 AcpSession 懒建,避免 renderer 订历史 session 时拉起 N 个 Agent /
// Loop / 订阅。真正的事件源在首个 prompt 到达时才补建。
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
	if _, err := c.sessions.EnsureEntry(p.SessionID); err != nil {
		return errorResp(id, CodeInternalError, "subscribe session create", err)
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
