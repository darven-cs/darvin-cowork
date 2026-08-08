// Workspace, message-save, and imported-file JSON-RPC handlers.

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

	"darvin-cowork/backend/internal/agents/store"
)

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
