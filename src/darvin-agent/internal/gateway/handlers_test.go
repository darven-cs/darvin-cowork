package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"darvin-cowork/backend/internal/acp"
	"darvin-cowork/backend/internal/agents"
	"darvin-cowork/backend/internal/agents/event"
	"darvin-cowork/backend/internal/llm"
	"darvin-cowork/backend/internal/agents/session"
	"darvin-cowork/backend/internal/agents/store"
	"darvin-cowork/backend/internal/agents/tool"
	"darvin-cowork/backend/internal/skills"
)

// newTestHandler wires factory + SessionManager + SteerControl so handlers
// run against production code paths. prompt 路径走 factory 懒建 AcpSession,
// steer 路径接一个仅供 SteerControl 持有的 steerAgent(spec §1.3 非目标)。
func newTestHandler(t *testing.T) (*Handler, *client) {
	t.Helper()
	prov := &blockingProvider{}
	store := store.NewMemoryStore()
	factory := &acp.AgentFactory{
		Provider: prov,
		Store:    store,
		Logger:   zap.NewNop(),
	}
	steerAgent, err := agent.New(agent.NewAgentConfig{
		Session:  session.NewSession("steer-placeholder"),
		Provider: prov,
		Store:    store,
	})
	if err != nil {
		t.Fatalf("agent.New steer: %v", err)
	}
	steer := acp.NewSteerControl(steerAgent)
	sessions := NewSessionManager(WithAgentFactory(factory))
	ledger := NewEventLedger(zap.NewNop())
	ledger.fakeDelay = 0
	handler := NewHandler(sessions, ledger, steer, nil, nil, nil)
	client := &client{
		sessions: sessions,
		ledger:   ledger,
		handler:  handler,
		log:      zap.NewNop(),
	}
	return handler, client
}

var idRe21 = regexp.MustCompile(`^[A-Za-z0-9]{21}$`)

// newTestHandlerWithStores builds the same wiring as newTestHandler but
// additionally plumbs in a real SQLite SessionStore + MessageStore so the
// list_sessions / get_messages handlers can be exercised end-to-end.
func newTestHandlerWithStores(t *testing.T) (*Handler, *client, store.SessionStore, store.MessageStore, *store.AppStateStore) {
	t.Helper()
	prov := &blockingProvider{}
	memStore := store.NewMemoryStore()
	factory := &acp.AgentFactory{
		Provider: prov,
		Store:    memStore,
		Logger:   zap.NewNop(),
	}
	steerAgent, err := agent.New(agent.NewAgentConfig{
		Session:  session.NewSession("steer-placeholder"),
		Provider: prov,
		Store:    memStore,
	})
	if err != nil {
		t.Fatalf("agent.New steer: %v", err)
	}
	steer := acp.NewSteerControl(steerAgent)
	sessions := NewSessionManager(WithAgentFactory(factory))
	ledger := NewEventLedger(zap.NewNop())
	ledger.fakeDelay = 0

	dsn := filepath.Join(t.TempDir(), "sessions.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := db.AutoMigrate(&store.Session{}, &store.Message{}, &store.CompactionCheckpoint{}, &store.SkillSnapshot{}, &store.AppState{}, &store.ImportedFile{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	sessStore := store.NewSQLiteStore(db)
	msgStore := store.NewSQLiteMessageStore(db)
	appState := store.NewAppStateStore(db)
	ifs := store.NewImportedFileStore(db)

	handler := NewHandler(sessions, ledger, steer, sessStore, msgStore, appState,
		HandlerOptions{ImportedFiles: ifs, WorkspaceRoot: t.TempDir()})
	client := &client{
		sessions: sessions,
		ledger:   ledger,
		handler:  handler,
		log:      zap.NewNop(),
	}
	return handler, client, sessStore, msgStore, appState
}

func TestDispatchPrompt(t *testing.T) {
	_, c := newTestHandler(t)
	req := &Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"1"`),
		Method:  "agent.prompt",
		Params:  json.RawMessage(`{"content":"hi"}`),
	}
	resp := dispatchRequest(context.Background(), req, c, c.handler)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	res, ok := resp.Result.(PromptResult)
	if !ok {
		t.Fatalf("result type: %T", resp.Result)
	}
	// sessionId is the Agent's own session so the caller can subscribe to
	// it; messageId is a fresh 21-char nanoid minted by acp.Loop.
	if res.SessionID != DefaultSessionID || !idRe21.MatchString(res.MessageID) {
		t.Fatalf("id shape: %+v", res)
	}
}

func TestDispatchPromptMissingContent(t *testing.T) {
	_, c := newTestHandler(t)
	req := &Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"2"`),
		Method:  "agent.prompt",
		Params:  json.RawMessage(`{}`),
	}
	resp := dispatchRequest(context.Background(), req, c, c.handler)
	if resp.Error == nil || resp.Error.Code != CodeInvalidParams {
		t.Fatalf("expected invalid params, got %+v", resp)
	}
}

func TestDispatchPromptEmptyContent(t *testing.T) {
	_, c := newTestHandler(t)
	req := &Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"3"`),
		Method:  "agent.prompt",
		Params:  json.RawMessage(`{"content":"   "}`),
	}
	resp := dispatchRequest(context.Background(), req, c, c.handler)
	if resp.Error == nil || resp.Error.Code != CodeInvalidParams {
		t.Fatalf("expected invalid params, got %+v", resp)
	}
}

// TestDispatchPromptAcceptsDefaultSessionID covers the single-session
// guard from the accepting side: an explicit sessionId is allowed only
// when it equals SessionManager.DefaultID(), and it resolves to the same
// session an omitted sessionId would.
func TestDispatchPromptAcceptsDefaultSessionID(t *testing.T) {
	_, c := newTestHandler(t)
	req := &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.prompt",
		Params: json.RawMessage(`{"content":"hi","sessionId":"` + DefaultSessionID + `"}`),
	}
	resp := dispatchRequest(context.Background(), req, c, c.handler)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	res := resp.Result.(PromptResult)
	if res.SessionID != DefaultSessionID {
		t.Fatalf("sessionId = %q, want %q", res.SessionID, DefaultSessionID)
	}
	if !c.sessions.Has(res.SessionID) {
		t.Fatalf("expected session %q to be registered", res.SessionID)
	}
}

// TestDispatchPromptThenSubscribeRoutes is the end-to-end id contract:
// the sessionId agent.prompt returns must be acceptable to
// agent.subscribe_events, otherwise events emitted by the run could never
// reach the client.
func TestDispatchPromptThenSubscribeRoutes(t *testing.T) {
	_, c := newTestHandler(t)
	pr := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.prompt",
		Params: json.RawMessage(`{"content":"hi"}`),
	}, c, c.handler)
	sid := pr.Result.(PromptResult).SessionID

	sr := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"2"`), Method: "agent.subscribe_events",
		Params: json.RawMessage(`{"sessionId":"` + sid + `"}`),
	}, c, c.handler)
	if sr.Error != nil {
		t.Fatalf("subscribe with the sessionId prompt returned: %+v", sr.Error)
	}
	// The subscribed id has to match what the Agent stamps on its events;
	// newTestHandler binds the Agent to DefaultSessionID for exactly that
	// reason, mirroring main.go.
	if sid != DefaultSessionID {
		t.Fatalf("prompt sessionId = %q, want the agent's session %q", sid, DefaultSessionID)
	}
}

