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
	"go.uber.org/zap"
	"gorm.io/gorm"

	"darvin-cowork/backend/internal/agentloop"
	"darvin-cowork/backend/internal/agents"
	"darvin-cowork/backend/internal/agents/ctxengine"
	"darvin-cowork/backend/internal/agents/event"
	"darvin-cowork/backend/internal/agents/executor"
	"darvin-cowork/backend/internal/agents/queue"
	"darvin-cowork/backend/internal/agents/store"
	"darvin-cowork/backend/internal/mcp"
	"darvin-cowork/backend/internal/skills"
)

// PromptParams is the JSON-RPC params for agent.prompt. sessionId is
// optional; when omitted the gateway allocates the default session.
// runId is optional; when omitted the gateway mints one so the result
// always carries a non-empty correlation token.
type PromptParams struct {
	Content     string           `json:"content"`
	SessionID   string           `json:"sessionId,omitempty"`
	RunID       string           `json:"runId,omitempty"`
	Attachments []string         `json:"attachments,omitempty"`
	Images      []queue.ImageRef `json:"images,omitempty"`
}

// PromptResult is the JSON-RPC result for agent.prompt. sessionId and
// messageId are 21-char nanoids with no prefix (the spec rejected the
// s-/m- scheme because the WS frame is opaque to the renderer).
// Queued=true means this turn was parked behind a same-session run;
// it will start once the previous turn completes.
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

// SteerParams is the JSON-RPC params for agent.steer. sessionId is
// required — steer targets a specific session's per-session Loop.
// runId is optional; when omitted the steer lands at the head of the
// session's steerQueue regardless of which turn is in flight.
type SteerParams struct {
	SessionID string `json:"sessionId"`
	RunID     string `json:"runId,omitempty"`
	Content   string `json:"content"`
}

// SteerResult is the JSON-RPC result for agent.steer. RunID /
// MessageID / Queued mirror the RunTicket returned by Loop.Steer so
// the renderer can correlate events the same way it does for prompt.
type SteerResult struct {
	Steered   bool   `json:"steered"`
	RunID     string `json:"runId,omitempty"`
	MessageID string `json:"messageId,omitempty"`
	Queued    bool   `json:"queued,omitempty"`
}

// ListSessionsResult is the JSON-RPC result for agent.list_sessions.
// Matches DarvinListSessionsResponse in src/shared/darvin-api.ts:
// `sessions: [{id, title, createdAt, updatedAt, status, claudeSessionId}]`.
type ListSessionsResult struct {
	Sessions []SessionWire `json:"sessions"`
}

// SessionWire is the renderer-facing session shape. It is the store
// package's Session row projected onto the darvin-api wire contract;
// UpdatedAt is unix milliseconds.
type SessionWire struct {
	ID              string  `json:"id"`
	Title           string  `json:"title"`
	CreatedAt       int64   `json:"createdAt"`
	UpdatedAt       int64   `json:"updatedAt"`
	Status          string  `json:"status"`
	ClaudeSessionID *string `json:"claudeSessionId"`
}

