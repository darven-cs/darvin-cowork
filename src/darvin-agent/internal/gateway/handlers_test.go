package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sync"
	"testing"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/acp"
	"darvin-cowork/backend/internal/agent"
	"darvin-cowork/backend/internal/agent/llm"
	"darvin-cowork/backend/internal/agent/session"
)

// newTestHandler wires a real Agent + acp.Loop + acp.SteerControl on top
// of a mock provider so handlers run against production code paths. The
// provider emits one event then blocks — the spawned Run goroutine leaks
// per test, which is fine because each test exits independently.
func newTestHandler(t *testing.T) (*Handler, *client) {
	t.Helper()
	prov := &blockingProvider{}
	sess := session.NewSession(DefaultSessionID)
	a, err := agent.New(agent.NewAgentConfig{
		Session:  sess,
		Provider: prov,
	})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	loop := acp.NewLoop(a)
	a.AttachMessageIDSrc(loop.CurrentMessageID)
	steer := acp.NewSteerControl(a)
	sessions := NewSessionManager()
	ledger := NewEventLedger(zap.NewNop())
	ledger.fakeDelay = 0
	handler := NewHandler(sessions, ledger, loop, steer)
	client := &client{
		sessions: sessions,
		ledger:   ledger,
		handler:  handler,
		log:      zap.NewNop(),
	}
	return handler, client
}

var idRe21 = regexp.MustCompile(`^[A-Za-z0-9]{21}$`)

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

func TestDispatchPromptRejectsUnknownSession(t *testing.T) {
	_, c := newTestHandler(t)
	req := &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.prompt",
		Params: json.RawMessage(`{"content":"hi","sessionId":"unknown"}`),
	}
	resp := dispatchRequest(context.Background(), req, c, c.handler)
	if resp.Error == nil || resp.Error.Code != CodeInvalidParams {
		t.Fatalf("expected invalid params for unknown session, got %+v", resp)
	}
	if resp.Error == nil || resp.Error.Message != "session not active" {
		t.Fatalf("expected 'session not active' message, got %+v", resp.Error)
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
		Params: json.RawMessage(`{"sessionId":"any"}`),
	}
	resp := dispatchRequest(context.Background(), req, c, c.handler)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	res, _ := resp.Result.(AbortResult)
	if !res.Aborted {
		t.Fatalf("expected aborted=true, got %+v", res)
	}
}

func TestDispatchSubscribeEventsUnknownSession(t *testing.T) {
	_, c := newTestHandler(t)
	req := &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.subscribe_events",
		Params: json.RawMessage(`{"sessionId":"nope"}`),
	}
	resp := dispatchRequest(context.Background(), req, c, c.handler)
	if resp.Error == nil || resp.Error.Code != CodeInvalidParams {
		t.Fatalf("expected invalid params for unknown session, got %+v", resp)
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
