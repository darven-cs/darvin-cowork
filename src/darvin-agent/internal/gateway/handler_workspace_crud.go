// Workspace-first entity CRUD and active-workspace JSON-RPC handlers.

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/jaevor/go-nanoid"

	"darvin-cowork/backend/internal/agents/store"
)

var workspaceIDGen = nanoid.MustCustomASCII(sessionAlphabet, sessionIDLen)

// WorkspaceWire is the renderer-facing workspace shape. Label is the
// display title (name when set, else the root basename); RootPath is sent
// only so the renderer can operate on files in the workspace directory.
type WorkspaceWire struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Label          string `json:"label"`
	RootPath       string `json:"rootPath"`
	SessionCount   int64  `json:"sessionCount"`
	DefaultAgentID string `json:"defaultAgentId,omitempty"`
	CreatedAt      int64  `json:"createdAt"`
	UpdatedAt      int64  `json:"updatedAt"`
}

// ListWorkspacesResult is the JSON-RPC result for agent.list_workspaces.
type ListWorkspacesResult struct {
	Workspaces []WorkspaceWire `json:"workspaces"`
}

// handleListWorkspaces returns every workspace with its session count.
func handleListWorkspaces(ctx context.Context, id json.RawMessage, h *Handler) *Response {
	if h.WorkspaceStore == nil {
		return successResp(id, ListWorkspacesResult{Workspaces: []WorkspaceWire{}})
	}
	rows, err := h.WorkspaceStore.List(ctx)
	if err != nil {
		return errorResp(id, CodeInternalError, "workspace list", err)
	}
	out := make([]WorkspaceWire, 0, len(rows))
	for _, r := range rows {
		n, err := h.WorkspaceStore.CountSessions(ctx, r.ID)
		if err != nil {
			return errorResp(id, CodeInternalError, "workspace session count", err)
		}
		out = append(out, toWorkspaceWire(r, n))
	}
	return successResp(id, ListWorkspacesResult{Workspaces: out})
}

// CreateWorkspaceParams is the JSON-RPC params for agent.create_workspace.
// rootPath is the intended workspace directory (main computes the default
// under userData/workspaces/<id>); the handler creates it if missing.
type CreateWorkspaceParams struct {
	Name     string `json:"name,omitempty"`
	RootPath string `json:"rootPath,omitempty"`
}

// CreateWorkspaceResult is the JSON-RPC result for agent.create_workspace.
type CreateWorkspaceResult struct {
	Workspace WorkspaceWire `json:"workspace"`
}

// handleCreateWorkspace records a new workspace row. The directory is
// created here (idempotent), so main need not guarantee ordering; an
// existing row for the same rootPath is returned unchanged (retry-safe).
func handleCreateWorkspace(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	var p CreateWorkspaceParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if strings.TrimSpace(p.RootPath) == "" {
		return errorResp(id, CodeInvalidParams, "rootPath is required", nil)
	}
	if !filepath.IsAbs(p.RootPath) {
		return errorResp(id, CodeInvalidParams, "rootPath must be absolute", nil)
	}
	if h.WorkspaceStore == nil {
		return errorResp(id, CodeInternalError, "workspace store not configured", nil)
	}
	if existing, err := h.WorkspaceStore.GetByRoot(ctx, p.RootPath); err == nil {
		return successResp(id, CreateWorkspaceResult{Workspace: toWorkspaceWire(existing, 0)})
	} else if !errors.Is(err, store.ErrNotFound) {
		return errorResp(id, CodeInternalError, "workspace lookup", err)
	}
	if err := os.MkdirAll(p.RootPath, 0o755); err != nil {
		return errorResp(id, CodeInternalError, "create workspace dir", err)
	}
	w := store.Workspace{ID: workspaceIDGen(), Name: strings.TrimSpace(p.Name), RootPath: p.RootPath}
	if err := h.WorkspaceStore.Create(ctx, w); err != nil {
		return errorResp(id, CodeInternalError, "create workspace", err)
	}
	// Seed the 9 expert presets + bind the Main Agent default so a fresh
	// workspace is immediately usable by the expert suite / settings UI.
	// Failures keep the workspace functional but agent-less; the
	// create_session path degrades to "no default agent".
	if h.AgentStore != nil {
		if err := h.AgentStore.SeedPresets(ctx, w.ID); err != nil {
			return errorResp(id, CodeInternalError, "seed agent presets", err)
		}
		main, err := h.AgentStore.EnsureDefaultForWorkspace(ctx, w.ID)
		if err == nil {
			_ = h.WorkspaceStore.UpdateDefaultAgent(ctx, w.ID, main.ID)
			w.DefaultAgentID = main.ID
		}
	}
	return successResp(id, CreateWorkspaceResult{Workspace: toWorkspaceWire(w, 0)})
}