// TestDispatchPromptAcceptsArbitrarySessionID verifies the handler
// resolves a non-default sessionId through SessionManager.GetOrCreate
// (i.e. the session is allocated on first prompt) rather than rejecting
// it like the previous single-session guard did.
func TestDispatchPromptAcceptsArbitrarySessionID(t *testing.T) {
	_, c := newTestHandler(t)
	req := &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.prompt",
		Params: json.RawMessage(`{"content":"hi","sessionId":"new-session"}`),
	}
	resp := dispatchRequest(context.Background(), req, c, c.handler)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	res := resp.Result.(PromptResult)
	if res.SessionID != "new-session" {
		t.Fatalf("sessionId = %q, want %q", res.SessionID, "new-session")
	}
	if !c.sessions.Has(res.SessionID) {
		t.Fatalf("expected session %q to be registered", res.SessionID)
	}
}

// TestDispatchPromptHonoursCallerRunID verifies the runId the caller
// passes back on PromptResult so the renderer can pin aborts to it.
func TestDispatchPromptHonoursCallerRunID(t *testing.T) {
	_, c := newTestHandler(t)
	req := &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.prompt",
		Params: json.RawMessage(`{"content":"hi","runId":"caller-run"}`),
	}
	resp := dispatchRequest(context.Background(), req, c, c.handler)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	res := resp.Result.(PromptResult)
	if res.RunID != "caller-run" {
		t.Fatalf("runId = %q, want %q", res.RunID, "caller-run")
	}
}

func TestDispatchUnknownMethod(t *testing.T) {
	_, c := newTestHandler(t)
	req := &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"x"`), Method: "no.such",
	}
	resp := dispatchRequest(context.Background(), req, c, c.handler)
	if resp.Error == nil || resp.Error.Code != CodeMethodNotFound {
		t.Fatalf("expected method not found, got %+v", resp)
	}
}

func TestDispatchAbort(t *testing.T) {
	_, c := newTestHandler(t)
	req := &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.abort",
		Params: json.RawMessage(`{"sessionId":"any","runId":"r1"}`),
	}
	resp := dispatchRequest(context.Background(), req, c, c.handler)
	if resp.Error == nil || resp.Error.Code != CodeInvalidParams {
		t.Fatalf("expected invalid params for unknown session, got %+v", resp)
	}
}

func TestDispatchAbortMissingRunID(t *testing.T) {
	_, c := newTestHandler(t)
	req := &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.abort",
		Params: json.RawMessage(`{"sessionId":"any"}`),
	}
	resp := dispatchRequest(context.Background(), req, c, c.handler)
	if resp.Error == nil || resp.Error.Code != CodeInvalidParams {
		t.Fatalf("expected invalid params when runId missing, got %+v", resp)
	}
}

func TestDispatchAbortKnownSessionMismatch(t *testing.T) {
	_, c := newTestHandler(t)
	c.sessions.GetOrCreateEntry("any")
	req := &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.abort",
		Params: json.RawMessage(`{"sessionId":"any","runId":"r1"}`),
	}
	resp := dispatchRequest(context.Background(), req, c, c.handler)
	if resp.Error == nil || resp.Error.Code != CodeInvalidParams {
		t.Fatalf("expected invalid params for run mismatch, got %+v", resp)
	}
}

// TestDispatchSubscribeEventsCreatesUnknownSession 覆盖 main 端的真实路径：
// SessionStore 先落 UUID 再发 subscribe_events；此时 SessionManager 还没见
// 过这个 id，subscribe 应自创建 entry 而不是拒绝。
func TestDispatchSubscribeEventsCreatesUnknownSession(t *testing.T) {
	_, c := newTestHandler(t)
	req := &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.subscribe_events",
		Params: json.RawMessage(`{"sessionId":"main-minted-uuid"}`),
	}
	resp := dispatchRequest(context.Background(), req, c, c.handler)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	res, _ := resp.Result.(SubscribeEventsResult)
	if !res.Subscribed {
		t.Fatalf("expected subscribed=true, got %+v", res)
	}
	if !c.sessions.Has("main-minted-uuid") {
		t.Fatalf("subscribe should auto-register the previously-unknown session")
	}
}

func TestDispatchSubscribeEventsSuccess(t *testing.T) {
	_, c := newTestHandler(t)
	pr := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.prompt",
		Params: json.RawMessage(`{"content":"x"}`),
	}, c, c.handler)
	sid := pr.Result.(PromptResult).SessionID

	resp := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"2"`), Method: "agent.subscribe_events",
		Params: json.RawMessage(`{"sessionId":"` + sid + `"}`),
	}, c, c.handler)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	res, _ := resp.Result.(SubscribeEventsResult)
	if !res.Subscribed {
		t.Fatalf("expected subscribed=true, got %+v", res)
	}
}

func TestDispatchSteer(t *testing.T) {
	_, c := newTestHandler(t)
	req := &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.steer",
		Params: json.RawMessage(`{"content":"redirect"}`),
	}
	resp := dispatchRequest(context.Background(), req, c, c.handler)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	res, _ := resp.Result.(SteerResult)
	if !res.Steered {
		t.Fatalf("expected steered=true, got %+v", res)
	}
}

// TestDispatchListSessionsEmpty verifies list_sessions returns an empty
// list (not nil, not an error) when the store is fresh.
func TestDispatchListSessionsEmpty(t *testing.T) {
	_, c, _, _, _ := newTestHandlerWithStores(t)
	resp := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.list_sessions",
	}, c, c.handler)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	res, _ := resp.Result.(ListSessionsResult)
	if res.Sessions == nil {
		t.Error("Sessions is nil; want empty slice for stable JSON shape")
	}
	if len(res.Sessions) != 0 {
		t.Errorf("len(Sessions) = %d, want 0", len(res.Sessions))
	}
}

