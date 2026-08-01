package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

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
// `sessions: [{id, title, updatedAt, status, claudeSessionId}]`.
type ListSessionsResult struct {
	Sessions []SessionWire `json:"sessions"`
}

// SessionWire is the renderer-facing session shape. It is the store
// package's Session row projected onto the darvin-api wire contract;
// UpdatedAt is unix milliseconds.
type SessionWire struct {
	ID              string  `json:"id"`
	Title           string  `json:"title"`
	UpdatedAt       int64   `json:"updatedAt"`
	Status          string  `json:"status"`
	ClaudeSessionID *string `json:"claudeSessionId"`
}

// toSessionWire projects a store.Session row onto SessionWire.
func toSessionWire(r store.Session) SessionWire {
	return SessionWire{
		ID:              r.ID,
		Title:           r.Title,
		UpdatedAt:       r.UpdatedAt.UnixMilli(),
		Status:          r.Status,
		ClaudeSessionID: r.ClaudeSessionID,
	}
}

// GetMessagesParams is the JSON-RPC params for agent.get_messages.
// sessionId selects which conversation to replay.
type GetMessagesParams struct {
	SessionID string `json:"sessionId"`
	Limit     int    `json:"limit,omitempty"`
	Offset    int    `json:"offset,omitempty"`
}

// GetMessagesResult is the JSON-RPC result for agent.get_messages.
// Messages are store.MessageRecord, whose JSON tags match the
// renderer's DarvinMessage (id / sessionId / role / content / done /
// error / toolLabel / createdAt).
type GetMessagesResult struct {
	Messages []store.MessageRecord `json:"messages"`
}

// CreateSessionParams is the JSON-RPC params for agent.create_session.
// title is optional; an empty title falls back to the store default.
type CreateSessionParams struct {
	Title string `json:"title,omitempty"`
}

// CreateSessionResult is the JSON-RPC result for agent.create_session.
type CreateSessionResult struct {
	Session SessionWire `json:"session"`
}

// GetActiveSessionResult is the JSON-RPC result for
// agent.get_active_session. sessionId is null when no active session is
// persisted.
type GetActiveSessionResult struct {
	SessionID *string `json:"sessionId"`
}

// SetActiveSessionParams is the JSON-RPC params for
// agent.set_active_session.
type SetActiveSessionParams struct {
	SessionID string `json:"sessionId"`
}

// SetActiveSessionResult is the JSON-RPC result for
// agent.set_active_session.
type SetActiveSessionResult struct {
	SessionID string `json:"sessionId"`
}

// DeleteSessionParams is the JSON-RPC params for agent.delete_session.
type DeleteSessionParams struct {
	SessionID string `json:"sessionId"`
}

// DeleteSessionResult is the JSON-RPC result for agent.delete_session.
// nextActiveSessionId is null when the deleted session was the last one.
type DeleteSessionResult struct {
	Deleted             bool    `json:"deleted"`
	NextActiveSessionID *string `json:"nextActiveSessionId"`
}

// RenameSessionParams is the JSON-RPC params for agent.rename_session.
type RenameSessionParams struct {
	SessionID string `json:"sessionId"`
	Title     string `json:"title"`
}

// RenameSessionResult is the JSON-RPC result for agent.rename_session.
type RenameSessionResult struct {
	Session SessionWire `json:"session"`
}

// SearchSessionsParams is the JSON-RPC params for agent.search_sessions.
type SearchSessionsParams struct {
	Query string `json:"query"`
}

// SearchHitWire is one message-content search hit, decorated with the
// owning session title so the renderer can group results by session.
type SearchHitWire struct {
	Message      store.MessageRecord `json:"message"`
	SessionTitle string              `json:"sessionTitle"`
}

// SearchSessionsResult is the JSON-RPC result for agent.search_sessions:
// sessions whose title matches plus message hits whose content matches.
type SearchSessionsResult struct {
	Sessions []SessionWire   `json:"sessions"`
	Messages []SearchHitWire `json:"messages"`
}