// GetActiveWorkspaceResult is the JSON-RPC result for
// agent.get_active_workspace. workspaceId is null when none is persisted.
type GetActiveWorkspaceResult struct {
	WorkspaceID *string `json:"workspaceId"`
}

// handleGetActiveWorkspace reads active_workspace_id from app_state.
func handleGetActiveWorkspace(ctx context.Context, id json.RawMessage, h *Handler) *Response {
	if h.AppState == nil {
		return successResp(id, GetActiveWorkspaceResult{WorkspaceID: nil})
	}
	wid, err := h.AppState.GetActiveWorkspace(ctx)
	if err != nil {
		return errorResp(id, CodeInternalError, "get active workspace", err)
	}
	var out *string
	if wid != "" {
		wid := wid
		out = &wid
	}
	return successResp(id, GetActiveWorkspaceResult{WorkspaceID: out})
}

// SetActiveWorkspaceParams is the JSON-RPC params for
// agent.set_active_workspace.
type SetActiveWorkspaceParams struct {
	WorkspaceID string `json:"workspaceId"`
}

// SetActiveWorkspaceResult is the JSON-RPC result for
// agent.set_active_workspace. ActiveSessionID is the workspace's most
// recent session (or null when the workspace has none), synchronized by
// the handler so a workspace switch atomically lands on its conversation.
type SetActiveWorkspaceResult struct {
	WorkspaceID     string  `json:"workspaceId"`
	ActiveSessionID *string `json:"activeSessionId"`
}

// handleSetActiveWorkspace persists active_workspace_id and re-anchors the
// active session to that workspace's most recent session.
func handleSetActiveWorkspace(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	var p SetActiveWorkspaceParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if p.WorkspaceID == "" {
		return errorResp(id, CodeInvalidParams, "workspaceId is required", nil)
	}
	if h.AppState == nil {
		return errorResp(id, CodeInternalError, "app state not configured", nil)
	}
	if h.WorkspaceStore != nil {
		if _, err := h.WorkspaceStore.GetByID(ctx, p.WorkspaceID); err != nil {
			return errorResp(id, CodeInternalError, "workspace lookup", err)
		}
	}
	if err := h.AppState.SetActiveWorkspace(ctx, p.WorkspaceID); err != nil {
		return errorResp(id, CodeInternalError, "set active workspace", err)
	}
	var nextSession *string
	if h.WorkspaceStore != nil {
		if ids, err := h.WorkspaceStore.ListSessionIDs(ctx, p.WorkspaceID); err == nil && len(ids) > 0 {
			sid := ids[0]
			nextSession = &sid
		}
	}
	if nextSession != nil {
		_ = h.AppState.SetActiveSession(ctx, *nextSession)
	} else {
		_ = h.AppState.SetActiveSession(ctx, "")
	}
	return successResp(id, SetActiveWorkspaceResult{WorkspaceID: p.WorkspaceID, ActiveSessionID: nextSession})
}