// toSessionWire projects a store.Session row onto SessionWire.
func toSessionWire(r store.Session) SessionWire {
	return SessionWire{
		ID:              r.ID,
		Title:           r.Title,
		CreatedAt:       r.CreatedAt.UnixMilli(),
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
// Steer goes through h.Sessions to reach the per-session Loop; the
// global Agent-level steer path is gone.
type HandlerOptions struct {
	ImportedFiles *store.ImportedFileStore
	WorkspaceRoot string
	// SetWorkspaceRoot re-anchors the runtime workspace on
// agent.set_workspace: tools sandbox + project skill rescan +
// RefreshAllTools. Wired by runtime; nil means set_workspace only
// updates Handler.WorkspaceRoot (handler-test stubs).
	SetWorkspaceRoot func(string) error
	// Skills is the skill registry. Wired by main.go.
	Skills *skills.SkillRegistry
	// SkillRunner backs agent.skill.invoke_user. Wired by main.go;
	// nil means the handler returns empty results (handler-test stubs
	// do not need to build a runner).
	SkillRunner *skills.SkillRunner
	// UsageStore backs agent.get_session_usage / session-level cascade
	// delete. nil means get_session_usage returns a zero-value Usage and
	// delete does not cascade.
	UsageStore *store.SQLiteUsageStore
	// DigestStore backs session_digests write + cascade delete. nil
	// means runManualCompact does not persist a digest and
	// handleDeleteSession does not cascade.
	DigestStore store.DigestStore
	// Mcp is the MCP server registry. Wired by main.go.
	Mcp *mcp.Registry
	// Log is the skills-handler logger outlet; nil disables warn logs.
	Log *zap.Logger
}

type Handler struct {
	Sessions     *SessionManager
	Ledger       *EventLedger
	SessionStore store.SessionStore
	MessageStore store.MessageStore
	// UsageStore backs agent.get_session_usage; persisted at the end of
	// Run and read as a snapshot when switching sessions. nil means
	// the handler returns an empty Usage object (renderer takes the
	// empty-state branch).
	UsageStore store.UsageStore
	// DigestStore backs runManualCompact persisting a digest and
	// handleDeleteSession cascading delete. nil means both paths
	// degrade to no-ops.
	DigestStore store.DigestStore
	// AppState persists active_session_id (get/set_active_session and
	// the active-slot rotation in create_session / delete_session).
	// nil means get_active returns null and set/create only touch the
	// in-memory side.
	AppState *store.AppStateStore
	// ImportedFiles backs agent.import_files / list_imported_files /
	// remove_imported_file / get_workspace_info. nil means those
	// handlers return empty results, suiting handler-test stubs.
	ImportedFiles *store.ImportedFileStore
	// WorkspaceRoot is the agent sandbox root (env
	// DARVIN_AGENT_WORKSPACE). import_files uses it for the
	// sourcePaths containment check.
	WorkspaceRoot string
	// SetWorkspaceRoot re-anchors the runtime workspace on
	// agent.set_workspace (sandbox + skills + tools). nil means
	// set_workspace only updates WorkspaceRoot.
	SetWorkspaceRoot func(string) error
	// Skills backs agent.skills.list / set_enabled / bootstrap. nil
	// means those handlers return empty results, suiting handler-test
	// stubs that do not need to build a registry.
	Skills *skills.SkillRegistry
	// SkillRunner backs agent.skill.invoke_user. nil means the
	// handler returns empty results, suiting handler-test stubs.
	SkillRunner *skills.SkillRunner
	// Mcp backs agent.mcp.list / register / update / unregister /
	// set_enabled / test / retry_resolution / bootstrap.
	Mcp *mcp.Registry
	// Log records skills-handler errors; nil falls back to zap.NewNop().
	Log *zap.Logger
}

// NewHandler wires the dependencies. The runtime injects SessionManager,
// EventLedger, the two session / message stores, and AppStateStore.
// opts carries optional workspace / imported-file / skills / mcp wiring.
func NewHandler(
	s *SessionManager,
	l *EventLedger,
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
		Sessions:         s,
		Ledger:           l,
		SessionStore:     sessStore,
		MessageStore:     msgStore,
		UsageStore:       o.UsageStore,
		DigestStore:      o.DigestStore,
		AppState:         appState,
		ImportedFiles:    o.ImportedFiles,
		WorkspaceRoot:    o.WorkspaceRoot,
		SetWorkspaceRoot: o.SetWorkspaceRoot,
		Skills:           o.Skills,
		SkillRunner:      o.SkillRunner,
		Mcp:              o.Mcp,
		Log:              o.Log,
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
	case "agent.compact_context":
		return handleCompactContext(ctx, req.ID, req.Params, c, h)
	case "agent.subscribe_events":
		return handleSubscribeEvents(ctx, req.ID, req.Params, c, h)
	case "agent.steer":
		return handleSteer(ctx, req.ID, req.Params, c, h)
	case "agent.list_sessions":
		return handleListSessions(ctx, req.ID, h)
	case "agent.get_messages":
		return handleGetMessages(ctx, req.ID, req.Params, h)
	case "agent.get_session_usage":
		return handleGetSessionUsage(ctx, req.ID, req.Params, h)
	case "agent.create_session":
		return handleCreateSession(ctx, req.ID, req.Params, c, h)
	case "agent.get_active_session":
		return handleGetActiveSession(ctx, req.ID, h)
	case "agent.set_active_session":
		return handleSetActiveSession(ctx, req.ID, req.Params, c, h)
	case "agent.set_workspace":
		return handleSetWorkspace(ctx, req.ID, req.Params, h)
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
	case "agent.permission_response":
		return handlePermissionResponse(ctx, req.ID, req.Params, c, h)
	case "agent.skills.list":
		return handleListSkills(req.ID, h)
	case "agent.skills.set_enabled":
		return handleSetSkillEnabled(req.ID, req.Params, h)
	case "agent.skills.bootstrap":
		return handleBootstrapSkills(req.ID, req.Params, h)
	case "agent.mcp.list":
		return handleMcpList(req.ID, h)
	case "agent.mcp.register":
		return handleMcpRegister(req.ID, req.Params, h)
	case "agent.mcp.update":
		return handleMcpUpdate(req.ID, req.Params, h)
	case "agent.mcp.unregister":
		return handleMcpUnregister(req.ID, req.Params, h)
	case "agent.mcp.set_enabled":
		return handleMcpSetEnabled(req.ID, req.Params, h)
	case "agent.mcp.test":
		return handleMcpTest(req.ID, req.Params, h)
	case "agent.mcp.retry_resolution":
		return handleMcpRetryResolution(req.ID, req.Params, h)
	case "agent.mcp.bootstrap":
		return handleMcpBootstrap(req.ID, req.Params, h)
	case "agent.tools.list":
		return handleListTools(req.ID, req.Params, h)
	case "agent.skill.invoke_user":
		return handleInvokeSkillUser(req.ID, req.Params, h)
	default:
		return errorResp(req.ID, CodeMethodNotFound,
			"Method not found: "+req.Method, nil)
	}
}

// handlePrompt routes the prompt to sessionID's AgentLoopSession.Loop.
// ErrSessionStalled (Stop refusal window) maps to CodeSessionStalled;
// factory construction failure maps to CodeAgentInitFailed; the
// handler-test stub case (entry.AgentLoop is nil) maps to
// CodeNoAgentLoopSession. Empty sessionId falls back to DefaultSessionID,
// matching the legacy CreateOrGet behaviour.
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
	if entry.AgentLoop == nil {
		// Reached when the handler-test stub did not wire a factory.
		return errorResp(id, CodeNoAgentLoopSession, "no AgentLoopSession bound", nil)
	}
	ticket, err := entry.AgentLoop.Loop.Submit(agentloop.PromptRequest{RunID: p.RunID, Content: p.Content, Attachments: p.Attachments, Images: p.Images})
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

// CompactContextParams is the JSON-RPC params for agent.compact_context.
type CompactContextParams struct {
	SessionID string `json:"sessionId"`
}

// CompactContextResult is the JSON-RPC result for agent.compact_context.
type CompactContextResult struct {
	Accepted  bool   `json:"accepted"`
	SessionID string `json:"sessionId"`
}

// handleCompactContext triggers one manual context compaction. When
// the session is not in a compactable state (running / no assembler /
// no AgentLoopSession) it returns accepted=false and the renderer
// keeps the spinner as-is without entering the next. The compact
// itself includes an LLM summary call and runs in the background to
// avoid blocking the WS read loop; final success / failure is
// driven by the subsequent compaction events.
func handleCompactContext(_ context.Context, id json.RawMessage, params json.RawMessage, c *client, _ *Handler) *Response {
	var p CompactContextParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return errorResp(id, CodeInvalidParams, "params must be an object", nil)
		}
	}
	if p.SessionID == "" {
		return errorResp(id, CodeInvalidParams, "sessionId is required", nil)
	}
	entry, err := c.sessions.GetOrCreateEntry(p.SessionID)
	if err != nil || entry.AgentLoop == nil || entry.AgentLoop.Agent == nil {
		return successResp(id, CompactContextResult{Accepted: false, SessionID: p.SessionID})
	}
	a := entry.AgentLoop.Agent
	if a.IsRunning() || a.Assembler() == nil || !a.AssemblerEnabled() {
		return successResp(id, CompactContextResult{Accepted: false, SessionID: p.SessionID})
	}
	go runManualCompact(a, p.SessionID)
	return successResp(id, CompactContextResult{Accepted: true, SessionID: p.SessionID})
}