// Handler bundles the shared dependencies the JSON-RPC dispatch layer
// needs. Each per-connection *client carries a reference alongside its
// own write state; the read loop pulls handler into dispatchRequest.
//
// Loop 不再挂在 Handler 上 —— prompt 路径按 sessionID 路由到对应 entry 的
// per-session Loop(见 handlePrompt)。Steer 仍接全局 Agent(本期不迁,见
// spec §1.3 非目标)。
// HandlerOptions carries the optional workspace / imported-file wiring.
// Kept separate from the required constructor args so existing call sites
// (and handler tests) do not need to change.
type HandlerOptions struct {
	ImportedFiles *store.ImportedFileStore
	WorkspaceRoot string
}

type Handler struct {
	Sessions     *SessionManager
	Ledger       *EventLedger
	Steer        acp.SteerControl
	SessionStore store.SessionStore
	MessageStore store.MessageStore
	// AppState 承载 active_session_id 持久化(get/set_active_session 与
	// create_session / delete_session 的 active 推进)。nil 时 get_active
	// 返 null、set/create 只做内存侧行为。
	AppState *store.AppStateStore
	// ImportedFiles 支撑 agent.import_files / list_imported_files /
	// remove_imported_file / get_workspace_info。nil 时这些 handler 返回
	// 空结果,便于 handler 测试 stub。
	ImportedFiles *store.ImportedFileStore
	// WorkspaceRoot 是 agent 的沙箱根(env DARVIN_AGENT_WORKSPACE),
	// import_files 用它做 sourcePaths 的 containment check。
	WorkspaceRoot string
}

// NewHandler wires the dependencies. main.go 注入 SessionManager /
// EventLedger / SteerControl 与两个 store 及 AppStateStore;Loop 不再
// 入参。opts 可选携带 ImportedFiles / WorkspaceRoot。
func NewHandler(
	s *SessionManager,
	l *EventLedger,
	steer acp.SteerControl,
	sessStore store.SessionStore,
	msgStore store.MessageStore,
	appState *store.AppStateStore,
	opts ...HandlerOptions,
) *Handler {
	var o HandlerOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	return &Handler{
		Sessions:      s,
		Ledger:        l,
		Steer:         steer,
		SessionStore:  sessStore,
		MessageStore:  msgStore,
		AppState:      appState,
		ImportedFiles: o.ImportedFiles,
		WorkspaceRoot: o.WorkspaceRoot,
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
	case "agent.create_session":
		return handleCreateSession(ctx, req.ID, req.Params, c, h)
	case "agent.get_active_session":
		return handleGetActiveSession(ctx, req.ID, h)
	case "agent.set_active_session":
		return handleSetActiveSession(ctx, req.ID, req.Params, c, h)
	case "agent.delete_session":
		return handleDeleteSession(ctx, req.ID, req.Params, c, h)
	case "agent.rename_session":
		return handleRenameSession(ctx, req.ID, req.Params, h)
	case "agent.search_sessions":
		return handleSearchSessions(ctx, req.ID, req.Params, h)
	case "agent.save_message":
		return handleSaveMessage(ctx, req.ID, req.Params, h)
	case "agent.import_files":
		return handleImportFiles(ctx, req.ID, req.Params, h)
	case "agent.list_imported_files":
		return handleListImportedFiles(ctx, req.ID, req.Params, h)
	case "agent.remove_imported_file":
		return handleRemoveImportedFile(ctx, req.ID, req.Params, h)
	case "agent.get_workspace_info":
		return handleGetWorkspaceInfo(ctx, req.ID, req.Params, h)
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

// handleListSessions returns every session row (with Title) known to
// SessionStore, newest first. Sessions with no messages are still
// returned so the renderer's empty state has a row to highlight.
func handleListSessions(ctx context.Context, id json.RawMessage, h *Handler) *Response {
	if h.SessionStore == nil {
		return successResp(id, ListSessionsResult{Sessions: []SessionWire{}})
	}
	rows, err := h.SessionStore.ListAll(ctx)
	if err != nil {
		return errorResp(id, CodeInternalError, "session list", err)
	}
	out := make([]SessionWire, 0, len(rows))
	for _, r := range rows {
		out = append(out, toSessionWire(r))
	}
	return successResp(id, ListSessionsResult{Sessions: out})
}

// handleGetMessages returns the messages for one session. Pagination is
// supported via params.limit / params.offset (default 1000 / 0). An
// unknown sessionId returns an empty list rather than an error so the
// renderer's initial-load path doesn't choke on a stale id. The rows are
// store.MessageRecord whose JSON tags match the renderer's DarvinMessage.
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
		return successResp(id, GetMessagesResult{Messages: []store.MessageRecord{}})
	}
	rows, err := h.MessageStore.List(ctx, p.SessionID, p.Limit, p.Offset)
	if err != nil {
		return errorResp(id, CodeInternalError, "message list", err)
	}
	return successResp(id, GetMessagesResult{Messages: rows})
}

