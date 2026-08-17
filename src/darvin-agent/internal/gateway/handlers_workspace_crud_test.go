// Focused tests for workspace CRUD handlers: rename_workspace,
// update_workspace_root, and force-cascading delete_workspace. Each
// test pins one observable contract: happy path, input validation,
// uniqueness/conflict, sandbox re-anchor, cascade outcome.

package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func dispatchCreateWorkspace(c *client, name, root string) (WorkspaceWire, *Response) {
	resp := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`),
		Method: "agent.create_workspace",
		Params: json.RawMessage(`{"name":"` + name + `","rootPath":"` + root + `"}`),
	}, c, c.handler)
	if resp.Error != nil {
		return WorkspaceWire{}, resp
	}
	return resp.Result.(CreateWorkspaceResult).Workspace, resp
}

func dispatchRename(c *client, id, name string) *Response {
	return dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`),
		Method: "agent.rename_workspace",
		Params: json.RawMessage(`{"workspaceId":"` + id + `","name":"` + name + `"}`),
	}, c, c.handler)
}

func dispatchUpdateRoot(c *client, id, root string) *Response {
	return dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`),
		Method: "agent.update_workspace_root",
		Params: json.RawMessage(`{"workspaceId":"` + id + `","rootPath":"` + root + `"}`),
	}, c, c.handler)
}

func dispatchCreateSession(c *client, workspaceID string) (string, *Response) {
	resp := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`),
		Method: "agent.create_session",
		Params: json.RawMessage(`{"workspaceId":"` + workspaceID + `"}`),
	}, c, c.handler)
	if resp.Error != nil {
		return "", resp
	}
	return resp.Result.(CreateSessionResult).Session.ID, resp
}

// TestHandler_RenameWorkspace_UpdatesLabelAndCount: happy path also
// confirms SessionCount is recomputed from the live store (the rename
// handler is the one place that refreshes the count after a write).
func TestHandler_RenameWorkspace_UpdatesLabelAndCount(t *testing.T) {
	_, c, _, _, _ := newTestHandlerWithStores(t)
	ctx := context.Background()
	w, err := dispatchCreateWorkspace(c, "old", t.TempDir())
	if err.Error != nil {
		t.Fatalf("setup: %+v", err.Error)
	}
	// Two sessions so the re-count is meaningful.
	for range []int{1, 2} {
		sid, r := dispatchCreateSession(c, w.ID)
		if r.Error != nil {
			t.Fatalf("create_session: %+v", r.Error)
		}
		if sid == "" {
			t.Fatal("create_session returned empty id")
		}
	}

	resp := dispatchRename(c, w.ID, "new")
	if resp.Error != nil {
		t.Fatalf("rename: %+v", resp.Error)
	}
	res := resp.Result.(RenameWorkspaceResult).Workspace
	if res.Name != "new" {
		t.Errorf("Name = %q, want new", res.Name)
	}
	if res.Label != "new" {
		t.Errorf("Label = %q, want new (name wins over basename)", res.Label)
	}
	if res.SessionCount != 2 {
		t.Errorf("SessionCount = %d, want 2 (re-counted after rename)", res.SessionCount)
	}
	if res.ID != w.ID {
		t.Errorf("ID = %q, want %q", res.ID, w.ID)
	}
	// DB row is updated.
	row, lookupErr := c.handler.WorkspaceStore.GetByID(ctx, w.ID)
	if lookupErr != nil {
		t.Fatalf("get: %v", err)
	}
	if row.Name != "new" {
		t.Errorf("row.Name = %q, want new", row.Name)
	}
}

// TestHandler_RenameWorkspace_RejectsEmptyName: trim-whitespace empty
// inputs must surface as CodeInvalidParams without mutating the row.
func TestHandler_RenameWorkspace_RejectsEmptyName(t *testing.T) {
	_, c, _, _, _ := newTestHandlerWithStores(t)
	ctx := context.Background()
	w, _ := dispatchCreateWorkspace(c, "valid", t.TempDir())

	for _, blank := range []string{"", "   ", "\t\n"} {
		resp := dispatchRename(c, w.ID, blank)
		if resp.Error == nil {
			t.Fatalf("blank %q should be rejected", blank)
		}
		if resp.Error.Code != CodeInvalidParams {
			t.Errorf("blank %q code = %d, want CodeInvalidParams", blank, resp.Error.Code)
		}
	}
	row, lookupErr := c.handler.WorkspaceStore.GetByID(ctx, w.ID)
	if lookupErr != nil {
		t.Fatalf("get: %v", lookupErr)
	}
	if row.Name != "valid" {
		t.Errorf("row.Name = %q, want valid (rejected renames must not write)", row.Name)
	}
}