// DeleteWorkspaceParams is the JSON-RPC params for agent.delete_workspace.
// force, when true, cascades the deletion to every session in the
// workspace (and through to messages / imported-files / digests / usage).
// Without force a non-empty workspace is refused to keep callers explicit.
type DeleteWorkspaceParams struct {
	WorkspaceID string `json:"workspaceId"`
	Force       bool   `json:"force,omitempty"`
}

// DeleteWorkspaceResult is the JSON-RPC result for agent.delete_workspace.
// nextActiveWorkspaceId is null when the deleted workspace was the last one.
// deletedSessionCount is the number of sessions removed by a forced
// delete; zero for an empty delete.
type DeleteWorkspaceResult struct {
	Deleted               bool    `json:"deleted"`
	NextActiveWorkspaceID *string `json:"nextActiveWorkspaceId"`
	DeletedSessionCount   int     `json:"deletedSessionCount"`
}

// handleDeleteWorkspace removes a workspace. With force=false a non-empty
// workspace is refused; with force=true every session in the workspace is
// deleted (cascading through messages / imported-files / usage / digests),
// then the workspace row itself is removed. The forced cascade proceeds
// non-active sessions first to avoid an active-session rotation mid-loop
// pinning the next-active pointer to a session we're about to delete.
func handleDeleteWorkspace(ctx context.Context, id json.RawMessage, params json.RawMessage, c *client, h *Handler) *Response {
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	var p DeleteWorkspaceParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if p.WorkspaceID == "" {
		return errorResp(id, CodeInvalidParams, "workspaceId is required", nil)
	}
	if h.WorkspaceStore == nil || h.AppState == nil {
		return errorResp(id, CodeInternalError, "workspace stores not configured", nil)
	}
	if _, err := h.WorkspaceStore.GetByID(ctx, p.WorkspaceID); err != nil {
		return errorResp(id, CodeInternalError, "workspace lookup", err)
	}

	var deletedCount int
	if p.Force {
		if ids, err := h.WorkspaceStore.ListSessionIDs(ctx, p.WorkspaceID); err != nil {
			return errorResp(id, CodeInternalError, "workspace session list", err)
		} else if len(ids) > 0 {
			active, _ := h.AppState.GetActiveSession(ctx)
			nonActive := make([]string, 0, len(ids))
			var activeID string
			for _, sid := range ids {
				if sid == active {
					activeID = sid
				} else {
					nonActive = append(nonActive, sid)
				}
			}
			for _, sid := range nonActive {
				if resp := handleDeleteSession(ctx, nil, mustMarshal(map[string]string{"sessionId": sid}), c, h); resp.Error != nil {
					return errorResp(id, CodeInternalError, "cascade delete session", resp.Error)
				}
				deletedCount++
			}
			if activeID != "" {
				if resp := handleDeleteSession(ctx, nil, mustMarshal(map[string]string{"sessionId": activeID}), c, h); resp.Error != nil {
					return errorResp(id, CodeInternalError, "cascade delete active session", resp.Error)
				}
				deletedCount++
			}
		}
	} else {
		if n, err := h.WorkspaceStore.CountSessions(ctx, p.WorkspaceID); err != nil {
			return errorResp(id, CodeInternalError, "workspace session count", err)
		} else if n > 0 {
			return errorResp(id, CodeInvalidParams, "workspace is not empty", nil)
		}
	}

	if err := h.WorkspaceStore.Delete(ctx, p.WorkspaceID); err != nil {
		return errorResp(id, CodeInternalError, "delete workspace", err)
	}
	var next *string
	if cur, err := h.AppState.GetActiveWorkspace(ctx); err == nil && cur == p.WorkspaceID {
		if rows, err := h.WorkspaceStore.List(ctx); err == nil {
			for _, w := range rows {
				if w.ID != p.WorkspaceID {
					next = &w.ID
					break
				}
			}
		}
		_ = h.AppState.SetActiveWorkspace(ctx, nextActiveValue(next))
		_ = h.AppState.SetActiveSession(ctx, "")
	}
	return successResp(id, DeleteWorkspaceResult{
		Deleted:               true,
		NextActiveWorkspaceID: next,
		DeletedSessionCount:   deletedCount,
	})
}