// handleCreateSession 新建一个 session 并把它设为 active:
//  1. 用 SessionManager 的 idGen 生成 21 位 nanoid
//  2. GetOrCreateEntry 建 SessionEntry,并借 factory 懒建 AcpSession
//  3. 落 SessionStore(默认 title),title 非空时 UpdateTitle
//  4. AppState.SetActiveSession 持久化 active
//
// 返回新建 session 的完整 SessionWire(含 title / status)。
func handleCreateSession(ctx context.Context, id json.RawMessage, params json.RawMessage, c *client, h *Handler) *Response {
	var p CreateSessionParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return errorResp(id, CodeInvalidParams, "params must be an object", nil)
		}
	}
	sessionID := c.sessions.MintSessionID()
	entry, err := c.sessions.GetOrCreateEntry(sessionID)
	if err != nil {
		return errorResp(id, CodeAgentInitFailed, "create session", err)
	}
	if h.SessionStore != nil {
		if err := h.SessionStore.Save(ctx, entry.Session); err != nil {
			return errorResp(id, CodeInternalError, "session save", err)
		}
		if strings.TrimSpace(p.Title) != "" {
			if err := h.SessionStore.UpdateTitle(ctx, sessionID, p.Title); err != nil {
				return errorResp(id, CodeInternalError, "session title", err)
			}
		}
	}
	if h.AppState != nil {
		if err := h.AppState.SetActiveSession(ctx, sessionID); err != nil {
			return errorResp(id, CodeInternalError, "set active session", err)
		}
	}
	return successResp(id, CreateSessionResult{Session: wireForSession(ctx, h, sessionID, entry)})
}

// wireForSession 返回 sessionID 的 SessionWire。优先从 SessionStore 读
// 完整行(带 title);store 为 nil 或读失败时退回到内存 entry 的元数据,
// 保证 handler 测试 stub / 无 store 的快速路径不崩。
func wireForSession(ctx context.Context, h *Handler, sessionID string, entry *SessionEntry) SessionWire {
	if h.SessionStore != nil {
		if row, err := h.SessionStore.GetByID(ctx, sessionID); err == nil {
			return toSessionWire(row)
		}
	}
	return SessionWire{
		ID:        sessionID,
		Title:     "",
		UpdatedAt: entry.Session.UpdatedAt().UnixMilli(),
		Status:    string(entry.Session.Status),
	}
}

// handleGetActiveSession 从 app_state 读 active_session_id;未设置时返
// sessionId=null(renderer 冷启动空状态)。
func handleGetActiveSession(ctx context.Context, id json.RawMessage, h *Handler) *Response {
	if h.AppState == nil {
		return successResp(id, GetActiveSessionResult{SessionID: nil})
	}
	sid, err := h.AppState.GetActiveSession(ctx)
	if err != nil {
		return errorResp(id, CodeInternalError, "get active session", err)
	}
	var out *string
	if sid != "" {
		sid := sid
		out = &sid
	}
	return successResp(id, GetActiveSessionResult{SessionID: out})
}

// handleSetActiveSession 持久化 active_session_id 并刷新 updated_at,
// 让 list 顺序随之推进。Touch 对未知 session 返回的 ErrNotFound 忽略
// (active 已写,存不存在由其它 handler 保证)。
func handleSetActiveSession(ctx context.Context, id json.RawMessage, params json.RawMessage, _ *client, h *Handler) *Response {
	var p SetActiveSessionParams
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if p.SessionID == "" {
		return errorResp(id, CodeInvalidParams, "sessionId is required", nil)
	}
	if h.AppState != nil {
		if err := h.AppState.SetActiveSession(ctx, p.SessionID); err != nil {
			return errorResp(id, CodeInternalError, "set active session", err)
		}
	}
	if h.SessionStore != nil {
		if err := h.SessionStore.Touch(ctx, p.SessionID, time.Now().UnixMilli()); err != nil && !errors.Is(err, store.ErrNotFound) {
			return errorResp(id, CodeInternalError, "session touch", err)
		}
	}
	return successResp(id, SetActiveSessionResult{SessionID: p.SessionID})
}