// TestDispatchListSessionsSeeded saves two sessions through the store
// and confirms list_sessions surfaces both, with updatedAt preserved.
func TestDispatchListSessionsSeeded(t *testing.T) {
	_, c, sessStore, _, _ := newTestHandlerWithStores(t)
	ctx := context.Background()
	a := session.NewSession("a")
	b := session.NewSession("b")
	if err := sessStore.Save(ctx, a); err != nil {
		t.Fatalf("Save a: %v", err)
	}
	if err := sessStore.Save(ctx, b); err != nil {
		t.Fatalf("Save b: %v", err)
	}
	resp := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.list_sessions",
	}, c, c.handler)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	res, _ := resp.Result.(ListSessionsResult)
	if len(res.Sessions) != 2 {
		t.Fatalf("len = %d, want 2", len(res.Sessions))
	}
	got := map[string]bool{}
	for _, s := range res.Sessions {
		got[s.ID] = true
		if s.UpdatedAt == 0 {
			t.Errorf("session %s has zero UpdatedAt", s.ID)
		}
	}
	if !got["a"] || !got["b"] {
		t.Errorf("missing session(s); got %v", got)
	}
}

// TestDispatchGetMessagesEmpty returns an empty list for a session with
// no messages (no error).
func TestDispatchGetMessagesEmpty(t *testing.T) {
	_, c, _, _, _ := newTestHandlerWithStores(t)
	resp := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.get_messages",
		Params: json.RawMessage(`{"sessionId":"any"}`),
	}, c, c.handler)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	res, _ := resp.Result.(GetMessagesResult)
	if res.Messages == nil {
		t.Error("Messages is nil; want empty slice for stable JSON shape")
	}
	if len(res.Messages) != 0 {
		t.Errorf("len(Messages) = %d, want 0", len(res.Messages))
	}
}

// TestDispatchGetMessagesSeeded writes two messages via the store and
// confirms get_messages replays them in timestamp order.
func TestDispatchGetMessagesSeeded(t *testing.T) {
	_, c, _, msgStore, _ := newTestHandlerWithStores(t)
	ctx := context.Background()
	if err := msgStore.Save(ctx, &store.MessageRecord{
		ID: "m1", SessionID: "s1", Role: "user", Content: "hi", Timestamp: 1000, Done: true,
	}); err != nil {
		t.Fatalf("Save m1: %v", err)
	}
	if err := msgStore.Save(ctx, &store.MessageRecord{
		ID: "m2", SessionID: "s1", Role: "assistant", Content: "hello", Timestamp: 1100, Done: true,
	}); err != nil {
		t.Fatalf("Save m2: %v", err)
	}

	resp := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.get_messages",
		Params: json.RawMessage(`{"sessionId":"s1"}`),
	}, c, c.handler)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	res, _ := resp.Result.(GetMessagesResult)
	if len(res.Messages) != 2 {
		t.Fatalf("len = %d, want 2", len(res.Messages))
	}
	if res.Messages[0].ID != "m1" || res.Messages[1].ID != "m2" {
		t.Errorf("order = [%s, %s], want [m1, m2]",
			res.Messages[0].ID, res.Messages[1].ID)
	}
	if !res.Messages[0].Done || !res.Messages[1].Done {
		t.Error("persisted messages must report Done=true")
	}
	if res.Messages[0].Role != "user" || res.Messages[1].Role != "assistant" {
		t.Errorf("role mapping wrong: %+v", res.Messages)
	}
	// get_messages is a transparent MessageRecord passthrough now; the
	// field is Timestamp (JSON tag createdAt).
	if res.Messages[0].Timestamp != 1000 {
		t.Errorf("Timestamp = %d, want 1000", res.Messages[0].Timestamp)
	}
}

// TestDispatchGetMessagesRequiresSessionID covers the input validation.
func TestDispatchGetMessagesRequiresSessionID(t *testing.T) {
	_, c, _, _, _ := newTestHandlerWithStores(t)
	resp := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.get_messages",
		Params: json.RawMessage(`{}`),
	}, c, c.handler)
	if resp.Error == nil || resp.Error.Code != CodeInvalidParams {
		t.Fatalf("expected invalid params, got %+v", resp)
	}
}

// TestDispatchListGetMessagesNilStores covers the graceful-degradation
// path: a Handler with nil stores still answers list_sessions and
// get_messages with empty slices instead of panicking. This matches
// the unit-test fast path (no DB) and is what keeps the dispatch
// layer safe even if main.go forgets to wire a store.
func TestDispatchListGetMessagesNilStores(t *testing.T) {
	_, c := newTestHandler(t)
	resp := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.list_sessions",
	}, c, c.handler)
	if resp.Error != nil {
		t.Fatalf("list_sessions: %+v", resp.Error)
	}
	if res, _ := resp.Result.(ListSessionsResult); res.Sessions == nil {
		t.Error("Sessions nil with nil store")
	}

	resp = dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"2"`), Method: "agent.get_messages",
		Params: json.RawMessage(`{"sessionId":"x"}`),
	}, c, c.handler)
	if resp.Error != nil {
		t.Fatalf("get_messages: %+v", resp.Error)
	}
	if res, _ := resp.Result.(GetMessagesResult); res.Messages == nil {
		t.Error("Messages nil with nil store")
	}
}

// TestHandlePrompt_RoutesBySessionID:同 WS 连接上先后给 A、B 发 prompt,
// 两条 prompt 落到各自 AcpSession.Loop,A/B 的 ActiveRunID 互不干扰。
func TestHandlePrompt_RoutesBySessionID(t *testing.T) {
	_, c := newTestHandler(t)
	respA := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.prompt",
		Params: json.RawMessage(`{"content":"hi","sessionId":"a"}`),
	}, c, c.handler)
	if respA.Error != nil {
		t.Fatalf("prompt a: %+v", respA.Error)
	}
	resA := respA.Result.(PromptResult)
	if resA.SessionID != "a" {
		t.Fatalf("a sessionId = %q, want \"a\"", resA.SessionID)
	}

	respB := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"2"`), Method: "agent.prompt",
		Params: json.RawMessage(`{"content":"hi","sessionId":"b"}`),
	}, c, c.handler)
	if respB.Error != nil {
		t.Fatalf("prompt b: %+v", respB.Error)
	}
	resB := respB.Result.(PromptResult)
	if resB.SessionID != "b" {
		t.Fatalf("b sessionId = %q, want \"b\"", resB.SessionID)
	}

	entryA, _ := c.sessions.GetOrCreateEntry("a")
	entryB, _ := c.sessions.GetOrCreateEntry("b")
	subA := entryA.Acp.Agent.Subscribe(8)
	subB := entryB.Acp.Agent.Subscribe(8)
	defer subA.Unsubscribe()
	defer subB.Unsubscribe()
	waitForSubEvent(t, subA)
	waitForSubEvent(t, subB)
	if got := entryA.Acp.Loop.ActiveRunID(); got == "" {
		t.Errorf("a ActiveRunID is empty; expected in-flight")
	}
	if got := entryB.Acp.Loop.ActiveRunID(); got == "" {
		t.Errorf("b ActiveRunID is empty; expected in-flight")
	}
	if entryA.Acp == entryB.Acp {
		t.Fatalf("a and b share AcpSession — per-session isolation broken")
	}
}