// TestHandler_RenameWorkspace_IdempotentSameName: renaming to the
// current value is allowed; the wire preserves the identity.
func TestHandler_RenameWorkspace_IdempotentSameName(t *testing.T) {
	_, c, _, _, _ := newTestHandlerWithStores(t)
	w, _ := dispatchCreateWorkspace(c, "stable", t.TempDir())

	resp := dispatchRename(c, w.ID, "stable")
	if resp.Error != nil {
		t.Fatalf("idempotent rename rejected: %+v", resp.Error)
	}
	res := resp.Result.(RenameWorkspaceResult).Workspace
	if res.Name != "stable" || res.ID != w.ID {
		t.Errorf("rename wire = %+v, want name preserved", res)
	}
}

// TestHandler_UpdateWorkspaceRoot_RejectsRelativePath: the absolute-path
// contract must be enforced before any DB write.
func TestHandler_UpdateWorkspaceRoot_RejectsRelativePath(t *testing.T) {
	_, c, _, _, _ := newTestHandlerWithStores(t)
	ctx := context.Background()
	oldRoot := t.TempDir()
	w, _ := dispatchCreateWorkspace(c, "rel", oldRoot)

	resp := dispatchUpdateRoot(c, w.ID, "not/absolute")
	if resp.Error == nil {
		t.Fatal("relative rootPath should be rejected")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("code = %d, want CodeInvalidParams", resp.Error.Code)
	}
	row, lookupErr := c.handler.WorkspaceStore.GetByID(ctx, w.ID)
	if lookupErr != nil {
		t.Fatalf("get: %v", lookupErr)
	}
	if row.RootPath != oldRoot {
		t.Errorf("RootPath mutated to %q, want %q", row.RootPath, oldRoot)
	}
}

// TestHandler_UpdateWorkspaceRoot_ConflictsWithOtherWorkspace: two
// workspaces cannot share a RootPath.
func TestHandler_UpdateWorkspaceRoot_ConflictsWithOtherWorkspace(t *testing.T) {
	_, c, _, _, _ := newTestHandlerWithStores(t)
	rootA := t.TempDir()
	rootB := t.TempDir()
	wA, _ := dispatchCreateWorkspace(c, "A", rootA)
	dispatchCreateWorkspace(c, "B", rootB)

	resp := dispatchUpdateRoot(c, wA.ID, rootB)
	if resp.Error == nil {
		t.Fatal("updating to another workspace's root should fail")
	}
	if resp.Error.Code != CodeConflict {
		t.Errorf("code = %d, want CodeConflict", resp.Error.Code)
	}
}

// TestHandler_UpdateWorkspaceRoot_CreatesMissingDirectory: a brand-new
// directory under an existing parent is created by the handler.
func TestHandler_UpdateWorkspaceRoot_CreatesMissingDirectory(t *testing.T) {
	_, c, _, _, _ := newTestHandlerWithStores(t)
	parent := t.TempDir()
	fresh := filepath.Join(parent, "new", "deeper")
	w, _ := dispatchCreateWorkspace(c, "mk", parent)

	resp := dispatchUpdateRoot(c, w.ID, fresh)
	if resp.Error != nil {
		t.Fatalf("update_root: %+v", resp.Error)
	}
	res := resp.Result.(UpdateWorkspaceRootResult).Workspace
	if res.RootPath != fresh {
		t.Errorf("RootPath = %q, want %q", res.RootPath, fresh)
	}
	info, err := os.Stat(fresh)
	if err != nil {
		t.Fatalf("expected %q to exist, stat err = %v", fresh, err)
	}
	if !info.IsDir() {
		t.Errorf("%q exists but is not a directory", fresh)
	}
}