// handleDeleteSession 删除一个 session:
//  1. 从 SessionManager 摘掉 entry(Stop/abort + Close,防复活)
//  2. SessionStore.Delete 删行
//  3. 若删的是当前 active,则把 app_state 推进到列表首条(或清空),
//     nextActiveSessionId 返回给 renderer 供 UI 切换
func handleDeleteSession(ctx context.Context, id json.RawMessage, params json.RawMessage, c *client, h *Handler) *Response {
	var p DeleteSessionParams
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if p.SessionID == "" {
		return errorResp(id, CodeInvalidParams, "sessionId is required", nil)
	}

	_ = c.sessions.Remove(p.SessionID)

	if h.SessionStore != nil {
		if err := h.SessionStore.Delete(ctx, p.SessionID); err != nil {
			return errorResp(id, CodeInternalError, "session delete", err)
		}
	}

	// 级联删除该 session 的消息行,避免孤儿消息在 sessions.db 累积。
	if h.MessageStore != nil {
		if err := h.MessageStore.DeleteBySession(ctx, p.SessionID); err != nil {
			return errorResp(id, CodeInternalError, "session messages delete", err)
		}
	}

	// 级联删除该 session 的 imported_files 行;workspace 目录本身由 main
	// 端在 darvin:delete_session handler 里 fs.rm(递归)清掉。
	if h.ImportedFiles != nil {
		if err := h.ImportedFiles.DeleteBySession(ctx, p.SessionID); err != nil {
			return errorResp(id, CodeInternalError, "session imported files delete", err)
		}
	}

	// 计算 nextActive:非删 active 时保持原 active;删 active 时取列表首条。
	var next *string
	if h.AppState != nil {
		if cur, err := h.AppState.GetActiveSession(ctx); err == nil && cur == p.SessionID {
			if n, ok := firstSessionID(ctx, h); ok {
				next = &n
			}
			if err := h.AppState.SetActiveSession(ctx, nextActiveValue(next)); err != nil {
				return errorResp(id, CodeInternalError, "set active session", err)
			}
		} else if cur != "" {
			c := cur
			next = &c
		}
	}
	return successResp(id, DeleteSessionResult{Deleted: true, NextActiveSessionID: next})
}

// firstSessionID 返回 ListAll 的首条 id(最新 updated_at)。列表为空返
// ok=false。
func firstSessionID(ctx context.Context, h *Handler) (string, bool) {
	if h.SessionStore == nil {
		return "", false
	}
	rows, err := h.SessionStore.ListAll(ctx)
	if err != nil || len(rows) == 0 {
		return "", false
	}
	return rows[0].ID, true
}

// nextActiveValue 把 next 归一化:删除 active 且列表还有条 → next;删光
// → 空串(清空 app_state)。
func nextActiveValue(next *string) string {
	if next == nil {
		return ""
	}
	return *next
}

// handleRenameSession 更新 title。空 title fallback 到 '新建会话'。
// 同时 Touch 刷 updated_at,让改名后的 session 顶到列表头。
func handleRenameSession(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	var p RenameSessionParams
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if p.SessionID == "" {
		return errorResp(id, CodeInvalidParams, "sessionId is required", nil)
	}
	if h.SessionStore == nil {
		return successResp(id, RenameSessionResult{Session: SessionWire{ID: p.SessionID, Title: p.Title}})
	}
	title := strings.TrimSpace(p.Title)
	if title == "" {
		title = "新建会话"
	}
	if err := h.SessionStore.UpdateTitle(ctx, p.SessionID, title); err != nil {
		return errorResp(id, CodeInternalError, "session rename", err)
	}
	if err := h.SessionStore.Touch(ctx, p.SessionID, time.Now().UnixMilli()); err != nil && !errors.Is(err, store.ErrNotFound) {
		return errorResp(id, CodeInternalError, "session touch", err)
	}
	row, err := h.SessionStore.GetByID(ctx, p.SessionID)
	if err != nil {
		return errorResp(id, CodeInternalError, "session get", err)
	}
	return successResp(id, RenameSessionResult{Session: toSessionWire(row)})
}