func runManualCompact(a *agent.Agent, sessionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	res := a.Assembler().Compact(ctx, ctxengine.CompactParams{
		SessionID: sessionID,
		Messages:  a.Session().Messages(),
		Budget:    a.Config().ContextWindow,
		Force:     true,
		Reason:    "manual",
		LastUsage: a.LastUsage(),
	})
	if !res.Success {
		return
	}
	a.Session().ReplaceAll(res.RetainedMessages)
	if persistErr := a.PersistCompaction(ctx, res); persistErr != nil && ctx.Err() == nil {
		// Already logged by PersistCompaction; the live slice is in
		// place, only the digest row failed to land.
		_ = persistErr
	}
	a.Emit(event.CompactionEvent{
		EventBase: event.EventBase{EventCommon: event.EventCommon{SessionID: sessionID}},
		Before:    res.TokensBefore,
		After:     res.TokensAfter,
		Note:      "manual",
	})
}

// handleSubscribeEvents runs in two phases: EnsureEntry only creates the
// SessionEntry and does not lazily build AgentLoopSession, so subscribing
// to historical sessions from the renderer does not spin up N agents /
// loops / subscriptions. The real event source is materialised on the
// first prompt arrival.
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

// handleSteer routes agent.steer to the target session's per-session
// Loop. The steer lands at the head of the Loop's steerQueue and
// cancels the in-flight turn; idle sessions behave like Submit.
func handleSteer(ctx context.Context, id json.RawMessage, params json.RawMessage, _ *client, h *Handler) *Response {
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	var p SteerParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if p.SessionID == "" {
		return errorResp(id, CodeInvalidParams, "sessionId is required", nil)
	}
	if strings.TrimSpace(p.Content) == "" {
		return errorResp(id, CodeInvalidParams, "content is required", nil)
	}
	entry, err := h.Sessions.EnsureEntry(p.SessionID)
	if err != nil {
		return errorResp(id, CodeInternalError, "session lookup", err)
	}
	if entry.AgentLoop == nil {
		return errorResp(id, CodeNoAgentLoopSession, "session has no agent loop", nil)
	}
	ticket, err := entry.AgentLoop.Loop.Steer(agentloop.PromptRequest{
		RunID:   p.RunID,
		Content: p.Content,
	})
	if err != nil {
		return errorResp(id, CodeInternalError, "loop steer", err)
	}
	return successResp(id, SteerResult{
		Steered:   true,
		RunID:     ticket.RunID,
		MessageID: ticket.MessageID,
		Queued:    ticket.Queued,
	})
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
//     AgentLoopSession through the factory.
//  3. Persist via SessionStore (default title); UpdateTitle when a
//     non-empty title is provided.
//  4. AppState.SetActiveSession to persist the active slot.
//
// Returns the full SessionWire (including title / status) for the
// newly created session.
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
	return successResp(id, SetActiveSessionResult{SessionID: p.SessionID})
}

