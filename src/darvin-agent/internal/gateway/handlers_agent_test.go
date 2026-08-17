// Tests for the agent CRUD handlers and create_session agent derivation.

package gateway

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"gorm.io/gorm"

	agent "darvin-cowork/backend/internal/agents"
	"darvin-cowork/backend/internal/agents/store"
	"darvin-cowork/backend/internal/harness"
	"darvin-cowork/backend/internal/sessionruntime"
	tool "darvin-cowork/backend/internal/tools"
)

// newTestHandlerWithAgents wires the same factory pattern as
// newTestHandlerWithStores plus an AgentStore + WorkspaceStore so the
// agent derivation and CRUD paths run end-to-end.
func newTestHandlerWithAgents(t *testing.T) (*Handler, *client, *store.SQLiteAgentStore, *store.SQLiteWorkspaceStore, store.SessionStore) {
	t.Helper()
	prov := &blockingProvider{}
	memStore := store.NewMemoryStore()
	factory := &sessionruntime.AgentFactory{
		Provider: prov,
		Tools:    tool.NewRegistry(),
		Store:    memStore,
		Logger:   zap.NewNop(),
		Selector: func(a *agent.Agent, _ *sessionruntime.AgentFactory) (harness.Harness, error) {
			return sessionruntime.NewEmbeddedTestHarness(a), nil
		},
	}
	sessions := NewSessionManager(WithAgentFactory(factory))
	ledger := NewEventLedger(zap.NewNop())
	ledger.fakeDelay = 0

	dsn := filepath.Join(t.TempDir(), "agents.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := db.AutoMigrate(&store.Session{}, &store.Workspace{}, &store.Agent{}, &store.Message{}, &store.SessionDigest{}, &store.SkillSnapshot{}, &store.AppState{}, &store.ImportedFile{}, &store.SessionUsage{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	sessStore := store.NewSQLiteStore(db)
	msgStore := store.NewSQLiteMessageStore(db)
	appState := store.NewAppStateStore(db)
	wsStore := store.NewSQLiteWorkspaceStore(db)
	agentStore := store.NewSQLiteAgentStore(db)

	handler := NewHandler(sessions, ledger, sessStore, msgStore, appState,
		HandlerOptions{
			WorkspaceStore: wsStore,
			AgentStore:     agentStore,
		})
	client := &client{
		sessions: sessions,
		ledger:   ledger,
		handler:  handler,
		log:      zap.NewNop(),
	}
	return handler, client, agentStore, wsStore, sessStore
}

func createWorkspaceForAgents(t *testing.T, h *Handler) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "ws")
	resp := handleCreateWorkspace(context.Background(),
		json.RawMessage(`"1"`),
		mustMarshal(map[string]string{"name": "ws", "rootPath": root}),
		h)
	if resp.Error != nil {
		t.Fatalf("create_workspace: %+v", resp.Error)
	}
	res, ok := resp.Result.(CreateWorkspaceResult)
	if !ok {
		t.Fatalf("create_workspace result type: %T", resp.Result)
	}
	if res.Workspace.DefaultAgentID == "" {
		t.Fatal("create_workspace did not bind a default agent")
	}
	return res.Workspace.ID
}

func callCreateSessionWithAgent(t *testing.T, c *client, h *Handler, params string) (SessionWire, *RPCError) {
	t.Helper()
	resp := handleCreateSession(context.Background(),
		json.RawMessage(`"1"`), json.RawMessage(params), c, h)
	if resp.Error != nil {
		return SessionWire{}, resp.Error
	}
	res, ok := resp.Result.(CreateSessionResult)
	if !ok {
		t.Fatalf("create_session result type: %T", resp.Result)
	}
	return res.Session, nil
}

func TestCreateSessionDerivesFromExplicitAgent(t *testing.T) {
	h, c, agentStore, _, _ := newTestHandlerWithAgents(t)
	wsID := createWorkspaceForAgents(t, h)

	rows, err := agentStore.ListByWorkspace(context.Background(), wsID)
	if err != nil || len(rows) == 0 {
		t.Fatalf("ListByWorkspace: %v rows=%d", err, len(rows))
	}
	var translator store.Agent
	for _, r := range rows {
		if r.PresetID == "preset-translator" {
			translator = r
		}
	}
	if translator.ID == "" {
		t.Fatal("translator preset not seeded")
	}

	sess, rpcErr := callCreateSessionWithAgent(t, c, h,
		`{"workspaceId":"`+wsID+`","agentId":"`+translator.ID+`","title":"翻译官"}`)
	if rpcErr != nil {
		t.Fatalf("create_session: %+v", rpcErr)
	}
	if sess.AgentID != translator.ID {
		t.Errorf("session agentId = %q, want %q", sess.AgentID, translator.ID)
	}
	if sess.SystemPrompt != translator.SystemPrompt {
		t.Error("session systemPrompt not derived from agent")
	}
	if sess.Identity != translator.Identity {
		t.Error("session identity not derived from agent")
	}
}