// handleSearchSessions 合并两个搜索桶:title 命中(SessionWire) +
// content 命中(SearchHitWire,带所属 session 的 title)。空 query 由
// store 层短路返空。
func handleSearchSessions(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	var p SearchSessionsParams
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if h.SessionStore == nil {
		return successResp(id, SearchSessionsResult{Sessions: []SessionWire{}, Messages: []SearchHitWire{}})
	}
	titleHits, err := h.SessionStore.SearchByTitle(ctx, p.Query)
	if err != nil {
		return errorResp(id, CodeInternalError, "search titles", err)
	}
	contentHits, err := h.SessionStore.SearchByContent(ctx, p.Query, 100)
	if err != nil {
		return errorResp(id, CodeInternalError, "search content", err)
	}
	sessions := make([]SessionWire, 0, len(titleHits))
	for _, r := range titleHits {
		sessions = append(sessions, toSessionWire(r))
	}
	messages := make([]SearchHitWire, 0, len(contentHits))
	for _, r := range contentHits {
		messages = append(messages, SearchHitWire{
			Message:      toMessageRecord(r.Message),
			SessionTitle: r.SessionTitle,
		})
	}
	return successResp(id, SearchSessionsResult{Sessions: sessions, Messages: messages})
}

// toMessageRecord projects a store.Message row (from a search hit) onto
// the wire MessageRecord.
func toMessageRecord(r store.Message) store.MessageRecord {
	return store.MessageRecord{
		ID:         r.ID,
		SessionID:  r.SessionID,
		Role:       r.Role,
		Content:    r.Content,
		ToolCalls:  r.ToolCalls,
		Timestamp:  r.Timestamp,
		StopReason: r.StopReason,
		ParentID:   r.ParentID,
		Done:       r.Done,
		Error:      r.Error,
		ToolLabel:  r.ToolLabel,
	}
}

// SaveMessageParams is the JSON-RPC params for agent.save_message. The role
// is derived from meta.tag when present ('workspace_event' → system), else
// taken from role with a 'user' fallback.
type SaveMessageParams struct {
	SessionID string `json:"sessionId"`
	Content   string `json:"content"`
	Role      string `json:"role"`
	Meta      *struct {
		Tag string `json:"tag"`
	} `json:"meta"`
}

// SaveMessageResult is the JSON-RPC result for agent.save_message.
type SaveMessageResult struct {
	ID string `json:"id"`
}

// ImportFilesParams is the JSON-RPC params for agent.import_files. main
// copies each file into the workspace first and passes the workspace-absolute
// sourcePaths plus the workspace-relative names; the Go side only records
// rows and re-validates containment.
type ImportFilesParams struct {
	SessionID         string   `json:"sessionId"`
	SourcePaths       []string `json:"sourcePaths"`
	WorkspaceRelPaths []string `json:"workspaceRelPaths"`
	Shas              []string `json:"shas"`
	Sizes             []int64  `json:"sizes"`
	OriginalNames     []string `json:"originalNames"`
}

// ImportedFileWire is the renderer-facing imported file shape (matches
// DarvinImportedFile in src/shared/darvin-api.ts). ImportedAt is unix
// milliseconds.
type ImportedFileWire struct {
	ID           string  `json:"id"`
	OriginalName string  `json:"originalName"`
	RelativePath string  `json:"relativePath"`
	Size         int64   `json:"size"`
	MimeType     *string `json:"mimeType"`
	Sha256       string  `json:"sha256"`
	ImportedAt   int64   `json:"importedAt"`
}

// ImportSkipWire is one rejected import entry.
type ImportSkipWire struct {
	SourcePath string `json:"sourcePath"`
	Reason     string `json:"reason"`
	Message    string `json:"message"`
}

// ImportFilesResult is the JSON-RPC result for agent.import_files.
type ImportFilesResult struct {
	Imported []ImportedFileWire `json:"imported"`
	Skipped  []ImportSkipWire   `json:"skipped"`
}

