// Workspace-first flow: create_workspace → set_active_workspace →
// create_session binds → list filters by workspace.

package gateway

import (
	"context"
	"encoding/json"
	"testing"
)

func dispatchJSON(t *testing.T, c *client, method, params string) *Response {
	t.Helper()
	return dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: method,
		Params: json.RawMessage(params),
	}, c, c.handler)
}

// TestHandler_WorkspaceFirstFlow covers the end-to-end ordering: a session
// is created inside a workspace, the workspace's session list only returns
// its own sessions, and the active workspace is persisted.
func TestHandler_WorkspaceFirstFlow(t *testing.T) {
	_, c, _, _, appState := newTestHandlerWithStores(t)
	root := t.TempDir()

	create := dispatchJSON(t, c, "agent.create_workspace",
		`{"name":"项目A","rootPath":"`+root+`"}`)
	if create.Error != nil {
		t.Fatalf("create_workspace error: %+v", create.Error)
	}
	ws := create.Result.(CreateWorkspaceResult).Workspace
	if ws.ID == "" || ws.Label != "项目A" || ws.SessionCount != 0 {
		t.Errorf("unexpected workspace wire: %+v", ws)
	}

	active := dispatchJSON(t, c, "agent.set_active_workspace",
		`{"workspaceId":"`+ws.ID+`"}`)
	if active.Error != nil {
		t.Fatalf("set_active_workspace error: %+v", active.Error)
	}
	if got := active.Result.(SetActiveWorkspaceResult).ActiveSessionID; got != nil {
		t.Errorf("empty workspace ActiveSessionID = %v, want nil", got)
	}

	created := dispatchJSON(t, c, "agent.create_session",
		`{"title":"首会话","workspaceId":"`+ws.ID+`"}`)
	if created.Error != nil {
		t.Fatalf("create_session error: %+v", created.Error)
	}
	sess := created.Result.(CreateSessionResult).Session
	if sess.WorkspaceID != ws.ID {
		t.Errorf("session workspaceId = %q, want %q", sess.WorkspaceID, ws.ID)
	}
	if cur, err := appState.GetActiveSession(context.Background()); err != nil || cur != sess.ID {
		t.Errorf("active session = %q err=%v, want %q", cur, err, sess.ID)
	}

	// 第二个 session 落在同一 workspace。
	created2 := dispatchJSON(t, c, "agent.create_session",
		`{"workspaceId":"`+ws.ID+`"}`)
	if created2.Error != nil {
		t.Fatalf("create_session #2 error: %+v", created2.Error)
	}
	sess2 := created2.Result.(CreateSessionResult).Session

	list := dispatchJSON(t, c, "agent.list_sessions",
		`{"workspaceId":"`+ws.ID+`"}`)
	if list.Error != nil {
		t.Fatalf("list_sessions error: %+v", list.Error)
	}
	got := list.Result.(ListSessionsResult).Sessions
	if len(got) != 2 || got[0].WorkspaceID != ws.ID {
		t.Errorf("workspace sessions = %+v, want 2 bound to %q", got, ws.ID)
	}
	_ = sess2

	wsList := dispatchJSON(t, c, "agent.list_workspaces", `{}`)
	if wsList.Error != nil {
		t.Fatalf("list_workspaces error: %+v", wsList.Error)
	}
	wires := wsList.Result.(ListWorkspacesResult).Workspaces
	if len(wires) != 1 || wires[0].SessionCount != 2 {
		t.Errorf("workspaces = %+v, want 1 with SessionCount 2", wires)
	}
}

// TestHandler_CreateSessionRequiresWorkspace covers the hard gate: with a
// WorkspaceStore wired and no active workspace, create_session is refused.
func TestHandler_CreateSessionRequiresWorkspace(t *testing.T) {
	_, c, _, _, _ := newTestHandlerWithStores(t)

	resp := dispatchJSON(t, c, "agent.create_session", `{}`)
	if resp.Error == nil {
		t.Fatal("create_session without workspace should fail")
	}
	if resp.Error.Code != CodeWorkspaceRequired {
		t.Errorf("error code = %d, want CodeWorkspaceRequired (%d)", resp.Error.Code, CodeWorkspaceRequired)
	}
}