// SetWorkspaceParams is the JSON-RPC params for agent.set_workspace.
type SetWorkspaceParams struct {
	// SessionID is the session that initiated the switch. set_workspace
	// itself is a process-wide workspace re-anchor with no session
	// semantics; the field is kept only to align with the other
	// workspace handlers.
	SessionID string `json:"sessionId"`
	// RootPath is the new absolute workspace root. Must be an
	// existing directory.
	RootPath string `json:"rootPath"`
}

// SetWorkspaceResult is the JSON-RPC result for agent.set_workspace.
type SetWorkspaceResult struct {
	RootPath string `json:"rootPath"`
}

// handleSetWorkspace re-anchors the runtime workspace to a new
// absolute path. Unlike the main-side legacy restartGoSubprocess,
// it does not restart the Go subprocess, preserving the in-memory
// context and in-flight streams of other sessions. After validating
// rootPath is absolute and points at an existing directory:
//  1. Call h.SetWorkspaceRoot(rootPath) (sandbox + project skills +
//     tools); on failure return an RPC error and leave WorkspaceRoot
//     untouched.
//  2. Update h.WorkspaceRoot for the import_files containment check.
func handleSetWorkspace(_ context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	var p SetWorkspaceParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if strings.TrimSpace(p.RootPath) == "" {
		return errorResp(id, CodeInvalidParams, "rootPath is required", nil)
	}
	if !filepath.IsAbs(p.RootPath) {
		return errorResp(id, CodeInvalidParams, "rootPath must be absolute", nil)
	}
	st, err := os.Stat(p.RootPath)
	if err != nil || !st.IsDir() {
		return errorResp(id, CodeInvalidParams, "rootPath is not an existing directory", nil)
	}
	if h.SetWorkspaceRoot != nil {
		if err := h.SetWorkspaceRoot(p.RootPath); err != nil {
			return errorResp(id, CodeInternalError, "set workspace root", err)
		}
	}
	h.WorkspaceRoot = p.RootPath
	return successResp(id, SetWorkspaceResult{RootPath: p.RootPath})
}

// handleDeleteSession deletes a session:
//  1. Remove the entry from SessionManager (Stop/abort + Close, so it
//     cannot be revived).
//  2. SessionStore.Delete removes the row.
//  3. If the deleted session is the active one, advance app_state to
//     the head of the list (or clear it) and surface
//     nextActiveSessionId to the renderer for UI hand-off.
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

// handleSaveMessage inserts one message row. main uses it to push
// workspace_event system notes (import / remove file); the role is
// derived from meta.tag.
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

// PermissionResponseParams is the JSON-RPC params for agent.permission_response
// (renderer → main → Go). sessionId routes to the right per-session Agent.
type PermissionResponseParams struct {
	SessionID    string         `json:"sessionId"`
	RequestID    string         `json:"requestId"`
	Behavior     string         `json:"behavior"` // allow | deny
	UpdatedInput map[string]any `json:"updatedInput,omitempty"`
	Message      string         `json:"message,omitempty"`
	Interrupt    bool           `json:"interrupt,omitempty"`
	Remember     bool           `json:"remember,omitempty"`
}

