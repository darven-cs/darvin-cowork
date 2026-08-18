// JSON-RPC method dispatch for session, message, skill, MCP, and workspace handlers.

package gateway

import (
	"context"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agents/store"
	"darvin-cowork/backend/internal/im"
	"darvin-cowork/backend/internal/mcp"
	"darvin-cowork/backend/internal/scheduledtask"
	"darvin-cowork/backend/internal/skills"
)

// HandlerOptions bundles the shared dependencies the JSON-RPC dispatch
// layer needs. Each per-connection *client carries a reference alongside
// its own write state; the read loop pulls handler into dispatchRequest.
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
	// SubagentStore backs agent.subagent.list. nil means the handler
	// returns an empty list (handler-test stubs).
	SubagentStore store.SubagentStore
	// WorkspaceStore backs agent.list_workspaces / create_workspace /
	// set_active_workspace / delete_workspace and session→workspace
	// binding. nil means those handlers return empty results, suiting
	// handler-test stubs.
	WorkspaceStore *store.SQLiteWorkspaceStore
	// AgentStore backs the agent CRUD RPCs and create_session's agent
	// prompt derivation. nil disables derivation (params carry the prompt)
	// and the agent.* handlers return empty results / not-configured.
	AgentStore store.AgentStore
	// ScheduleHandlers dispatches agent.schedule.* RPCs. nil makes those
	// handlers return an internal-error response.
	ScheduleHandlers *scheduledtask.Handlers
	// IMHandlers dispatches agent.im.* RPCs. nil makes those handlers
	// return an internal-error response.
	IMHandlers *im.Handlers
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
	// SubagentStore backs agent.subagent.list / read_result fallback.
	SubagentStore store.SubagentStore
	// WorkspaceStore backs workspace CRUD + session→workspace binding.
	WorkspaceStore *store.SQLiteWorkspaceStore
	// AgentStore backs agent CRUD + create_session agent derivation.
	// nil disables both (handler-test stubs).
	AgentStore store.AgentStore
	// ScheduleHandlers dispatches agent.schedule.* RPCs. nil disables them.
	ScheduleHandlers *scheduledtask.Handlers
	// IMHandlers dispatches agent.im.* RPCs. nil disables them.
	IMHandlers *im.Handlers
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
		SubagentStore:    o.SubagentStore,
		WorkspaceStore:   o.WorkspaceStore,
		AgentStore:       o.AgentStore,
		ScheduleHandlers: o.ScheduleHandlers,
		IMHandlers:       o.IMHandlers,
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
		return handleListSessions(ctx, req.ID, req.Params, h)
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
	case "agent.llm.list_models":
		return handleLLMListModels(ctx, req.ID, c, h)
	case "agent.import_files":
		return handleImportFiles(ctx, req.ID, req.Params, h)
	case "agent.list_imported_files":
		return handleListImportedFiles(ctx, req.ID, req.Params, h)
	case "agent.remove_imported_file":
		return handleRemoveImportedFile(ctx, req.ID, req.Params, h)
	case "agent.get_workspace_info":
		return handleGetWorkspaceInfo(ctx, req.ID, req.Params, h)
	case "agent.list_workspaces":
		return handleListWorkspaces(ctx, req.ID, h)
	case "agent.create_workspace":
		return handleCreateWorkspace(ctx, req.ID, req.Params, h)
	case "agent.get_active_workspace":
		return handleGetActiveWorkspace(ctx, req.ID, h)
	case "agent.set_active_workspace":
		return handleSetActiveWorkspace(ctx, req.ID, req.Params, h)
	case "agent.delete_workspace":
		return handleDeleteWorkspace(ctx, req.ID, req.Params, c, h)
	case "agent.rename_workspace":
		return handleRenameWorkspace(ctx, req.ID, req.Params, h)
	case "agent.update_workspace_root":
		return handleUpdateWorkspaceRoot(ctx, req.ID, req.Params, h)
	case "agent.bind_session_workspace":
		return handleBindSessionWorkspace(ctx, req.ID, req.Params, h)
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
	case "agent.mcp.resources.list":
		return handleMcpResourcesList(req.ID, req.Params, h)
	case "agent.mcp.resource.read":
		return handleMcpResourceRead(ctx, req.ID, req.Params, h)
	case "agent.mcp.prompts.list":
		return handleMcpPromptsList(req.ID, req.Params, h)
	case "agent.mcp.prompt.get":
		return handleMcpPromptGet(ctx, req.ID, req.Params, h)
	case "agent.mcp.logs.get":
		return handleMcpLogsGet(req.ID, req.Params, h)
	case "agent.tools.list":
		return handleListTools(req.ID, req.Params, h)
	case "agent.skill.invoke_user":
		return handleInvokeSkillUser(req.ID, req.Params, h)
	case "agent.subagent.list":
		return handleSubagentList(ctx, req.ID, req.Params, h)
	case "agent.subagent.get_messages":
		return handleSubagentGetMessages(ctx, req.ID, req.Params, h)
	case "agent.subagent.abort":
		return handleSubagentAbort(ctx, req.ID, req.Params, h)
	case "agent.subagent.read_result":
		return handleSubagentReadResult(ctx, req.ID, req.Params, h)
	case "agent.list_agents":
		return handleListAgents(ctx, req.ID, req.Params, h)
	case "agent.get_agent":
		return handleGetAgent(ctx, req.ID, req.Params, h)
	case "agent.create_agent":
		return handleCreateAgent(ctx, req.ID, req.Params, h)
	case "agent.update_agent":
		return handleUpdateAgent(ctx, req.ID, req.Params, h)
	case "agent.delete_agent":
		return handleDeleteAgent(ctx, req.ID, req.Params, h)
	case "agent.update_default_agent":
		return handleUpdateDefaultAgent(ctx, req.ID, req.Params, h)
	case "agent.schedule.list":
		return handleScheduleList(ctx, req.ID, req.Params, h)
	case "agent.schedule.get":
		return handleScheduleGet(ctx, req.ID, req.Params, h)
	case "agent.schedule.create":
		return handleScheduleCreate(ctx, req.ID, req.Params, h)
	case "agent.schedule.update":
		return handleScheduleUpdate(ctx, req.ID, req.Params, h)
	case "agent.schedule.delete":
		return handleScheduleDelete(ctx, req.ID, req.Params, h)
	case "agent.schedule.toggle":
		return handleScheduleToggle(ctx, req.ID, req.Params, h)
	case "agent.schedule.run_now":
		return handleScheduleRunNow(ctx, req.ID, req.Params, h)
	case "agent.schedule.abort":
		return handleScheduleAbort(ctx, req.ID, req.Params, h)
	case "agent.schedule.list_runs":
		return handleScheduleListRuns(ctx, req.ID, req.Params, h)
	case "agent.schedule.list_all_runs":
		return handleScheduleListAllRuns(ctx, req.ID, req.Params, h)
	case "agent.im.list":
		return handleIMList(ctx, req.ID, req.Params, h)
	case "agent.im.get":
		return handleIMGet(ctx, req.ID, req.Params, h)
	case "agent.im.create":
		return handleIMCreate(ctx, req.ID, req.Params, h)
	case "agent.im.update":
		return handleIMUpdate(ctx, req.ID, req.Params, h)
	case "agent.im.delete":
		return handleIMDelete(ctx, req.ID, req.Params, h)
	case "agent.im.set_enabled":
		return handleIMSetEnabled(ctx, req.ID, req.Params, h)
	case "agent.im.test":
		return handleIMTest(ctx, req.ID, req.Params, h)
	case "agent.im.login_start":
		return handleIMLoginStart(ctx, req.ID, req.Params, h)
	case "agent.im.login_poll":
		return handleIMLoginPoll(ctx, req.ID, req.Params, h)
	default:
		return errorResp(req.ID, CodeMethodNotFound,
			"Method not found: "+req.Method, nil)
	}
}