func TestCreateSessionFallsBackToWorkspaceDefault(t *testing.T) {
	h, c, agentStore, _, _ := newTestHandlerWithAgents(t)
	wsID := createWorkspaceForAgents(t, h)

	def, err := agentStore.GetDefaultForWorkspace(context.Background(), wsID)
	if err != nil {
		t.Fatalf("GetDefaultForWorkspace: %v", err)
	}

	sess, rpcErr := callCreateSessionWithAgent(t, c, h, `{"workspaceId":"`+wsID+`"}`)
	if rpcErr != nil {
		t.Fatalf("create_session: %+v", rpcErr)
	}
	if sess.AgentID != def.ID {
		t.Errorf("session agentId = %q, want default %q", sess.AgentID, def.ID)
	}
	if sess.SystemPrompt != def.SystemPrompt {
		t.Error("session systemPrompt not derived from default agent")
	}
}

func TestCreateSessionUnknownAgentFallsBackToParams(t *testing.T) {
	h, c, _, _, _ := newTestHandlerWithAgents(t)
	wsID := createWorkspaceForAgents(t, h)

	sess, rpcErr := callCreateSessionWithAgent(t, c, h,
		`{"workspaceId":"`+wsID+`","agentId":"no-such-agent","systemPrompt":"P","identity":"I"}`)
	if rpcErr != nil {
		t.Fatalf("create_session: %+v", rpcErr)
	}
	if sess.AgentID != "" {
		t.Errorf("unknown agent still bound: %q", sess.AgentID)
	}
	if sess.SystemPrompt != "P" || sess.Identity != "I" {
		t.Errorf("params fallback not applied: %q/%q", sess.SystemPrompt, sess.Identity)
	}
}

func TestCreateSessionRejectsCrossWorkspaceAgent(t *testing.T) {
	h, c, agentStore, _, _ := newTestHandlerWithAgents(t)
	wsA := createWorkspaceForAgents(t, h)
	wsB := createWorkspaceForAgents(t, h)

	// Pick a preset row from workspace B.
	rows, _ := agentStore.ListByWorkspace(context.Background(), wsB)
	if len(rows) == 0 {
		t.Fatal("workspace B has no agents")
	}
	foreign := rows[0]

	_, rpcErr := callCreateSessionWithAgent(t, c, h,
		`{"workspaceId":"`+wsA+`","agentId":"`+foreign.ID+`"}`)
	if rpcErr == nil {
		t.Fatal("cross-workspace agent accepted")
	}
}