// TestHandler_DeleteWorkspace_Force_CascadesAndCounts: force=true
// deletes every session in the workspace and reports the count, while
// force=false on a non-empty workspace is refused.
func TestHandler_DeleteWorkspace_Force_CascadesAndCounts(t *testing.T) {
	_, c, _, _, _ := newTestHandlerWithStores(t)
	ctx := context.Background()
	w, err := dispatchCreateWorkspace(c, "cascade", t.TempDir())
	if err.Error != nil {
		t.Fatalf("setup: %+v", err.Error)
	}
	for range []int{1, 2} {
		if _, r := dispatchCreateSession(c, w.ID); r.Error != nil {
			t.Fatalf("setup session: %+v", r.Error)
		}
	}

	// force=false refuses non-empty.
	refused := dispatchRequest(ctx, &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"r"`),
		Method: "agent.delete_workspace",
		Params: json.RawMessage(`{"workspaceId":"` + w.ID + `"}`),
	}, c, c.handler)
	if refused.Error == nil {
		t.Fatal("force=false should refuse non-empty workspace")
	}
	if refused.Error.Code != CodeInvalidParams {
		t.Errorf("refuse code = %d, want CodeInvalidParams", refused.Error.Code)
	}

	// force=true cascades.
	forced := dispatchRequest(ctx, &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"f"`),
		Method: "agent.delete_workspace",
		Params: json.RawMessage(`{"workspaceId":"` + w.ID + `","force":true}`),
	}, c, c.handler)
	if forced.Error != nil {
		t.Fatalf("force delete: %+v", forced.Error)
	}
	res := forced.Result.(DeleteWorkspaceResult)
	if !res.Deleted {
		t.Errorf("Deleted = false, want true")
	}
	if res.DeletedSessionCount != 2 {
		t.Errorf("DeletedSessionCount = %d, want 2", res.DeletedSessionCount)
	}
	// No sessions left under this workspace.
	remaining, lookupErr := c.handler.SessionStore.ListByWorkspace(ctx, w.ID)
	if lookupErr != nil {
		t.Fatalf("ListByWorkspace: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("remaining = %d, want 0", len(remaining))
	}
	// Workspace row gone.
	if _, err := c.handler.WorkspaceStore.GetByID(ctx, w.ID); err == nil {
		t.Errorf("workspace %q still present after force delete", w.ID)
	}
}

// TestHandler_DeleteWorkspace_Force_ClearsActiveSession: the cascade
// must end with active_session_id cleared so the AppState never points
// at a deleted session.
func TestHandler_DeleteWorkspace_Force_ClearsActiveSession(t *testing.T) {
	_, c, _, _, _ := newTestHandlerWithStores(t)
	ctx := context.Background()
	w, _ := dispatchCreateWorkspace(c, "active-cascade", t.TempDir())
	sid, _ := dispatchCreateSession(c, w.ID)

	switchResp := dispatchRequest(ctx, &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"s"`),
		Method: "agent.set_active_session",
		Params: json.RawMessage(`{"sessionId":"` + sid + `"}`),
	}, c, c.handler)
	if switchResp.Error != nil {
		t.Fatalf("set_active_session: %+v", switchResp.Error)
	}

	resp := dispatchRequest(ctx, &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"x"`),
		Method: "agent.delete_workspace",
		Params: json.RawMessage(`{"workspaceId":"` + w.ID + `","force":true}`),
	}, c, c.handler)
	if resp.Error != nil {
		t.Fatalf("force delete: %+v", resp.Error)
	}

	got, lookupErr := c.handler.AppState.GetActiveSession(ctx)
	if lookupErr != nil {
		t.Fatalf("GetActiveSession: %v", lookupErr)
	}
	if got != "" {
		t.Errorf("active_session after cascade = %q, want empty", got)
	}
	// The session is truly gone.
	if _, err := c.handler.SessionStore.GetByID(ctx, sid); err == nil {
		t.Errorf("deleted session %q still present", sid)
	}
}

// TestHandler_RenameWorkspace_LeavesUnrelatedWorkspacesAlone ensures the
// rename handler targets exactly the named workspace and does not touch
// siblings in the store.
func TestHandler_RenameWorkspace_LeavesUnrelatedWorkspacesAlone(t *testing.T) {
	_, c, _, _, _ := newTestHandlerWithStores(t)
	wA, _ := dispatchCreateWorkspace(c, "A", t.TempDir())
	wB, _ := dispatchCreateWorkspace(c, "B", t.TempDir())

	resp := dispatchRename(c, wA.ID, "A-renamed")
	if resp.Error != nil {
		t.Fatalf("rename: %+v", resp.Error)
	}
	ctx := context.Background()
	rowB, lookupErr := c.handler.WorkspaceStore.GetByID(ctx, wB.ID)
	if lookupErr != nil {
		t.Fatalf("get B: %v", lookupErr)
	}
	if rowB.Name != "B" {
		t.Errorf("sibling B name mutated to %q, want B", rowB.Name)
	}
}