// Session CRUD, message listing, usage, and search JSON-RPC handlers.

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"darvin-cowork/backend/internal/agents/store"
)

type ListSessionsResult struct {
	Sessions []SessionWire `json:"sessions"`
}

// SessionWire is the renderer-facing session shape. It is the store
// package's Session row projected onto the darvin-api wire contract;
// UpdatedAt is unix milliseconds.
type SessionWire struct {
	ID              string  `json:"id"`
	Title           string  `json:"title"`
	WorkspaceID     string  `json:"workspaceId"`
	CreatedAt       int64   `json:"createdAt"`
	UpdatedAt       int64   `json:"updatedAt"`
	Status          string  `json:"status"`
	ClaudeSessionID *string `json:"claudeSessionId"`
	SystemPrompt    string  `json:"systemPrompt,omitempty"`
	Identity        string  `json:"identity,omitempty"`
	AgentID         string  `json:"agentId,omitempty"`
}

// toSessionWire projects a store.Session row onto SessionWire.
func toSessionWire(r store.Session) SessionWire {
	return SessionWire{
		ID:              r.ID,
		Title:           r.Title,
		WorkspaceID:     r.WorkspaceID,
		CreatedAt:       r.CreatedAt.UnixMilli(),
		UpdatedAt:       r.UpdatedAt.UnixMilli(),
		Status:          r.Status,
		ClaudeSessionID: r.ClaudeSessionID,
		SystemPrompt:    r.SystemPrompt,
		Identity:        r.Identity,
		AgentID:         r.AgentID,
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

// GetSessionUsageParams is the JSON-RPC params for agent.get_session_usage.
// Returns the persisted per-session snapshot so renderer-side
// contextUsageBySessionId can be rehydrated on session switch / cold start.
type GetSessionUsageParams struct {
	SessionID string `json:"sessionId"`
}

// SessionUsageWire is the JSON-RPC shape of one row from the session_usages
// table. Fields mirror the snapshot columns plus a synthetic lastUsedTokens
// (= LastPromptTokens + LastCompletion) the renderer wants to compute the
// context-window fill percentage. Last / Total are returned as zero-valued
// objects even when no snapshot exists — the renderer reads
// (lastUsedTokens == 0 && totalPromptTokens == 0) as "no usage yet" and
// keeps the empty-state branch.
type SessionUsageWire struct {
	LastPromptTokens      int    `json:"lastPromptTokens"`
	LastCompletionTokens  int    `json:"lastCompletionTokens"`
	LastUsedTokens        int    `json:"lastUsedTokens"`
	LastCacheReadTokens   int    `json:"lastCacheReadTokens"`
	LastCacheWriteTokens  int    `json:"lastCacheWriteTokens"`
	LastContextTokens     int    `json:"lastContextTokens"`
	LastPercent           int    `json:"lastPercent"`
	LastModel             string `json:"lastModel,omitempty"`
	RequestCount          int    `json:"requestCount"`
	TotalPromptTokens     int    `json:"totalPromptTokens"`
	TotalCompletionTokens int    `json:"totalCompletionTokens"`
	UpdatedAt             int64  `json:"updatedAt"`
}

// GetSessionUsageResult is the JSON-RPC result for agent.get_session_usage.
// Usage is always present (zero-valued when no row exists) — the renderer
// distinguishes "no data" by the zero checks above rather than nil.
type GetSessionUsageResult struct {
	Usage SessionUsageWire `json:"usage"`
}

// CreateSessionParams is the JSON-RPC params for agent.create_session.
// title is optional; an empty title falls back to the store default.
// workspaceId is optional; an empty value falls back to the active
// workspace, and a session cannot be created without a workspace.
// systemPrompt / identity are optional session-level prompt data; both
// are fixed at creation time. agentId, when set, takes precedence over
// systemPrompt / identity — the agent's prompt fields are snapshotted
// onto the session instead.
type CreateSessionParams struct {
	Title        string `json:"title,omitempty"`
	WorkspaceID  string `json:"workspaceId,omitempty"`
	SystemPrompt string `json:"systemPrompt,omitempty"`
	Identity     string `json:"identity,omitempty"`
	AgentID      string `json:"agentId,omitempty"`
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

// HandlerOptions bundles the shared dependencies the JSON-RPC dispatch
// layer needs. Each per-connection *client carries a reference alongside
// its own write state; the read loop pulls handler into dispatchRequest.
//
// Steer goes through h.Sessions to reach the per-session Loop; the
// global Agent-level steer path is gone.

// ListSessionsParams is the JSON-RPC params for agent.list_sessions.
// workspaceId filters the list to one workspace; empty returns every
// session (renderer cold-start / legacy callers).
type ListSessionsParams struct {
	WorkspaceID string `json:"workspaceId,omitempty"`
}

func handleListSessions(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	var p ListSessionsParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return errorResp(id, CodeInvalidParams, "params must be an object", nil)
		}
	}
	if h.SessionStore == nil {
		return successResp(id, ListSessionsResult{Sessions: []SessionWire{}})
	}
	var rows []store.Session
	var err error
	if p.WorkspaceID != "" {
		rows, err = h.SessionStore.ListByWorkspace(ctx, p.WorkspaceID)
	} else {
		rows, err = h.SessionStore.ListAll(ctx)
	}
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

// handleGetSessionUsage returns the session's usage snapshot. nil
// store returns a zero-value Usage; missing rows return the same zero
// value — the renderer treats all-zero as "no data". ErrNoUsage
// (ErrRecordNotFound) is equivalent to "this session has never run a
// turn" and is not surfaced as an RPC error.
func handleGetSessionUsage(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	var p GetSessionUsageParams
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if p.SessionID == "" {
		return errorResp(id, CodeInvalidParams, "sessionId is required", nil)
	}
	if h.UsageStore == nil {
		return successResp(id, GetSessionUsageResult{Usage: SessionUsageWire{}})
	}
	rec, err := h.UsageStore.Get(ctx, p.SessionID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return successResp(id, GetSessionUsageResult{Usage: SessionUsageWire{}})
		}
		return errorResp(id, CodeInternalError, "session usage get", err)
	}
	used := 0
	if rec.Last != nil {
		used = rec.Last.PromptTokens + rec.Last.CompletionTokens
	}
	return successResp(id, GetSessionUsageResult{Usage: SessionUsageWire{
		LastPromptTokens:      usageLastPrompt(rec),
		LastCompletionTokens:  usageLastCompletion(rec),
		LastUsedTokens:        used,
		LastCacheReadTokens:   usageLastCacheRead(rec),
		LastCacheWriteTokens:  usageLastCacheWrite(rec),
		LastContextTokens:     rec.LastContextTokens,
		LastPercent:           rec.LastPercent,
		LastModel:             rec.LastModel,
		RequestCount:          rec.RequestCount,
		TotalPromptTokens:     usageTotalPrompt(rec),
		TotalCompletionTokens: usageTotalCompletion(rec),
		UpdatedAt:             rec.UpdatedAt,
	}})
}

func usageLastPrompt(rec *store.UsageRecord) int {
	if rec.Last == nil {
		return 0
	}
	return rec.Last.PromptTokens
}

func usageLastCompletion(rec *store.UsageRecord) int {
	if rec.Last == nil {
		return 0
	}
	return rec.Last.CompletionTokens
}

func usageLastCacheRead(rec *store.UsageRecord) int {
	if rec.Last == nil {
		return 0
	}
	return rec.Last.CacheReadTokens
}

func usageLastCacheWrite(rec *store.UsageRecord) int {
	if rec.Last == nil {
		return 0
	}
	return rec.Last.CacheWriteTokens
}

func usageTotalPrompt(rec *store.UsageRecord) int {
	if rec.Total == nil {
		return 0
	}
	return rec.Total.PromptTokens
}

func usageTotalCompletion(rec *store.UsageRecord) int {
	if rec.Total == nil {
		return 0
	}
	return rec.Total.CompletionTokens
}

// handleCreateSession creates a new session and makes it active:
//  1. Mint a 21-char nanoid via SessionManager.idGen.
//  2. GetOrCreateEntry to build the SessionEntry, lazily building
//     SessionRuntime through the factory.
//  3. Persist via SessionStore (default title); UpdateTitle when a
//     non-empty title is provided.
//  4. AppState.SetActiveSession to persist the active slot.
//
// Returns the full SessionWire (including title / status) for the
// newly created session.
// handleCreateSession creates a new session inside a workspace and makes it
// active:
//  1. Mint a 21-char nanoid via SessionManager.idGen.
//  2. Resolve the owning workspace: params.workspaceId, else the active
//     workspace; both missing → CodeWorkspaceRequired.
//  3. GetOrCreateEntry to build the SessionEntry, lazily building
//     SessionRuntime through the factory.
//  4. Persist via SessionStore (default title); bind workspace_id; set the
//     title when a non-empty title is provided.
//  5. AppState.SetActiveSession to persist the active slot.
//
// Returns the full SessionWire (including title / status) for the
// newly created session.
// errAgentWorkspaceMismatch marks an agentId that resolves to an agent
// owned by a different workspace — a caller error, not a degradation.
var errAgentWorkspaceMismatch = errors.New("agent does not belong to this workspace")

// resolveSessionAgent picks the agent a new session derives its prompt
// from: an explicit params.AgentID first, else the workspace's default
// agent. Returns the resolved agent id (empty = none) plus the prompt
// snapshot to land on the session. An agent id pointing into another
// workspace returns errAgentWorkspaceMismatch; an unknown agent id
// degrades to "no agent" so params carry the prompt instead.
func resolveSessionAgent(ctx context.Context, p CreateSessionParams, workspaceID string, h *Handler) (agentID, systemPrompt, identity string, err error) {
	if h.AgentStore == nil {
		return "", "", "", nil
	}
	resolved := strings.TrimSpace(p.AgentID)
	if resolved == "" && workspaceID != "" {
		def, err := h.AgentStore.GetDefaultForWorkspace(ctx, workspaceID)
		if err == nil && def.ID != "" {
			resolved = def.ID
			systemPrompt, identity = def.SystemPrompt, def.Identity
		}
	}
	if resolved == "" {
		return "", "", "", nil
	}
	agent, err := h.AgentStore.GetByID(ctx, resolved)
	if err != nil {
		return "", "", "", nil
	}
	if workspaceID != "" && agent.WorkspaceID != workspaceID {
		return "", "", "", errAgentWorkspaceMismatch
	}
	return agent.ID, agent.SystemPrompt, agent.Identity, nil
}

func handleCreateSession(ctx context.Context, id json.RawMessage, params json.RawMessage, c *client, h *Handler) *Response {
	var p CreateSessionParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return errorResp(id, CodeInvalidParams, "params must be an object", nil)
		}
	}
	workspaceID := strings.TrimSpace(p.WorkspaceID)
	if workspaceID == "" && h.AppState != nil {
		workspaceID, _ = h.AppState.GetActiveWorkspace(ctx)
	}
	// 仅当 WorkspaceStore 已装配（真实运行时）时强制要求 workspace；
	// handler-test stub / 无 store 快路径允许跳过绑定。
	if workspaceID == "" && h.WorkspaceStore != nil {
		return errorResp(id, CodeWorkspaceRequired, "workspace is required", nil)
	}

	resolvedAgentID, agentSystemPrompt, agentIdentity, err := resolveSessionAgent(ctx, p, workspaceID, h)
	if err != nil {
		return errorResp(id, CodeInvalidParams, err.Error(), nil)
	}

	sessionID := c.sessions.MintSessionID()
	entry, err := c.sessions.GetOrCreateEntry(sessionID)
	if err != nil {
		return errorResp(id, CodeAgentInitFailed, "create session", err)
	}
	// Agent-derived prompt wins; params act as the fallback for the
	// no-agent path (legacy sessions / handler-test stubs).
	sys := agentSystemPrompt
	if sys == "" {
		sys = p.SystemPrompt
	}
	ident := agentIdentity
	if ident == "" {
		ident = p.Identity
	}
	// Must land before SessionStore.Save below so the prompt reaches the
	// row on the same write.
	entry.Session.SetPrompt(sys, ident)
	entry.Session.SetAgentID(resolvedAgentID)
	if h.SessionStore != nil {
		if err := h.SessionStore.Save(ctx, entry.Session); err != nil {
			return errorResp(id, CodeInternalError, "session save", err)
		}
		if err := h.SessionStore.BindWorkspace(ctx, sessionID, workspaceID); err != nil {
			return errorResp(id, CodeInternalError, "session workspace bind", err)
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

// wireForSession returns the SessionWire for sessionID. It prefers the
// full row (with title) from SessionStore; on nil store or read
// failure it falls back to the in-memory entry metadata, so handler-test
// stubs / the no-store fast path never crash.
func wireForSession(ctx context.Context, h *Handler, sessionID string, entry *SessionEntry) SessionWire {
	if h.SessionStore != nil {
		if row, err := h.SessionStore.GetByID(ctx, sessionID); err == nil {
			return toSessionWire(row)
		}
	}
	return SessionWire{
		ID:        sessionID,
		Title:     "",
		CreatedAt: entry.Session.CreatedAt.UnixMilli(),
		UpdatedAt: entry.Session.UpdatedAt().UnixMilli(),
		Status:    string(entry.Session.Status),
		AgentID:   entry.Session.Meta().AgentID,
	}
}

// handleGetActiveSession reads active_session_id from app_state; when
// unset it returns sessionId=null (the renderer's cold-start empty
// state).
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

// handleSetActiveSession persists active_session_id and refreshes
// updated_at so the list order advances. ErrNotFound returned by
// Touch on an unknown session is ignored (active is already written;
// session existence is guaranteed by other handlers).
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
	return successResp(id, SetActiveSessionResult(p))
}

// SetWorkspaceParams is the JSON-RPC params for agent.set_workspace.

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

	// Cascade-delete the session's message rows so orphaned messages do
	// not accumulate in sessions.db.
	if h.MessageStore != nil {
		if err := h.MessageStore.DeleteBySession(ctx, p.SessionID); err != nil {
			return errorResp(id, CodeInternalError, "session messages delete", err)
		}
	}

	// Cascade-delete the session's imported_files rows; the workspace
	// directory itself is removed by the main-side darvin:delete_session
	// handler via fs.rm(recursive).
	if h.ImportedFiles != nil {
		if err := h.ImportedFiles.DeleteBySession(ctx, p.SessionID); err != nil {
			return errorResp(id, CodeInternalError, "session imported files delete", err)
		}
	}

	// Cascade-delete the session's usage snapshots so orphaned rows /
	// context data from a previous session id cannot be misread after a
	// new session reuses the id. UsageStore.DeleteBySession is
	// warn-and-continue (missing row is not an error), so it is safe
	// even when no snapshot was ever persisted.
	if h.UsageStore != nil {
		if err := h.UsageStore.DeleteBySession(ctx, p.SessionID); err != nil {
			return errorResp(id, CodeInternalError, "session usage delete", err)
		}
	}

	// Cascade-delete session_digests rows so hydrate cannot pick up an
	// orphaned digest and replay a session under a different
	// session_id.
	if h.DigestStore != nil {
		if err := h.DigestStore.DeleteBySession(ctx, p.SessionID); err != nil {
			return errorResp(id, CodeInternalError, "session digests delete", err)
		}
	}

	// Compute nextActive: when active is not the deleted session, keep
	// the current active; when it is, take the head of the list.
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

// firstSessionID returns the head id from ListAll (the most recent
// updated_at). Returns ok=false when the list is empty.
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

// nextActiveValue normalises next: when active was deleted and the
// list still has entries → next; when everything was deleted → empty
// string (clear app_state).
func nextActiveValue(next *string) string {
	if next == nil {
		return ""
	}
	return *next
}

// handleRenameSession updates the title. Empty title falls back to
// "新建会话". Touch refreshes updated_at so the renamed session
// bubbles to the top of the list.
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
		title = "新建会话" // empty-title fallback shown in the UI
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

// handleSearchSessions merges two search buckets: title hits
// (SessionWire) and content hits (SearchHitWire carrying the owning
// session's title). Empty query is short-circuited to an empty result
// at the store layer.
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
	return store.MessageRecord(r)
}

// SaveMessageParams is the JSON-RPC params for agent.save_message. The role
// is derived from meta.tag when present ('workspace_event' → system), else
// taken from role with a 'user' fallback.