// TestHandleAbort_RoutesBySessionIDAndRunID:abort A 不影响 B。
func TestHandleAbort_RoutesBySessionIDAndRunID(t *testing.T) {
	_, c := newTestHandler(t)
	respA := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.prompt",
		Params: json.RawMessage(`{"content":"hi","sessionId":"a","runId":"run-a"}`),
	}, c, c.handler)
	if respA.Error != nil {
		t.Fatalf("prompt a: %+v", respA.Error)
	}
	respB := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"2"`), Method: "agent.prompt",
		Params: json.RawMessage(`{"content":"hi","sessionId":"b","runId":"run-b"}`),
	}, c, c.handler)
	if respB.Error != nil {
		t.Fatalf("prompt b: %+v", respB.Error)
	}

	entryA, _ := c.sessions.GetOrCreateEntry("a")
	entryB, _ := c.sessions.GetOrCreateEntry("b")
	subA := entryA.Acp.Agent.Subscribe(8)
	subB := entryB.Acp.Agent.Subscribe(8)
	defer subA.Unsubscribe()
	defer subB.Unsubscribe()
	waitForSubEvent(t, subA)
	waitForSubEvent(t, subB)

	abortResp := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"3"`), Method: "agent.abort",
		Params: json.RawMessage(`{"sessionId":"a","runId":"run-a"}`),
	}, c, c.handler)
	if abortResp.Error != nil {
		t.Fatalf("abort a: %+v", abortResp.Error)
	}

	waitForCondition(t, func() bool { return entryA.Acp.Loop.ActiveRunID() == "" })
	if got := entryB.Acp.Loop.ActiveRunID(); got != "run-b" {
		t.Fatalf("b ActiveRunID = %q, want \"run-b\" (must NOT be cancelled)", got)
	}
}