// ListImportedFilesParams is the JSON-RPC params for agent.list_imported_files.
type ListImportedFilesParams struct {
	SessionID string `json:"sessionId"`
}

// ListImportedFilesResult is the JSON-RPC result for agent.list_imported_files.
type ListImportedFilesResult struct {
	Files          []ImportedFileWire `json:"files"`
	WorkspaceBytes int64              `json:"workspaceBytes"`
}

// RemoveImportedFileParams is the JSON-RPC params for agent.remove_imported_file.
type RemoveImportedFileParams struct {
	SessionID string `json:"sessionId"`
	RelPath   string `json:"relPath"`
}

// RemoveImportedFileResult is the JSON-RPC result for agent.remove_imported_file.
type RemoveImportedFileResult struct {
	Removed bool `json:"removed"`
}

// GetWorkspaceInfoResult is the JSON-RPC result for agent.get_workspace_info.
type GetWorkspaceInfoResult struct {
	WorkspaceBytes int64 `json:"workspaceBytes"`
}

// handleSaveMessage inserts one message row. main 用它注入 workspace_event
// system note(导入 / 移除文件),role 由 meta.tag 派生。
func handleSaveMessage(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	var p SaveMessageParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if p.SessionID == "" {
		return errorResp(id, CodeInvalidParams, "sessionId is required", nil)
	}
	if strings.TrimSpace(p.Content) == "" {
		return errorResp(id, CodeInvalidParams, "content is required", nil)
	}
	if h.MessageStore == nil {
		return errorResp(id, CodeInternalError, "message store not configured", nil)
	}
	role := p.Role
	if p.Meta != nil && p.Meta.Tag == "workspace_event" {
		role = "system"
	}
	if role == "" {
		role = "user"
	}
	msgID := uuid.NewString()
	if err := h.MessageStore.Save(ctx, &store.MessageRecord{
		ID:        msgID,
		SessionID: p.SessionID,
		Role:      role,
		Content:   p.Content,
		Timestamp: time.Now().UnixMilli(),
	}); err != nil {
		return errorResp(id, CodeInternalError, "save message", err)
	}
	return successResp(id, SaveMessageResult{ID: msgID})
}

// handleImportFiles records already-copied workspace files as ImportedFile
// rows. Each sourcePath is re-checked against WorkspaceRoot (realpath
// containment) so an injected path cannot reference a file outside the
// sandbox.
func handleImportFiles(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	var p ImportFilesParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if p.SessionID == "" {
		return errorResp(id, CodeInvalidParams, "sessionId is required", nil)
	}
	result := ImportFilesResult{Imported: []ImportedFileWire{}, Skipped: []ImportSkipWire{}}
	if h.ImportedFiles == nil {
		return successResp(id, result)
	}
	for i, src := range p.SourcePaths {
		relPath := at(p.WorkspaceRelPaths, i)
		if err := workspaceContained(h.WorkspaceRoot, src); err != nil {
			result.Skipped = append(result.Skipped, ImportSkipWire{SourcePath: src, Reason: "path_escapes", Message: err.Error()})
			continue
		}
		if relPath == "" || filepath.IsAbs(relPath) || strings.HasPrefix(relPath, "..") {
			result.Skipped = append(result.Skipped, ImportSkipWire{SourcePath: src, Reason: "path_escapes", Message: "workspace-relative path is invalid"})
			continue
		}
		info, err := os.Lstat(src)
		if err != nil || !info.Mode().IsRegular() {
			result.Skipped = append(result.Skipped, ImportSkipWire{SourcePath: src, Reason: "unsupported_type", Message: "not a regular file"})
			continue
		}
		rec := store.ImportedFile{
			ID:           uuid.NewString(),
			SessionID:    p.SessionID,
			OriginalName: at(p.OriginalNames, i),
			RelativePath: relPath,
			Size:         intAt(p.Sizes, i),
			Sha256:       at(p.Shas, i),
		}
		if rec.OriginalName == "" {
			rec.OriginalName = filepath.Base(relPath)
		}
		if rec.Size == 0 {
			rec.Size = info.Size()
		}
		inserted, err := h.ImportedFiles.Insert(ctx, rec)
		if err != nil {
			reason := "import_failed"
			switch {
			case errors.Is(err, store.ErrWorkspaceFull):
				reason = "workspace_full"
			case errors.Is(err, store.ErrDuplicate):
				reason = "duplicate"
			}
			result.Skipped = append(result.Skipped, ImportSkipWire{SourcePath: src, Reason: reason, Message: err.Error()})
			continue
		}
		result.Imported = append(result.Imported, toImportedFileWire(inserted))
	}
	return successResp(id, result)
}

