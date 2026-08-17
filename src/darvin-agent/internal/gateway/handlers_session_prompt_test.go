// create_session with systemPrompt / identity: wire output and row persistence.

package gateway

import (
	"context"
	"testing"
)

func TestHandler_CreateSessionPersistsPrompt(t *testing.T) {
	_, c, sessStore, _, _ := newTestHandlerWithStores(t)
	root := t.TempDir()

	ws := dispatchJSON(t, c, "agent.create_workspace",
		`{"name":"项目P","rootPath":"`+root+`"}`)
	if ws.Error != nil {
		t.Fatalf("create_workspace error: %+v", ws.Error)
	}
	wsID := ws.Result.(CreateWorkspaceResult).Workspace.ID

	created := dispatchJSON(t, c, "agent.create_session",
		`{"title":"股票助手","workspaceId":"`+wsID+`","systemPrompt":"stock capability","identity":"stock persona"}`)
	if created.Error != nil {
		t.Fatalf("create_session error: %+v", created.Error)
	}
	sess := created.Result.(CreateSessionResult).Session
	if sess.SystemPrompt != "stock capability" || sess.Identity != "stock persona" {
		t.Fatalf("wire prompt = (%q, %q)", sess.SystemPrompt, sess.Identity)
	}

	row, err := sessStore.GetByID(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if row.SystemPrompt != "stock capability" || row.Identity != "stock persona" {
		t.Fatalf("row prompt = (%q, %q)", row.SystemPrompt, row.Identity)
	}

	list := dispatchJSON(t, c, "agent.list_sessions", `{}`)
	if list.Error != nil {
		t.Fatalf("list_sessions error: %+v", list.Error)
	}
	got := list.Result.(ListSessionsResult).Sessions
	if len(got) == 0 || got[0].SystemPrompt != "stock capability" {
		t.Fatalf("list wire missing systemPrompt: %+v", got)
	}
}

func TestHandler_CreateSessionWithoutPrompt(t *testing.T) {
	_, c, sessStore, _, _ := newTestHandlerWithStores(t)
	root := t.TempDir()

	ws := dispatchJSON(t, c, "agent.create_workspace",
		`{"name":"项目P","rootPath":"`+root+`"}`)
	if ws.Error != nil {
		t.Fatalf("create_workspace error: %+v", ws.Error)
	}
	wsID := ws.Result.(CreateWorkspaceResult).Workspace.ID

	created := dispatchJSON(t, c, "agent.create_session",
		`{"workspaceId":"`+wsID+`"}`)
	if created.Error != nil {
		t.Fatalf("create_session error: %+v", created.Error)
	}
	sess := created.Result.(CreateSessionResult).Session
	if sess.SystemPrompt != "" || sess.Identity != "" {
		t.Fatalf("wire prompt = (%q, %q), want empty", sess.SystemPrompt, sess.Identity)
	}

	row, err := sessStore.GetByID(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if row.SystemPrompt != "" || row.Identity != "" {
		t.Fatalf("row prompt = (%q, %q), want empty", row.SystemPrompt, row.Identity)
	}
}