// TestHandlePrompt_QueuedForActiveSession:同 session 在跑时再发 prompt,
// 第二条进 followUpQueue,响应 Queued=true;第一条完成后第二条起跑。
func TestHandlePrompt_QueuedForActiveSession(t *testing.T) {
	_, c := newTestHandler(t)
	first := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.prompt",
		Params: json.RawMessage(`{"content":"first","sessionId":"a"}`),
	}, c, c.handler)
	if first.Error != nil {
		t.Fatalf("first prompt: %+v", first.Error)
	}
	if first.Result.(PromptResult).Queued {
		t.Fatalf("first prompt should not be queued")
	}
	entryA, _ := c.sessions.GetOrCreateEntry("a")
	subA := entryA.Acp.Agent.Subscribe(8)
	defer subA.Unsubscribe()
	waitForSubEvent(t, subA)

	second := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"2"`), Method: "agent.prompt",
		Params: json.RawMessage(`{"content":"second","sessionId":"a"}`),
	}, c, c.handler)
	if second.Error != nil {
		t.Fatalf("second prompt: %+v", second.Error)
	}
	if !second.Result.(PromptResult).Queued {
		t.Fatalf("second prompt must report Queued=true (active run still in flight)")
	}
}

// TestHandleSubscribeEvents_BuildsEntryNotAcp:FR-8 两阶段。subscribe
// 只建 SessionEntry,不触发 AcpSession 懒建 —— 否则 renderer 订历史
// session 时会拉起 N 个 Agent / Loop / 订阅。检查走 byID 直读而不是
// GetOrCreateEntry,后者会触发"阶段 2"补建,掩盖 subscribe 自己的行为。
func TestHandleSubscribeEvents_BuildsEntryNotAcp(t *testing.T) {
	_, c := newTestHandler(t)
	resp := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.subscribe_events",
		Params: json.RawMessage(`{"sessionId":"never-prompted"}`),
	}, c, c.handler)
	if resp.Error != nil {
		t.Fatalf("subscribe: %+v", resp.Error)
	}
	if !c.sessions.Has("never-prompted") {
		t.Fatalf("expected SessionEntry created for the unknown id")
	}
	c.sessions.mu.Lock()
	entry := c.sessions.byID["never-prompted"]
	c.sessions.mu.Unlock()
	if entry == nil {
		t.Fatalf("entry vanished from byID")
	}
	if entry.Acp != nil {
		t.Fatalf("subscribe must NOT trigger AcpSession build (FR-8 two-phase); got Acp=%+v", entry.Acp)
	}
}

// waitForSubEvent 等订阅拿到任意事件,证明 Loop.run goroutine 已取出
// 请求并设了 activeRun。
func waitForSubEvent(t *testing.T, sub *event.Subscription) {
	t.Helper()
	select {
	case <-sub.C():
	case <-time.After(2 * time.Second):
		t.Fatal("agent did not start running")
	}
}

// TestHandler_CreateSession covers agent.create_session: it walks the
// factory lazy-build path, returns a session carrying the caller title,
// and persists the new session as active in app_state.
func TestHandler_CreateSession(t *testing.T) {
	ctx := context.Background()
	_, c, _, _, appState := newTestHandlerWithStores(t)

	resp := dispatchRequest(ctx, &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.create_session",
		Params: json.RawMessage(`{"title":"我的会话"}`),
	}, c, c.handler)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	res := resp.Result.(CreateSessionResult)
	if !idRe21.MatchString(res.Session.ID) {
		t.Errorf("session id = %q, want 21-char nanoid", res.Session.ID)
	}
	if res.Session.Title != "我的会话" {
		t.Errorf("Title = %q, want 我的会话", res.Session.Title)
	}
	if !c.sessions.Has(res.Session.ID) {
		t.Errorf("session %q not registered in SessionManager", res.Session.ID)
	}
	active, err := appState.GetActiveSession(ctx)
	if err != nil {
		t.Fatalf("GetActiveSession: %v", err)
	}
	if active != res.Session.ID {
		t.Errorf("active = %q, want %q (create_session persists active)", active, res.Session.ID)
	}
}

// TestHandler_ListSessionsReturnsTitle covers agent.list_sessions returning
// the renderer-facing Title (not the old empty SessionSummary shape).
func TestHandler_ListSessionsReturnsTitle(t *testing.T) {
	ctx := context.Background()
	_, c, sessStore, _, _ := newTestHandlerWithStores(t)

	if err := sessStore.Save(ctx, session.NewSession("a")); err != nil {
		t.Fatalf("Save a: %v", err)
	}
	if err := sessStore.UpdateTitle(ctx, "a", "kubernetes 排障"); err != nil {
		t.Fatalf("UpdateTitle a: %v", err)
	}
	if err := sessStore.Save(ctx, session.NewSession("b")); err != nil {
		t.Fatalf("Save b: %v", err)
	}

	resp := dispatchRequest(ctx, &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.list_sessions",
	}, c, c.handler)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	res := resp.Result.(ListSessionsResult)
	if len(res.Sessions) != 2 {
		t.Fatalf("len = %d, want 2", len(res.Sessions))
	}
	byID := map[string]string{}
	for _, s := range res.Sessions {
		byID[s.ID] = s.Title
	}
	if byID["a"] != "kubernetes 排障" {
		t.Errorf("title for a = %q, want kubernetes 排障", byID["a"])
	}
	if byID["b"] == "" {
		t.Errorf("title for b is empty; want non-empty default")
	}
}

// TestHandler_DeleteSessionAdvancesActive covers the active-advance rule:
// deleting the active session returns the next list entry and persists it;
// deleting the last session returns null and clears active.
func TestHandler_DeleteSessionAdvancesActive(t *testing.T) {
	ctx := context.Background()
	_, c, sessStore, _, appState := newTestHandlerWithStores(t)

	for _, id := range []string{"a", "b"} {
		if err := sessStore.Save(ctx, session.NewSession(id)); err != nil {
			t.Fatalf("Save %s: %v", id, err)
		}
	}
	if err := appState.SetActiveSession(ctx, "a"); err != nil {
		t.Fatalf("SetActiveSession a: %v", err)
	}

	delA := dispatchRequest(ctx, &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.delete_session",
		Params: json.RawMessage(`{"sessionId":"a"}`),
	}, c, c.handler)
	if delA.Error != nil {
		t.Fatalf("delete a: %+v", delA.Error)
	}
	resA := delA.Result.(DeleteSessionResult)
	if !resA.Deleted {
		t.Error("Deleted = false, want true")
	}
	if resA.NextActiveSessionID == nil || *resA.NextActiveSessionID != "b" {
		t.Errorf("NextActiveSessionID = %v, want b (next list entry)", resA.NextActiveSessionID)
	}
	if active, _ := appState.GetActiveSession(ctx); active != "b" {
		t.Errorf("active after delete a = %q, want b", active)
	}

	delB := dispatchRequest(ctx, &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"2"`), Method: "agent.delete_session",
		Params: json.RawMessage(`{"sessionId":"b"}`),
	}, c, c.handler)
	if delB.Error != nil {
		t.Fatalf("delete b: %+v", delB.Error)
	}
	resB := delB.Result.(DeleteSessionResult)
	if resB.NextActiveSessionID != nil {
		t.Errorf("NextActiveSessionID after deleting last = %v, want null", resB.NextActiveSessionID)
	}
	if active, _ := appState.GetActiveSession(ctx); active != "" {
		t.Errorf("active after deleting last = %q, want cleared", active)
	}
}

// TestHandler_RenameUpdatesTitle covers agent.rename_session: a non-empty
// title is persisted; an empty / whitespace title falls back to 新建会话.
func TestHandler_RenameUpdatesTitle(t *testing.T) {
	ctx := context.Background()
	_, c, sessStore, _, _ := newTestHandlerWithStores(t)

	if err := sessStore.Save(ctx, session.NewSession("a")); err != nil {
		t.Fatalf("Save a: %v", err)
	}

	// Empty title → fallback.
	empty := dispatchRequest(ctx, &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.rename_session",
		Params: json.RawMessage(`{"sessionId":"a","title":"   "}`),
	}, c, c.handler)
	if empty.Error != nil {
		t.Fatalf("rename empty: %+v", empty.Error)
	}
	if got := empty.Result.(RenameSessionResult).Session.Title; got != "新建会话" {
		t.Errorf("Title after empty rename = %q, want 新建会话", got)
	}
	if row, _ := sessStore.GetByID(ctx, "a"); row.Title != "新建会话" {
		t.Errorf("persisted Title = %q, want 新建会话", row.Title)
	}

	// Real rename.
	named := dispatchRequest(ctx, &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"2"`), Method: "agent.rename_session",
		Params: json.RawMessage(`{"sessionId":"a","title":"生产排障"}`),
	}, c, c.handler)
	if named.Error != nil {
		t.Fatalf("rename named: %+v", named.Error)
	}
	if got := named.Result.(RenameSessionResult).Session.Title; got != "生产排障" {
		t.Errorf("Title after rename = %q, want 生产排障", got)
	}
	if row, _ := sessStore.GetByID(ctx, "a"); row.Title != "生产排障" {
		t.Errorf("persisted Title = %q, want 生产排障", row.Title)
	}
}

// TestHandler_SearchReturnsBothBuckets covers agent.search_sessions: title
// hits land in sessions, content hits land in messages (with the owning
// session title). Empty query returns empty.
func TestHandler_SearchReturnsBothBuckets(t *testing.T) {
	ctx := context.Background()
	_, c, sessStore, msgStore, _ := newTestHandlerWithStores(t)

	if err := sessStore.Save(ctx, session.NewSession("a")); err != nil {
		t.Fatalf("Save a: %v", err)
	}
	if err := sessStore.Save(ctx, session.NewSession("b")); err != nil {
		t.Fatalf("Save b: %v", err)
	}
	if err := sessStore.UpdateTitle(ctx, "a", "kubernetes 排障"); err != nil {
		t.Fatalf("UpdateTitle a: %v", err)
	}
	if err := sessStore.UpdateTitle(ctx, "b", "日常闲聊"); err != nil {
		t.Fatalf("UpdateTitle b: %v", err)
	}
	if err := msgStore.Save(ctx, &store.MessageRecord{
		ID: "m1", SessionID: "b", Role: "assistant", Content: "kubernetes 集群排障", Timestamp: 1000,
	}); err != nil {
		t.Fatalf("Save message: %v", err)
	}

	resp := dispatchRequest(ctx, &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.search_sessions",
		Params: json.RawMessage(`{"query":"kubernetes"}`),
	}, c, c.handler)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	res := resp.Result.(SearchSessionsResult)
	if len(res.Sessions) != 1 || res.Sessions[0].ID != "a" {
		t.Errorf("sessions bucket = %+v, want [a]", res.Sessions)
	}
	if len(res.Messages) != 1 {
		t.Fatalf("messages bucket len = %d, want 1", len(res.Messages))
	}
	if res.Messages[0].Message.SessionID != "b" {
		t.Errorf("hit SessionID = %q, want b", res.Messages[0].Message.SessionID)
	}
	if res.Messages[0].SessionTitle != "日常闲聊" {
		t.Errorf("hit SessionTitle = %q, want 日常闲聊", res.Messages[0].SessionTitle)
	}

	// Empty query → both buckets empty.
	emptyResp := dispatchRequest(ctx, &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"2"`), Method: "agent.search_sessions",
		Params: json.RawMessage(`{"query":"   "}`),
	}, c, c.handler)
	if emptyResp.Error != nil {
		t.Fatalf("empty search: %+v", emptyResp.Error)
	}
	empty := emptyResp.Result.(SearchSessionsResult)
	if len(empty.Sessions) != 0 || len(empty.Messages) != 0 {
		t.Errorf("empty query returned %d sessions / %d messages, want 0/0",
			len(empty.Sessions), len(empty.Messages))
	}
}