// mustMarshal encodes a payload to JSON or panics (the values are
// hand-built, so a marshal failure is a programming error).
func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// BindSessionWorkspaceParams is the JSON-RPC params for
// agent.bind_session_workspace. Used by the main-side migration that
// attaches legacy sessions to the workspace owning their old directory.
type BindSessionWorkspaceParams struct {
	SessionID   string `json:"sessionId"`
	WorkspaceID string `json:"workspaceId"`
}

// BindSessionWorkspaceResult is the JSON-RPC result for
// agent.bind_session_workspace.
type BindSessionWorkspaceResult struct {
	Bound bool `json:"bound"`
}

// handleBindSessionWorkspace binds an existing session to a workspace.
// Both must exist; the workspace must own a directory (its row exists).
func handleBindSessionWorkspace(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	var p BindSessionWorkspaceParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if p.SessionID == "" || p.WorkspaceID == "" {
		return errorResp(id, CodeInvalidParams, "sessionId and workspaceId are required", nil)
	}
	if h.SessionStore == nil || h.WorkspaceStore == nil {
		return errorResp(id, CodeInternalError, "stores not configured", nil)
	}
	if _, err := h.SessionStore.GetByID(ctx, p.SessionID); err != nil {
		return errorResp(id, CodeInternalError, "session lookup", err)
	}
	if _, err := h.WorkspaceStore.GetByID(ctx, p.WorkspaceID); err != nil {
		return errorResp(id, CodeInternalError, "workspace lookup", err)
	}
	if err := h.SessionStore.BindWorkspace(ctx, p.SessionID, p.WorkspaceID); err != nil {
		return errorResp(id, CodeInternalError, "bind session workspace", err)
	}
	return successResp(id, BindSessionWorkspaceResult{Bound: true})
}

func toWorkspaceWire(w store.Workspace, sessionCount int64) WorkspaceWire {
	label := strings.TrimSpace(w.Name)
	if label == "" {
		label = filepath.Base(w.RootPath)
	}
	return WorkspaceWire{
		ID:             w.ID,
		Name:           w.Name,
		Label:          label,
		RootPath:       w.RootPath,
		SessionCount:   sessionCount,
		DefaultAgentID: w.DefaultAgentID,
		CreatedAt:      w.CreatedAt.UnixMilli(),
		UpdatedAt:      w.UpdatedAt.UnixMilli(),
	}
}

// RenameWorkspaceParams is the JSON-RPC params for agent.rename_workspace.
// name is required after trimming; conflicts with another workspace's
// current name surface as CodeConflict.
type RenameWorkspaceParams struct {
	WorkspaceID string `json:"workspaceId"`
	Name        string `json:"name"`
}

// handleRenameWorkspace updates the user-visible workspace name. Empty
// (after trim) is refused with CodeInvalidParams; colliding with another
// workspace's name returns CodeConflict. The current name being unchanged
// is allowed (idempotent rename).
func handleRenameWorkspace(ctx context.Context, jsonID json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if len(params) == 0 {
		return errorResp(jsonID, CodeInvalidParams, "params required", nil)
	}
	var p RenameWorkspaceParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(jsonID, CodeInvalidParams, "params must be an object", nil)
	}
	if p.WorkspaceID == "" {
		return errorResp(jsonID, CodeInvalidParams, "workspaceId is required", nil)
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return errorResp(jsonID, CodeInvalidParams, "name is required", nil)
	}
	if h.WorkspaceStore == nil {
		return errorResp(jsonID, CodeInternalError, "workspace store not configured", nil)
	}
	if _, err := h.WorkspaceStore.GetByID(ctx, p.WorkspaceID); err != nil {
		return errorResp(jsonID, CodeInternalError, "workspace lookup", err)
	}
	if err := h.WorkspaceStore.UpdateName(ctx, p.WorkspaceID, name); err != nil {
		return errorResp(jsonID, CodeInternalError, "workspace rename", err)
	}
	row, err := h.WorkspaceStore.GetByID(ctx, p.WorkspaceID)
	if err != nil {
		return errorResp(jsonID, CodeInternalError, "workspace reload", err)
	}
	n, err := h.WorkspaceStore.CountSessions(ctx, p.WorkspaceID)
	if err != nil {
		return errorResp(jsonID, CodeInternalError, "workspace session count", err)
	}
	return successResp(jsonID, RenameWorkspaceResult{Workspace: toWorkspaceWire(row, n)})
}

