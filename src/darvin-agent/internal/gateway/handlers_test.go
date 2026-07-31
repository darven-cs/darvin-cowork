package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"darvin-cowork/backend/internal/acp"
	"darvin-cowork/backend/internal/agent"
	"darvin-cowork/backend/internal/agent/event"
	"darvin-cowork/backend/internal/agent/llm"
	"darvin-cowork/backend/internal/agent/session"
	"darvin-cowork/backend/internal/agent/store"
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
	handler := NewHandler(sessions, ledger, steer, nil, nil)
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
func newTestHandlerWithStores(t *testing.T) (*Handler, *client, store.SessionStore, store.MessageStore) {
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
	if err := db.AutoMigrate(&store.Session{}, &store.Message{}, &store.CompactionCheckpoint{}, &store.SkillSnapshot{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	sessStore := store.NewSQLiteStore(db)
	msgStore := store.NewSQLiteMessageStore(db)

	handler := NewHandler(sessions, ledger, steer, sessStore, msgStore)
	client := &client{
		sessions: sessions,
		ledger:   ledger,
		handler:  handler,
		log:      zap.NewNop(),
	}
	return handler, client, sessStore, msgStore
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
	_, c, _, _ := newTestHandlerWithStores(t)
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
	_, c, sessStore, _ := newTestHandlerWithStores(t)
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
	_, c, _, _ := newTestHandlerWithStores(t)
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
	_, c, _, msgStore := newTestHandlerWithStores(t)
	ctx := context.Background()
	if err := msgStore.Save(ctx, &store.MessageRecord{
		ID: "m1", SessionID: "s1", Role: "user", Content: "hi", Timestamp: 1000,
	}); err != nil {
		t.Fatalf("Save m1: %v", err)
	}
	if err := msgStore.Save(ctx, &store.MessageRecord{
		ID: "m2", SessionID: "s1", Role: "assistant", Content: "hello", Timestamp: 1100,
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
	if res.Messages[0].CreatedAt != 1000 {
		t.Errorf("createdAt = %d, want 1000", res.Messages[0].CreatedAt)
	}
}

// TestDispatchGetMessagesRequiresSessionID covers the input validation.
func TestDispatchGetMessagesRequiresSessionID(t *testing.T) {
	_, c, _, _ := newTestHandlerWithStores(t)
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