// blockingProvider emits one TextDelta then blocks until ctx fires. The
// spawned Run goroutine in each test exits when the test process dies;
// the goroutine leak is acceptable for a short-lived unit test.
type blockingProvider struct {
	mu sync.Mutex
}

func (b *blockingProvider) Name() string { return "blocking" }
func (b *blockingProvider) Complete(_ context.Context, _ *llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return nil, errors.New("not implemented")
}
func (b *blockingProvider) Stream(ctx context.Context, _ *llm.CompletionRequest) (*llm.StreamingResponse, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan llm.StreamEvent, 1)
	ch <- llm.TextDeltaEvent{Delta: "x"}
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return llm.NewStreamingResponse(ch, nil), nil
}

// newWorkspaceTestHandler builds a handler with a real SQLite store set plus
// an ImportedFileStore bound to a known workspace root, returning the root so
// tests can stage files.
func newWorkspaceTestHandler(t *testing.T) (*Handler, *client, string) {
	t.Helper()
	root := t.TempDir()
	dsn := filepath.Join(t.TempDir(), "sessions.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := db.AutoMigrate(&store.Session{}, &store.Message{}, &store.ImportedFile{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	sessStore := store.NewSQLiteStore(db)
	msgStore := store.NewSQLiteMessageStore(db)
	ifs := store.NewImportedFileStore(db)
	ledger := NewEventLedger(zap.NewNop())
	ledger.fakeDelay = 0
	handler := NewHandler(NewSessionManager(), ledger, nil, sessStore, msgStore, nil,
		HandlerOptions{ImportedFiles: ifs, WorkspaceRoot: root})
	c := &client{sessions: handler.Sessions, ledger: ledger, handler: handler, log: zap.NewNop()}
	return handler, c, root
}

func TestHandleSaveMessageWorkspaceEvent(t *testing.T) {
	h, c, _ := newWorkspaceTestHandler(t)
	resp := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.save_message",
		Params: json.RawMessage(`{"sessionId":"s1","content":"[系统] 用户导入了文件: a.md","meta":{"tag":"workspace_event"}}`),
	}, c, h)
	if resp.Error != nil {
		t.Fatalf("save_message: %+v", resp.Error)
	}
	rows, err := h.MessageStore.List(context.Background(), "s1", 0, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("List: rows=%d err=%v", len(rows), err)
	}
	if rows[0].Role != "system" {
		t.Errorf("role = %q, want system", rows[0].Role)
	}
}

func TestHandleSaveMessageDefaultRole(t *testing.T) {
	h, c, _ := newWorkspaceTestHandler(t)
	resp := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.save_message",
		Params: json.RawMessage(`{"sessionId":"s1","content":"hello"}`),
	}, c, h)
	if resp.Error != nil {
		t.Fatalf("save_message: %+v", resp.Error)
	}
	rows, _ := h.MessageStore.List(context.Background(), "s1", 0, 0)
	if rows[0].Role != "user" {
		t.Errorf("role = %q, want user", rows[0].Role)
	}
}

func TestHandleImportFilesHappyPath(t *testing.T) {
	h, c, root := newWorkspaceTestHandler(t)
	src := filepath.Join(root, "spec.md")
	if err := os.WriteFile(src, []byte("# hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	resp := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.import_files",
		Params: json.RawMessage(`{"sessionId":"s1","sourcePaths":["` + src + `"],"workspaceRelPaths":["spec.md"],"shas":["abc"],"sizes":[5],"originalNames":["spec.md"]}`),
	}, c, h)
	if resp.Error != nil {
		t.Fatalf("import_files: %+v", resp.Error)
	}
	res := resp.Result.(ImportFilesResult)
	if len(res.Imported) != 1 || len(res.Skipped) != 0 {
		t.Fatalf("imported=%d skipped=%d, want 1/0", len(res.Imported), len(res.Skipped))
	}
	if res.Imported[0].RelativePath != "spec.md" {
		t.Errorf("relativePath = %q, want spec.md", res.Imported[0].RelativePath)
	}
}

func TestHandleImportFilesPathTraversal(t *testing.T) {
	h, c, root := newWorkspaceTestHandler(t)
	_ = root
	resp := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.import_files",
		Params: json.RawMessage(`{"sessionId":"s1","sourcePaths":["/etc/passwd"],"workspaceRelPaths":["passwd"],"shas":["x"],"sizes":[1],"originalNames":["passwd"]}`),
	}, c, h)
	if resp.Error != nil {
		t.Fatalf("import_files: %+v", resp.Error)
	}
	res := resp.Result.(ImportFilesResult)
	if len(res.Imported) != 0 || len(res.Skipped) != 1 || res.Skipped[0].Reason != "path_escapes" {
		t.Fatalf("imported=%d skipped=%v, want 0/1 path_escapes", len(res.Imported), res.Skipped)
	}
}