// handlePermissionResponse delivers the renderer's answer to the Agent's
// pending permission_request, unblocking the waiting tool call.
func handlePermissionResponse(_ context.Context, id json.RawMessage, params json.RawMessage, c *client, h *Handler) *Response {
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	var p PermissionResponseParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if p.SessionID == "" || p.RequestID == "" || (p.Behavior != "allow" && p.Behavior != "deny") {
		return errorResp(id, CodeInvalidParams, "sessionId, requestId and behavior (allow|deny) are required", nil)
	}
	entry, err := c.sessions.GetOrCreateEntry(p.SessionID)
	if err != nil || entry.AgentLoop == nil || entry.AgentLoop.Agent == nil {
		return errorResp(id, CodeAgentInitFailed, "no agent bound for session", nil)
	}
	entry.AgentLoop.Agent.ResolvePermission(p.RequestID, executor.PermissionResult{
		Behavior:     p.Behavior,
		UpdatedInput: p.UpdatedInput,
		Message:      p.Message,
		Interrupt:    p.Interrupt,
		Remember:     p.Remember,
	})
	return successResp(id, map[string]bool{"resolved": true})
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

// ListSkillsResult is the JSON-RPC result for agent.skills.list. Mirrors
// DarvinListSkillsResponse in src/shared/darvin-api.ts.
type ListSkillsResult struct {
	Skills []skills.SkillSummaryWire `json:"skills"`
}

// SetSkillEnabledParams is the JSON-RPC params for
// agent.skills.set_enabled. Caller is main: it persists to its own SQLite
// first, then asks Go to flip the in-memory flag.
type SetSkillEnabledParams struct {
	SkillID string `json:"skillId"`
	Enabled bool   `json:"enabled"`
}

// SetSkillEnabledResult is the JSON-RPC result for agent.skills.set_enabled.
type SetSkillEnabledResult struct {
	OK bool `json:"ok"`
}

// BootstrapSkillsParams is the JSON-RPC params for agent.skills.bootstrap.
// main is the source of truth for the enabled state: at startup it
// bulk-pushes the enabled flags from its SQLite, and Go overwrites the
// local defaults with the main-side values.
type BootstrapSkillsParams struct {
	Skills []skills.SkillSummaryWire `json:"skills"`
}

// BootstrapSkillsResult is the JSON-RPC result for agent.skills.bootstrap.
type BootstrapSkillsResult struct {
	OK bool `json:"ok"`
}

// ToolDescriptorWire is one entry of agent.tools.list. Mirrors
// DarvinToolDescriptor in src/shared/darvin-api.ts.
type ToolDescriptorWire struct {
	Name        string         `json:"name"`
	Kind        string         `json:"kind"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// ListToolsResult is the JSON-RPC result for agent.tools.list.
type ListToolsResult struct {
	Tools []ToolDescriptorWire `json:"tools"`
}

// ListToolsParams is the optional sessionId for agent.tools.list. When
// omitted the default session's tool surface is returned — all sessions
// share the same plugin registries, so the surface is identical.
type ListToolsParams struct {
	SessionID string `json:"sessionId,omitempty"`
}

// handleListTools returns the merged agent tool registry view for the
// given session (default when omitted). When the session has not yet
// built an AgentLoopSession we lazily build it once so the first query
// already includes the full skill / mcp plugin surface.
func handleListTools(id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.Sessions == nil {
		return successResp(id, ListToolsResult{Tools: []ToolDescriptorWire{}})
	}
	var p ListToolsParams
	if len(params) > 0 {
		_ = json.Unmarshal(params, &p)
	}
	sessionID := p.SessionID
	if sessionID == "" {
		sessionID = DefaultSessionID
	}
	entry, err := h.Sessions.GetOrCreateEntry(sessionID)
	if err != nil {
		return errorResp(id, CodeInternalError, err.Error(), err)
	}
	if entry.AgentLoop == nil {
		return successResp(id, ListToolsResult{Tools: []ToolDescriptorWire{}})
	}
	reg := entry.AgentLoop.Agent.Tools()
	entries := reg.List()
	out := make([]ToolDescriptorWire, 0, len(entries))
	for _, e := range entries {
		out = append(out, ToolDescriptorWire{
			Name:        e.Tool.Name(),
			Kind:        string(e.Kind),
			Description: e.Tool.Description(),
			InputSchema: rawSchemaToMap(e.Tool.Parameters()),
			Metadata:    e.Metadata,
		})
	}
	return successResp(id, ListToolsResult{Tools: out})
}

func rawSchemaToMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{"type": "object"}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

// handleListSkills returns the current snapshot of the Go-side
// registry. Go loads both the bundled 5 skills from embed and the
// user-directory skills; the list is one of the sources of truth
// (the enabled state is overridden by the main-side bootstrap).
func handleListSkills(id json.RawMessage, h *Handler) *Response {
	if h.Skills == nil {
		return successResp(id, ListSkillsResult{Skills: []skills.SkillSummaryWire{}})
	}
	entries := h.Skills.Snapshot()
	return successResp(id, ListSkillsResult{Skills: skills.ToSummaries(entries)})
}

// handleSetSkillEnabled flips the in-memory enabled flag for a skill,
// then broadcasts agent.skills.changed on every active WS so main can
// refresh its own cache. Errors are returned as JSON-RPC errors; the
// Go-side state is left untouched on failure.
func handleSetSkillEnabled(id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.Skills == nil {
		return errorResp(id, CodeMethodNotFound, "skills not configured", nil)
	}
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	var p SetSkillEnabledParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if strings.TrimSpace(p.SkillID) == "" {
		return errorResp(id, CodeInvalidParams, "skillId is required", nil)
	}
	if err := h.Skills.SetEnabled(p.SkillID, p.Enabled); err != nil {
		return errorResp(id, CodeInvalidParams, err.Error(), err)
	}
	h.broadcastSkillsChanged()
	h.refreshToolsIfNeeded()
	return successResp(id, SetSkillEnabledResult{OK: true})
}

// refreshToolsIfNeeded re-runs plugin registration for every session
// after a skill / mcp state change so the agent tool surface stays
// current. No-op when SessionManager is nil.
func (h *Handler) refreshToolsIfNeeded() {
	if h.Sessions != nil {
		h.Sessions.RefreshAllTools()
	}
}

// InvokeSkillUserParams is the JSON-RPC params for agent.skill.invoke_user.
// Content is the raw `/skill-name args` command the user sent; when empty
// the handler reconstructs it from SkillID + Args so the persisted user
// message matches the renderer bubble.
type InvokeSkillUserParams struct {
	SessionID string `json:"sessionId,omitempty"`
	SkillID   string `json:"skillId"`
	Args      string `json:"args,omitempty"`
	Content   string `json:"content,omitempty"`
}

// InvokeSkillUserResult is the JSON-RPC result for agent.skill.invoke_user.
// Mirrors PromptResult so the renderer can start an assistant bubble keyed
// by messageId and correlate the run's events.
type InvokeSkillUserResult struct {
	SessionID string `json:"sessionId"`
	RunID     string `json:"runId"`
	MessageID string `json:"messageId"`
	OK        bool   `json:"ok"`
}

// handleInvokeSkillUser implements explicit `/skill-name args`
// invocations from the user. Validation (exists + enabled +
// userInvocable) is synchronous — failures are returned as JSON-RPC
// errors so main can surface a toast; on success the skill context is
// handed to the session's Loop to run a mini agent loop
// asynchronously, with an event stream identical to a normal prompt.
func handleInvokeSkillUser(id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.Skills == nil || h.SkillRunner == nil {
		return errorResp(id, CodeMethodNotFound, "skills not configured", nil)
	}
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	var p InvokeSkillUserParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if strings.TrimSpace(p.SkillID) == "" {
		return errorResp(id, CodeInvalidParams, "skillId is required", nil)
	}
	sessionID := p.SessionID
	if sessionID == "" {
		sessionID = DefaultSessionID
	}

	sec, err := h.SkillRunner.ExecuteByUserInvocation(context.Background(), p.SkillID, p.Args)
	if err != nil {
		switch {
		case errors.Is(err, skills.ErrSkillNotFound):
			return errorResp(id, CodeSkillNotFound, err.Error(), err)
		case errors.Is(err, skills.ErrSkillDisabled):
			return errorResp(id, CodeSkillDisabled, err.Error(), err)
		case errors.Is(err, skills.ErrSkillNotUserInvocable):
			return errorResp(id, CodeSkillNotUserInvocable, err.Error(), err)
		default:
			return errorResp(id, CodeInternalError, "skill invoke", err)
		}
	}

	entry, err := h.Sessions.GetOrCreateEntry(sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionStalled) {
			return errorResp(id, CodeSessionStalled, "session stalled", err)
		}
		return errorResp(id, CodeAgentInitFailed, "get session", err)
	}
	if entry.AgentLoop == nil {
		return errorResp(id, CodeNoAgentLoopSession, "no AgentLoopSession bound", nil)
	}

	content := p.Content
	if strings.TrimSpace(content) == "" {
		content = "/" + p.SkillID
		if p.Args != "" {
			content += " " + p.Args
		}
	}
	ticket, err := entry.AgentLoop.Loop.SubmitSkill(agentloop.SkillInvocation{
		SystemPrompt: sec.SystemPrompt,
		Content:      content,
		Tools:        sec.Tools,
	})
	if err != nil {
		return errorResp(id, CodeInternalError, "loop submit", err)
	}
	return successResp(id, InvokeSkillUserResult{
		SessionID: entry.Session.ID,
		RunID:     ticket.RunID,
		MessageID: ticket.MessageID,
		OK:        true,
	})
}

// handleBootstrapSkills accepts the initial enabled state pushed by
// main and overwrites the Go-side registry flags one by one. Unknown
// ids are no-ops (so main can re-trigger a reload instead of receiving
// an error here), keeping the protocol idempotent.
func handleBootstrapSkills(id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.Skills == nil {
		return errorResp(id, CodeMethodNotFound, "skills not configured", nil)
	}
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	var p BootstrapSkillsParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	for _, s := range p.Skills {
		_ = h.Skills.SetEnabled(s.ID, s.Enabled)
	}
	h.broadcastSkillsChanged()
	return successResp(id, BootstrapSkillsResult{OK: true})
}

// broadcastSkillsChanged pushes agent.skills.changed to every active
// WS. It routes through EventLedger.Broadcast rather than the
// per-session path — skill state is global, and main is the sole
// subscriber.
func (h *Handler) broadcastSkillsChanged() {
	if h.Skills == nil || h.Ledger == nil {
		return
	}
	entries := h.Skills.Snapshot()
	params := map[string]any{
		"skills": skills.ToSummaries(entries),
	}
	h.Ledger.Broadcast("agent.skills.changed", params)
}

// --- MCP handlers ---

// ListMcpServersResult is the JSON-RPC result for agent.mcp.list.
// Mirrors DarvinListMcpServersResponse in src/shared/darvin-api.ts.
type ListMcpServersResult struct {
	Servers []McpServerWire `json:"servers"`
}

// McpServerWire is the IPC wire shape for a server. CreatedAt /
// UpdatedAt are unix ms; LaunchStatus / ConnectionStatus / etc. are
// nilable so the renderer can distinguish "not yet reported" from
// "reported as disconnected".
type McpServerWire struct {
	ID               string                     `json:"id"`
	Name             string                     `json:"name"`
	Description      string                     `json:"description"`
	Enabled          bool                       `json:"enabled"`
	TransportType    string                     `json:"transportType"`
	Command          string                     `json:"command,omitempty"`
	Args             []string                   `json:"args,omitempty"`
	Env              map[string]string          `json:"env,omitempty"`
	URL              string                     `json:"url,omitempty"`
	Headers          map[string]string          `json:"headers,omitempty"`
	IsBuiltIn        bool                       `json:"isBuiltIn"`
	GithubURL        string                     `json:"githubUrl,omitempty"`
	RegistryID       string                     `json:"registryId,omitempty"`
	CreatedAt        int64                      `json:"createdAt"`
	UpdatedAt        int64                      `json:"updatedAt"`
	LaunchStatus     string                     `json:"launchStatus,omitempty"`
	LaunchError      string                     `json:"launchError,omitempty"`
	ConnectionStatus string                     `json:"connectionStatus,omitempty"`
	ConnectionError  string                     `json:"connectionError,omitempty"`
	ExposedTools     []McpServerExposedToolWire `json:"exposedTools,omitempty"`
}

type McpServerExposedToolWire struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// McpLaunchResolutionWire is the wire shape for the resolution payload
// of mcp.resolution_changed.
type McpLaunchResolutionWire struct {
	ServerID          string            `json:"serverId"`
	ResolverKind      string            `json:"resolverKind"`
	SourceFingerprint string            `json:"sourceFingerprint"`
	Status            string            `json:"status"`
	PackageName       string            `json:"packageName,omitempty"`
	RequestedVersion  string            `json:"requestedVersion,omitempty"`
	ResolvedVersion   string            `json:"resolvedVersion,omitempty"`
	InstallDir        string            `json:"installDir,omitempty"`
	Command           string            `json:"command,omitempty"`
	Args              []string          `json:"args"`
	Env               map[string]string `json:"env"`
	Error             string            `json:"error,omitempty"`
	InstalledAt       *int64            `json:"installedAt,omitempty"`
	ResolvedAt        *int64            `json:"resolvedAt,omitempty"`
	UpdatedAt         int64             `json:"updatedAt"`
}

func wireFromServer(s mcp.ServerSpec, st mcp.ServerStatus, now int64) McpServerWire {
	w := McpServerWire{
		ID:            s.ID,
		Name:          s.Name,
		Description:   s.Description,
		Enabled:       st.Enabled,
		TransportType: string(s.Transport),
		Command:       s.Command,
		Args:          append([]string(nil), s.Args...),
		URL:           s.URL,
		Headers:       mcp.CloneStringMap(s.Headers),
		IsBuiltIn:     s.IsBuiltIn,
		GithubURL:     s.GitHubURL,
		RegistryID:    s.RegistryID,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if len(s.Env) > 0 {
		w.Env = mcp.CloneStringMap(s.Env)
	}
	if st.Connected {
		w.ConnectionStatus = string(mcp.ConnectionConnected)
	} else if st.ConnectionError != "" {
		w.ConnectionStatus = string(mcp.ConnectionError)
		w.ConnectionError = st.ConnectionError
	} else if st.Resolving {
		w.ConnectionStatus = string(mcp.ConnectionConnecting)
	} else {
		w.ConnectionStatus = string(mcp.ConnectionDisconnected)
	}
	if st.Resolution != nil {
		w.LaunchStatus = string(st.Resolution.Status)
		w.LaunchError = st.Resolution.Error
	}
	if len(st.Tools) > 0 {
		w.ExposedTools = make([]McpServerExposedToolWire, 0, len(st.Tools))
		for _, t := range st.Tools {
			w.ExposedTools = append(w.ExposedTools, McpServerExposedToolWire{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: t.InputSchema,
			})
		}
	}
	return w
}

func wireFromResolution(r mcp.LaunchResolution) McpLaunchResolutionWire {
	w := McpLaunchResolutionWire{
		ServerID:          r.ServerID,
		ResolverKind:      string(r.ResolverKind),
		SourceFingerprint: r.SourceFingerprint,
		Status:            string(r.Status),
		PackageName:       r.PackageName,
		RequestedVersion:  r.RequestedVersion,
		ResolvedVersion:   r.ResolvedVersion,
		InstallDir:        r.InstallDir,
		Command:           r.Command,
		Args:              append([]string(nil), r.Args...),
		Env:               mcp.CloneStringMap(r.Env),
		Error:             r.Error,
		UpdatedAt:         r.UpdatedAt.UnixMilli(),
	}
	if !r.InstalledAt.IsZero() {
		ms := r.InstalledAt.UnixMilli()
		w.InstalledAt = &ms
	}
	if !r.ResolvedAt.IsZero() {
		ms := r.ResolvedAt.UnixMilli()
		w.ResolvedAt = &ms
	}
	return w
}

func handleMcpList(id json.RawMessage, h *Handler) *Response {
	if h.Mcp == nil {
		return successResp(id, ListMcpServersResult{Servers: []McpServerWire{}})
	}
	statuses := h.Mcp.List()
	now := time.Now().UnixMilli()
	out := make([]McpServerWire, 0, len(statuses))
	for _, st := range statuses {
		spec, ok := h.Mcp.GetSpec(st.ServerID)
		if !ok {
			continue
		}
		out = append(out, wireFromServer(spec, st, now))
	}
	return successResp(id, ListMcpServersResult{Servers: out})
}

type McpRegisterParams struct {
	Server mcp.ServerSpec `json:"server"`
}

func handleMcpRegister(id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.Mcp == nil {
		return errorResp(id, CodeNoAgentLoopSession, "mcp registry not configured", nil)
	}
	var p McpRegisterParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if p.Server.ID == "" {
		return errorResp(id, CodeInvalidParams, "server.id required", nil)
	}
	if err := h.Mcp.Register(context.Background(), p.Server); err != nil {
		return errorResp(id, CodeInternalError, err.Error(), nil)
	}
	return successResp(id, map[string]any{"ok": true})
}

type McpUpdateParams struct {
	ID    string         `json:"id"`
	Patch mcp.ServerSpec `json:"patch"`
}

func handleMcpUpdate(id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.Mcp == nil {
		return errorResp(id, CodeNoAgentLoopSession, "mcp registry not configured", nil)
	}
	var p McpUpdateParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if p.ID == "" {
		return errorResp(id, CodeInvalidParams, "id required", nil)
	}
	if err := h.Mcp.Update(context.Background(), p.ID, p.Patch); err != nil {
		return errorResp(id, CodeInternalError, err.Error(), nil)
	}
	spec, ok := h.Mcp.GetSpec(p.ID)
	if !ok {
		return errorResp(id, CodeInternalError, "server disappeared after update", nil)
	}
	st, _ := h.Mcp.Get(p.ID)
	return successResp(id, map[string]any{"server": wireFromServer(spec, st, time.Now().UnixMilli())})
}

type McpServerIDParams struct {
	ID string `json:"id"`
}

func handleMcpUnregister(id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.Mcp == nil {
		return errorResp(id, CodeNoAgentLoopSession, "mcp registry not configured", nil)
	}
	var p McpServerIDParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if err := h.Mcp.Unregister(context.Background(), p.ID); err != nil {
		return errorResp(id, CodeInternalError, err.Error(), nil)
	}
	return successResp(id, map[string]any{"ok": true})
}

type McpSetEnabledParams struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

func handleMcpSetEnabled(id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.Mcp == nil {
		return errorResp(id, CodeNoAgentLoopSession, "mcp registry not configured", nil)
	}
	var p McpSetEnabledParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if err := h.Mcp.SetEnabled(context.Background(), p.ID, p.Enabled); err != nil {
		return errorResp(id, CodeInternalError, err.Error(), nil)
	}
	return successResp(id, map[string]any{"ok": true})
}

func handleMcpTest(id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.Mcp == nil {
		return successResp(id, map[string]any{"ok": false, "error": "mcp registry not configured"})
	}
	var p McpServerIDParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	ok, errMsg, tools := h.Mcp.Test(p.ID)
	resp := map[string]any{
		"ok":    ok,
		"error": errMsg,
	}
	if len(tools) > 0 {
		wire := make([]McpServerExposedToolWire, 0, len(tools))
		for _, t := range tools {
			wire = append(wire, McpServerExposedToolWire{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: t.InputSchema,
			})
		}
		resp["tools"] = wire
	}
	return successResp(id, resp)
}

func handleMcpRetryResolution(id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.Mcp == nil {
		return errorResp(id, CodeNoAgentLoopSession, "mcp registry not configured", nil)
	}
	var p McpServerIDParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if err := h.Mcp.RetryResolution(p.ID); err != nil {
		return errorResp(id, CodeInternalError, err.Error(), nil)
	}
	return successResp(id, map[string]any{"ok": true})
}

type McpBootstrapParams struct {
	Servers []mcp.ServerSpec `json:"servers"`
}

func handleMcpBootstrap(id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.Mcp == nil {
		return successResp(id, map[string]any{"ok": true})
	}
	var p McpBootstrapParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	for i := range p.Servers {
		if err := h.Mcp.Register(context.Background(), p.Servers[i]); err != nil {
			if h.Log != nil {
				h.Log.Warn("mcp bootstrap register failed",
					zap.String("id", p.Servers[i].ID),
					zap.Error(err))
			}
		}
	}
	return successResp(id, map[string]any{"ok": true})
}

// OnMcpConnectionChanged is the mcp.Notifier callback wired up by
// main.go after both registry and handler exist. It broadcasts the
// mcp.connection_changed notification; main forwards the payload to
// renderer via darvin:push:mcp-connection-changed.
func (h *Handler) OnMcpConnectionChanged(serverID string, status mcp.ConnectionStatus, errMsg string) {
	if h.Ledger != nil {
		h.Ledger.Broadcast("mcp.connection_changed", map[string]any{
			"id":     serverID,
			"status": string(status),
			"error":  errMsg,
		})
	}
	h.refreshToolsIfNeeded()
}

// OnMcpResolutionChanged is the mcp.Notifier callback for resolver
// output (pending / installing / ready / failed). main persists to
// SQLite and pushes the renderer via darvin:push:mcp-servers-changed
// (launchStatus field).
func (h *Handler) OnMcpResolutionChanged(serverID string, res mcp.LaunchResolution) {
	if h.Ledger == nil {
		return
	}
	h.Ledger.Broadcast("mcp.resolution_changed", map[string]any{
		"serverId":   serverID,
		"resolution": wireFromResolution(res),
	})
}