// RenameWorkspaceResult is the JSON-RPC result for agent.rename_workspace.
type RenameWorkspaceResult struct {
	Workspace WorkspaceWire `json:"workspace"`
}

// UpdateWorkspaceRootParams is the JSON-RPC params for
// agent.update_workspace_root. rootPath must be absolute and either already
// exist as a directory or be creatable.
type UpdateWorkspaceRootParams struct {
	WorkspaceID string `json:"workspaceId"`
	RootPath    string `json:"rootPath"`
}

// UpdateWorkspaceRootResult is the JSON-RPC result for
// agent.update_workspace_root.
type UpdateWorkspaceRootResult struct {
	Workspace WorkspaceWire `json:"workspace"`
}

// handleUpdateWorkspaceRoot relocates a workspace to a new directory. The
// new root is created if missing (mkdir -p). After updating the database,
// SetWorkspaceRoot re-binds the sandbox / project skills / tools to the
// new path. A path already owned by a different workspace is refused.
func handleUpdateWorkspaceRoot(ctx context.Context, jsonID json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if len(params) == 0 {
		return errorResp(jsonID, CodeInvalidParams, "params required", nil)
	}
	var p UpdateWorkspaceRootParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(jsonID, CodeInvalidParams, "params must be an object", nil)
	}
	if p.WorkspaceID == "" {
		return errorResp(jsonID, CodeInvalidParams, "workspaceId is required", nil)
	}
	abs := filepath.Clean(p.RootPath)
	if !filepath.IsAbs(abs) {
		return errorResp(jsonID, CodeInvalidParams, "rootPath must be absolute", nil)
	}
	if h.WorkspaceStore == nil {
		return errorResp(jsonID, CodeInternalError, "workspace store not configured", nil)
	}
	if _, err := h.WorkspaceStore.GetByID(ctx, p.WorkspaceID); err != nil {
		return errorResp(jsonID, CodeInternalError, "workspace lookup", err)
	}
	if other, err := h.WorkspaceStore.GetByRoot(ctx, abs); err == nil && other.ID != p.WorkspaceID {
		return errorResp(jsonID, CodeConflict, "rootPath is owned by another workspace", nil)
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return errorResp(jsonID, CodeInternalError, "workspace root path lookup", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return errorResp(jsonID, CodeInternalError, "create workspace dir", err)
	}
	if err := h.WorkspaceStore.UpdateRoot(ctx, p.WorkspaceID, abs); err != nil {
		return errorResp(jsonID, CodeInternalError, "workspace update root", err)
	}
	if h.SetWorkspaceRoot != nil {
		if err := h.SetWorkspaceRoot(abs); err != nil {
			return errorResp(jsonID, CodeInternalError, "rebind sandbox", err)
		}
	}
	updated, err := h.WorkspaceStore.GetByID(ctx, p.WorkspaceID)
	if err != nil {
		return errorResp(jsonID, CodeInternalError, "workspace reload", err)
	}
	n, err := h.WorkspaceStore.CountSessions(ctx, p.WorkspaceID)
	if err != nil {
		return errorResp(jsonID, CodeInternalError, "workspace session count", err)
	}
	return successResp(jsonID, UpdateWorkspaceRootResult{Workspace: toWorkspaceWire(updated, n)})
}