func TestDeleteAgentGuards(t *testing.T) {
	ctx := context.Background()
	h, _, agentStore, _, _ := newTestHandlerWithAgents(t)
	wsID := createWorkspaceForAgents(t, h)

	rows, _ := agentStore.ListByWorkspace(ctx, wsID)
	if len(rows) == 0 {
		t.Fatal("no agents seeded")
	}
	var presetRow, defRow store.Agent
	var userRow store.Agent
	for _, r := range rows {
		if r.PresetID == "preset-translator" {
			presetRow = r
		}
	}
	defRow, err := agentStore.GetDefaultForWorkspace(ctx, wsID)
	if err != nil {
		t.Fatalf("GetDefaultForWorkspace: %v", err)
	}
	userRow, err = agentStore.Create(ctx, store.Agent{Name: "自定义", WorkspaceID: wsID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	for name, id := range map[string]string{"preset": presetRow.ID, "default": defRow.ID} {
		resp := handleDeleteAgent(ctx, json.RawMessage(`"1"`),
			mustMarshal(map[string]string{"agentId": id}), h)
		if resp.Error == nil {
			t.Errorf("delete %s agent succeeded, want rejection", name)
		}
	}

	resp := handleDeleteAgent(ctx, json.RawMessage(`"1"`),
		mustMarshal(map[string]string{"agentId": userRow.ID}), h)
	if resp.Error != nil {
		t.Fatalf("delete user agent: %+v", resp.Error)
	}
}

func TestUpdateDefaultAgentFlow(t *testing.T) {
	ctx := context.Background()
	h, _, agentStore, _, _ := newTestHandlerWithAgents(t)
	wsID := createWorkspaceForAgents(t, h)

	userAgent, err := agentStore.Create(ctx, store.Agent{
		Name: "自定义", WorkspaceID: wsID, SystemPrompt: "UP", Identity: "UI",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	resp := handleUpdateDefaultAgent(ctx, json.RawMessage(`"1"`),
		mustMarshal(map[string]string{"workspaceId": wsID, "defaultAgentId": userAgent.ID}), h)
	if resp.Error != nil {
		t.Fatalf("update_default_agent: %+v", resp.Error)
	}
	res, ok := resp.Result.(UpdateDefaultAgentResult)
	if !ok {
		t.Fatalf("result type: %T", resp.Result)
	}
	if res.Workspace.DefaultAgentID != userAgent.ID {
		t.Errorf("workspace defaultAgentId = %q, want %q", res.Workspace.DefaultAgentID, userAgent.ID)
	}

	def, _ := agentStore.GetDefaultForWorkspace(ctx, wsID)
	if def.ID != userAgent.ID {
		t.Errorf("store default = %q, want %q", def.ID, userAgent.ID)
	}

	// New sessions without agentId now derive from the new default.
	c := &client{sessions: h.Sessions, ledger: h.Ledger, handler: h, log: zap.NewNop()}
	sess, rpcErr := callCreateSessionWithAgent(t, c, h, `{"workspaceId":"`+wsID+`"}`)
	if rpcErr != nil {
		t.Fatalf("create_session: %+v", rpcErr)
	}
	if sess.AgentID != userAgent.ID {
		t.Errorf("session agentId = %q, want new default %q", sess.AgentID, userAgent.ID)
	}
	if sess.SystemPrompt != "UP" {
		t.Errorf("session systemPrompt = %q, want UP", sess.SystemPrompt)
	}
}

func TestListAndGetAgents(t *testing.T) {
	ctx := context.Background()
	h, _, _, _, _ := newTestHandlerWithAgents(t)
	wsID := createWorkspaceForAgents(t, h)

	resp := handleListAgents(ctx, json.RawMessage(`"1"`),
		mustMarshal(map[string]string{"workspaceId": wsID}), h)
	if resp.Error != nil {
		t.Fatalf("list_agents: %+v", resp.Error)
	}
	res, ok := resp.Result.(ListAgentsResult)
	if !ok {
		t.Fatalf("result type: %T", resp.Result)
	}
	// 9 presets + Main Agent default
	if len(res.Agents) != 10 {
		t.Errorf("list_agents returned %d, want 10", len(res.Agents))
	}

	first := res.Agents[0]
	resp2 := handleGetAgent(ctx, json.RawMessage(`"1"`),
		mustMarshal(map[string]string{"agentId": first.ID}), h)
	if resp2.Error != nil {
		t.Fatalf("get_agent: %+v", resp2.Error)
	}
	got, ok := resp2.Result.(GetAgentResult)
	if !ok {
		t.Fatalf("result type: %T", resp2.Result)
	}
	if got.Agent.ID != first.ID {
		t.Errorf("get_agent = %q, want %q", got.Agent.ID, first.ID)
	}
	if got.Agent.SkillIDs == nil {
		t.Error("skillIds not decoded to array on wire")
	}
}

func TestCreateAgentFromPreset(t *testing.T) {
	ctx := context.Background()
	h, _, _, _, _ := newTestHandlerWithAgents(t)
	wsID := createWorkspaceForAgents(t, h)

	resp := handleCreateAgent(ctx, json.RawMessage(`"1"`),
		mustMarshal(map[string]any{
			"workspaceId": wsID, "name": "我的翻译官", "fromPresetId": "preset-translator",
		}), h)
	if resp.Error != nil {
		t.Fatalf("create_agent: %+v", resp.Error)
	}
	res, ok := resp.Result.(CreateAgentResult)
	if !ok {
		t.Fatalf("result type: %T", resp.Result)
	}
	if res.Agent.Source != "user" {
		t.Errorf("source = %q, want user", res.Agent.Source)
	}
	if res.Agent.PresetID != "preset-translator" {
		t.Errorf("presetId = %q, want preset-translator", res.Agent.PresetID)
	}
	if res.Agent.SystemPromptEn == "" {
		t.Error("fromPresetId copy missed systemPromptEn")
	}

	// Unknown preset id is rejected.
	resp2 := handleCreateAgent(ctx, json.RawMessage(`"1"`),
		mustMarshal(map[string]any{
			"workspaceId": wsID, "name": "x", "fromPresetId": "preset-nope",
		}), h)
	if resp2.Error == nil {
		t.Error("unknown fromPresetId accepted")
	}
}