// handleListImportedFiles returns the session's imported files plus the
// current workspace byte total.
func handleListImportedFiles(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	var p ListImportedFilesParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return errorResp(id, CodeInvalidParams, "params must be an object", nil)
		}
	}
	if p.SessionID == "" {
		return errorResp(id, CodeInvalidParams, "sessionId is required", nil)
	}
	result := ListImportedFilesResult{Files: []ImportedFileWire{}, WorkspaceBytes: 0}
	if h.ImportedFiles == nil {
		return successResp(id, result)
	}
	rows, err := h.ImportedFiles.List(ctx, p.SessionID)
	if err != nil {
		return errorResp(id, CodeInternalError, "list imported files", err)
	}
	sum, err := h.ImportedFiles.SumBytes(ctx, p.SessionID)
	if err != nil {
		return errorResp(id, CodeInternalError, "sum imported bytes", err)
	}
	out := make([]ImportedFileWire, 0, len(rows))
	for _, r := range rows {
		out = append(out, toImportedFileWire(r))
	}
	result.Files = out
	result.WorkspaceBytes = sum
	return successResp(id, result)
}

// handleRemoveImportedFile deletes the ImportedFile row. The physical file
// removal and the renderer-side system note are handled by main.
func handleRemoveImportedFile(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	var p RemoveImportedFileParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if p.SessionID == "" || p.RelPath == "" {
		return errorResp(id, CodeInvalidParams, "sessionId and relPath are required", nil)
	}
	if filepath.IsAbs(p.RelPath) || strings.HasPrefix(p.RelPath, "..") {
		return errorResp(id, CodeInvalidParams, "relPath must be a workspace-relative path", nil)
	}
	if h.ImportedFiles == nil {
		return successResp(id, RemoveImportedFileResult{Removed: false})
	}
	if err := h.ImportedFiles.Delete(ctx, p.SessionID, p.RelPath); err != nil {
		return errorResp(id, CodeInternalError, "remove imported file", err)
	}
	return successResp(id, RemoveImportedFileResult{Removed: true})
}

// handleGetWorkspaceInfo returns the session's current workspace byte total
// without exposing the workspace root path (main owns that).
func handleGetWorkspaceInfo(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	var p ListImportedFilesParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return errorResp(id, CodeInvalidParams, "params must be an object", nil)
		}
	}
	if p.SessionID == "" {
		return errorResp(id, CodeInvalidParams, "sessionId is required", nil)
	}
	if h.ImportedFiles == nil {
		return successResp(id, GetWorkspaceInfoResult{WorkspaceBytes: 0})
	}
	sum, err := h.ImportedFiles.SumBytes(ctx, p.SessionID)
	if err != nil {
		return errorResp(id, CodeInternalError, "sum imported bytes", err)
	}
	return successResp(id, GetWorkspaceInfoResult{WorkspaceBytes: sum})
}

// toImportedFileWire projects a store.ImportedFile onto the wire shape.
func toImportedFileWire(r store.ImportedFile) ImportedFileWire {
	return ImportedFileWire{
		ID:           r.ID,
		OriginalName: r.OriginalName,
		RelativePath: r.RelativePath,
		Size:         r.Size,
		MimeType:     r.MimeType,
		Sha256:       r.Sha256,
		ImportedAt:   r.ImportedAt.UnixMilli(),
	}
}

// workspaceContained reports whether abs sits under the workspace root.
func workspaceContained(root, abs string) error {
	if root == "" {
		return errors.New("workspace root not configured")
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return err
	}
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("source path escapes workspace root")
	}
	return nil
}

func at(xs []string, i int) string {
	if i >= 0 && i < len(xs) {
		return xs[i]
	}
	return ""
}

func intAt(xs []int64, i int) int64 {
	if i >= 0 && i < len(xs) {
		return xs[i]
	}
	return 0
}