func TestHandleImportFilesWorkspaceFull(t *testing.T) {
	h, c, root := newWorkspaceTestHandler(t)
	half := store.MaxWorkspaceBytes / 2
	if _, err := h.ImportedFiles.Insert(context.Background(), store.ImportedFile{
		ID: "seed", SessionID: "s1", OriginalName: "seed.bin",
		RelativePath: "seed.bin", Size: half, Sha256: "sha-seed",
	}); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(root, "big.bin")
	if err := os.WriteFile(big, make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}
	// size half+1 pushes the session (already holding half) over the cap.
	over := half + 1
	resp := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.import_files",
		Params: json.RawMessage(`{"sessionId":"s1","sourcePaths":["` + big + `"],"workspaceRelPaths":["big.bin"],"shas":["sha-big"],"sizes":[` + strconv.FormatInt(over, 10) + `],"originalNames":["big.bin"]}`),
	}, c, h)
	if resp.Error != nil {
		t.Fatalf("import_files: %+v", resp.Error)
	}
	res := resp.Result.(ImportFilesResult)
	if len(res.Skipped) != 1 || res.Skipped[0].Reason != "workspace_full" {
		t.Fatalf("skipped=%v, want workspace_full", res.Skipped)
	}
}

func TestHandleListImportedFilesAndWorkspaceInfo(t *testing.T) {
	h, c, root := newWorkspaceTestHandler(t)
	src := filepath.Join(root, "a.md")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.import_files",
		Params: json.RawMessage(`{"sessionId":"s1","sourcePaths":["` + src + `"],"workspaceRelPaths":["a.md"],"shas":["sha-a"],"sizes":[1],"originalNames":["a.md"]}`),
	}, c, h)

	resp := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"2"`), Method: "agent.list_imported_files",
		Params: json.RawMessage(`{"sessionId":"s1"}`),
	}, c, h)
	if resp.Error != nil {
		t.Fatalf("list_imported_files: %+v", resp.Error)
	}
	list := resp.Result.(ListImportedFilesResult)
	if len(list.Files) != 1 || list.WorkspaceBytes != 1 {
		t.Errorf("files=%d bytes=%d, want 1/1", len(list.Files), list.WorkspaceBytes)
	}

	resp = dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"3"`), Method: "agent.get_workspace_info",
		Params: json.RawMessage(`{"sessionId":"s1"}`),
	}, c, h)
	if resp.Error != nil {
		t.Fatalf("get_workspace_info: %+v", resp.Error)
	}
	if info := resp.Result.(GetWorkspaceInfoResult); info.WorkspaceBytes != 1 {
		t.Errorf("workspaceBytes = %d, want 1", info.WorkspaceBytes)
	}
}

func TestHandleRemoveImportedFile(t *testing.T) {
	h, c, root := newWorkspaceTestHandler(t)
	src := filepath.Join(root, "a.md")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.import_files",
		Params: json.RawMessage(`{"sessionId":"s1","sourcePaths":["` + src + `"],"workspaceRelPaths":["a.md"],"shas":["sha-a"],"sizes":[1],"originalNames":["a.md"]}`),
	}, c, h)

	resp := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"2"`), Method: "agent.remove_imported_file",
		Params: json.RawMessage(`{"sessionId":"s1","relPath":"a.md"}`),
	}, c, h)
	if resp.Error != nil {
		t.Fatalf("remove_imported_file: %+v", resp.Error)
	}
	if !resp.Result.(RemoveImportedFileResult).Removed {
		t.Error("expected removed=true")
	}
	rows, _ := h.ImportedFiles.List(context.Background(), "s1")
	if len(rows) != 0 {
		t.Errorf("rows after remove = %d, want 0", len(rows))
	}
}

func TestHandleListSkillsEmptyWhenNotConfigured(t *testing.T) {
	h, c := newTestHandler(t)
	resp := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.skills.list",
	}, c, h)
	if resp.Error != nil {
		t.Fatalf("list: %+v", resp.Error)
	}
	r := resp.Result.(ListSkillsResult)
	if len(r.Skills) != 0 {
		t.Errorf("len(skills) = %d, want 0 when registry missing", len(r.Skills))
	}
}

func TestHandleSkillsListSetEnabledAndBootstrap(t *testing.T) {
	h, c := newTestHandler(t)
	reg := skillsForTest(t)
	h.Skills = reg
	h.Ledger = c.ledger
	h.Log = zap.NewNop()

	listResp := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.skills.list",
	}, c, h)
	if listResp.Error != nil {
		t.Fatalf("list: %+v", listResp.Error)
	}
	initial := listResp.Result.(ListSkillsResult)
	if len(initial.Skills) != 1 {
		t.Fatalf("len(skills) = %d, want 1", len(initial.Skills))
	}
	if !initial.Skills[0].Enabled {
		t.Error("seeded skill should be enabled by default")
	}

	setResp := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"2"`), Method: "agent.skills.set_enabled",
		Params: json.RawMessage(`{"skillId":"code-review","enabled":false}`),
	}, c, h)
	if setResp.Error != nil {
		t.Fatalf("set_enabled: %+v", setResp.Error)
	}
	if !setResp.Result.(SetSkillEnabledResult).OK {
		t.Error("set_enabled ok=false")
	}
	entry, _ := reg.Get("code-review")
	if entry.Enabled {
		t.Error("registry entry still enabled")
	}

	bootResp := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"3"`), Method: "agent.skills.bootstrap",
		Params: json.RawMessage(`{"skills":[{"id":"code-review","name":"Code Review","description":"review src","enabled":true,"isOfficial":true,"isBuiltIn":true,"path":"embedded","source":"bundled","updatedAt":1}]}`),
	}, c, h)
	if bootResp.Error != nil {
		t.Fatalf("bootstrap: %+v", bootResp.Error)
	}
	if !bootResp.Result.(BootstrapSkillsResult).OK {
		t.Error("bootstrap ok=false")
	}
	entry, _ = reg.Get("code-review")
	if !entry.Enabled {
		t.Error("bootstrap should have re-enabled the skill")
	}
}

func TestHandleSetSkillEnabledMissing(t *testing.T) {
	h, c := newTestHandler(t)
	h.Skills = skillsForTest(t)
	h.Ledger = c.ledger
	h.Log = zap.NewNop()
	resp := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.skills.set_enabled",
		Params: json.RawMessage(`{"skillId":"nope","enabled":true}`),
	}, c, h)
	if resp.Error == nil {
		t.Fatal("expected error for unknown skillId")
	}
}

// skillsForTest returns a registry seeded with one bundled skill entry so
// the agent.skills.* handlers can be exercised without touching the disk
// or the //go:embed bundled FS.
func skillsForTest(t *testing.T) *skills.SkillRegistry {
	t.Helper()
	reg := skills.NewSkillRegistry()
	src := &skillsForTestSource{}
	if err := reg.Load(context.Background(), []skills.SkillSourceLoader{src}); err != nil {
		t.Fatal(err)
	}
	return reg
}

type skillsForTestSource struct{}

func (s *skillsForTestSource) LoadAll(_ context.Context) ([]*skills.SkillEntry, error) {
	return []*skills.SkillEntry{{
		ID:          "code-review",
		Name:        "code-review",
		Description: "review code",
		Source:      skills.SkillSourceBundled,
		Enabled:     true,
		IsBuiltIn:   true,
		IsOfficial:  true,
	}}, nil
}

type stubGatewayTool struct{ name string }

func (s *stubGatewayTool) Name() string                    { return s.name }
func (s *stubGatewayTool) Description() string             { return "stub tool" }
func (s *stubGatewayTool) Parameters() llm.ParameterSchema { return llm.ParameterSchema{Type: "object"} }
func (s *stubGatewayTool) Execute(_ context.Context, _ map[string]any) tool.Result {
	return tool.Result{}
}

type stubGatewayPlugin struct {
	pluginID string
	tool     *stubGatewayTool
}

func (p *stubGatewayPlugin) PluginID() string { return p.pluginID }
func (p *stubGatewayPlugin) Register(reg tool.ToolRegistrar) error {
	return reg.RegisterTool(p.tool, tool.KindSkill, map[string]any{
		"pluginID": p.pluginID,
		"skillID":  "test-skill",
	})
}
func (p *stubGatewayPlugin) Unregister(reg tool.ToolRegistrar) error {
	return reg.UnregisterByPlugin(p.pluginID)
}

// newTestHandlerWithPlugins wires the same session manager as
// newTestHandler but injects plugins into the agent factory.
func newTestHandlerWithPlugins(t *testing.T, plugins []tool.Plugin) (*Handler, *client) {
	t.Helper()
	prov := &blockingProvider{}
	store := store.NewMemoryStore()
	factory := &acp.AgentFactory{
		Provider: prov,
		Store:    store,
		Logger:   zap.NewNop(),
		Plugins:  plugins,
	}
	steerAgent, err := agent.New(agent.NewAgentConfig{
		Session:  session.NewSession("steer-placeholder"),
		Provider: prov,
		Store:    store,
	})
	if err != nil {
		t.Fatalf("agent.New steer: %v", err)
	}
	steer := acp.NewSteerControl(steerAgent)
	sessions := NewSessionManager(WithAgentFactory(factory))
	ledger := NewEventLedger(zap.NewNop())
	ledger.fakeDelay = 0
	handler := NewHandler(sessions, ledger, steer, nil, nil, nil)
	client := &client{
		sessions: sessions,
		ledger:   ledger,
		handler:  handler,
		log:      zap.NewNop(),
	}
	return handler, client
}

func hasToolWire(tools []ToolDescriptorWire, name string) bool {
	for _, td := range tools {
		if td.Name == name {
			return true
		}
	}
	return false
}

func TestHandleListTools_IncludesPluginTools(t *testing.T) {
	_, c := newTestHandlerWithPlugins(t, []tool.Plugin{
		&stubGatewayPlugin{pluginID: "skill", tool: &stubGatewayTool{name: "skill:test-skill"}},
	})
	req := &Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"1"`),
		Method:  "agent.tools.list",
		Params:  json.RawMessage(`{}`),
	}
	resp := dispatchRequest(context.Background(), req, c, c.handler)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	res := resp.Result.(ListToolsResult)
	found := false
	for _, td := range res.Tools {
		if td.Name == "skill:test-skill" {
			found = true
			if td.Kind != "skill" {
				t.Errorf("kind = %q, want skill", td.Kind)
			}
			if td.Metadata["skillID"] != "test-skill" {
				t.Errorf("Metadata[skillID] = %v, want test-skill", td.Metadata["skillID"])
			}
		}
	}
	if !found {
		t.Errorf("skill:test-skill missing from tools: %+v", res.Tools)
	}
}

func TestHandleListTools_NoSessions(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, nil, nil)
	c := &client{handler: h, log: zap.NewNop()}
	req := &Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"1"`),
		Method:  "agent.tools.list",
		Params:  json.RawMessage(`{}`),
	}
	resp := dispatchRequest(context.Background(), req, c, h)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if res := resp.Result.(ListToolsResult); len(res.Tools) != 0 {
		t.Errorf("tools = %+v, want empty", res.Tools)
	}
}

func TestSetSkillEnabled_RefreshTools(t *testing.T) {
	reg := skillsForTest(t)
	runner := skills.NewSkillRunner(reg, tool.NewRegistry())
	plugin := skills.NewSkillPlugin(reg, runner)
	_, c := newTestHandlerWithPlugins(t, []tool.Plugin{plugin})
	c.handler.Skills = reg

	listReq := &Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"1"`),
		Method:  "agent.tools.list",
		Params:  json.RawMessage(`{}`),
	}
	resp := dispatchRequest(context.Background(), listReq, c, c.handler)
	if resp.Error != nil {
		t.Fatalf("list error: %+v", resp.Error)
	}
	if !hasToolWire(resp.Result.(ListToolsResult).Tools, "skill:code-review") {
		t.Fatal("skill:code-review missing before disable")
	}

	disableReq := &Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"2"`),
		Method:  "agent.skills.set_enabled",
		Params:  json.RawMessage(`{"skillId":"code-review","enabled":false}`),
	}
	resp = dispatchRequest(context.Background(), disableReq, c, c.handler)
	if resp.Error != nil {
		t.Fatalf("set_enabled error: %+v", resp.Error)
	}

	resp = dispatchRequest(context.Background(), listReq, c, c.handler)
	if resp.Error != nil {
		t.Fatalf("list error after disable: %+v", resp.Error)
	}
	if hasToolWire(resp.Result.(ListToolsResult).Tools, "skill:code-review") {
		t.Error("skill:code-review still present after disable")
	}
}
