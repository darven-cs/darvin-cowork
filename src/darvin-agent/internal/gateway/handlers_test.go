package gateway

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"

	"go.uber.org/zap"
)

// dispatch tests use a fake *client (zero-value conn). Handlers
// themselves never touch conn — they only call c.sessions / c.ledger
// — so the nil conn is fine; writeJSON would panic, but the handler
// paths under test here don't write a frame back. The reply is
// returned to the caller for inspection.

func newTestClient(t *testing.T) (*client, *SessionManager, *EventLedger) {
	t.Helper()
	sessions := NewSessionManager()
	ledger := NewEventLedger(zap.NewNop())
	ledger.fakeDelay = 0
	return &client{sessions: sessions, ledger: ledger, log: zap.NewNop()}, sessions, ledger
}

var idRe21 = regexp.MustCompile(`^[A-Za-z0-9]{21}$`)

func TestDispatchPrompt(t *testing.T) {
	c, _, _ := newTestClient(t)
	req := &Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"1"`),
		Method:  "agent.prompt",
		Params:  json.RawMessage(`{"content":"hi"}`),
	}
	resp := dispatchRequest(context.Background(), req, c)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	res, ok := resp.Result.(PromptResult)
	if !ok {
		t.Fatalf("result type: %T", resp.Result)
	}
	if !idRe21.MatchString(res.SessionID) || !idRe21.MatchString(res.MessageID) {
		t.Fatalf("id shape: %+v", res)
	}
}

func TestDispatchPromptMissingContent(t *testing.T) {
	c, _, _ := newTestClient(t)
	req := &Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"2"`),
		Method:  "agent.prompt",
		Params:  json.RawMessage(`{}`),
	}
	resp := dispatchRequest(context.Background(), req, c)
	if resp.Error == nil || resp.Error.Code != CodeInvalidParams {
		t.Fatalf("expected invalid params, got %+v", resp)
	}
}

func TestDispatchPromptEmptyContent(t *testing.T) {
	c, _, _ := newTestClient(t)
	req := &Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"3"`),
		Method:  "agent.prompt",
		Params:  json.RawMessage(`{"content":"   "}`),
	}
	resp := dispatchRequest(context.Background(), req, c)
	if resp.Error == nil || resp.Error.Code != CodeInvalidParams {
		t.Fatalf("expected invalid params, got %+v", resp)
	}
}

func TestDispatchPromptReusesSessionID(t *testing.T) {
	c, sessions, _ := newTestClient(t)
	first := &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.prompt",
		Params: json.RawMessage(`{"content":"hi"}`),
	}
	r1 := dispatchRequest(context.Background(), first, c)
	id1 := r1.Result.(PromptResult).SessionID

	second := &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"2"`), Method: "agent.prompt",
		Params: json.RawMessage(`{"content":"again","sessionId":"` + id1 + `"}`),
	}
	r2 := dispatchRequest(context.Background(), second, c)
	if r2.Result.(PromptResult).SessionID != id1 {
		t.Fatalf("session id should be reused: %+v", r2.Result)
	}
	if !sessions.Has(id1) {
		t.Fatalf("expected session %q to exist", id1)
	}
}

func TestDispatchUnknownMethod(t *testing.T) {
	c, _, _ := newTestClient(t)
	req := &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"x"`), Method: "no.such",
	}
	resp := dispatchRequest(context.Background(), req, c)
	if resp.Error == nil || resp.Error.Code != CodeMethodNotFound {
		t.Fatalf("expected method not found, got %+v", resp)
	}
}

func TestDispatchAbort(t *testing.T) {
	c, _, _ := newTestClient(t)
	req := &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.abort",
		Params: json.RawMessage(`{"sessionId":"any"}`),
	}
	resp := dispatchRequest(context.Background(), req, c)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	res, _ := resp.Result.(AbortResult)
	if !res.Aborted {
		t.Fatalf("expected aborted=true, got %+v", res)
	}
}

func TestDispatchSubscribeEventsUnknownSession(t *testing.T) {
	c, _, _ := newTestClient(t)
	req := &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.subscribe_events",
		Params: json.RawMessage(`{"sessionId":"nope"}`),
	}
	resp := dispatchRequest(context.Background(), req, c)
	if resp.Error == nil || resp.Error.Code != CodeInvalidParams {
		t.Fatalf("expected invalid params for unknown session, got %+v", resp)
	}
}

func TestDispatchSubscribeEventsSuccess(t *testing.T) {
	c, _, _ := newTestClient(t)
	// First create a session via prompt.
	pr := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.prompt",
		Params: json.RawMessage(`{"content":"x"}`),
	}, c)
	sid := pr.Result.(PromptResult).SessionID

	resp := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"2"`), Method: "agent.subscribe_events",
		Params: json.RawMessage(`{"sessionId":"` + sid + `"}`),
	}, c)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	res, _ := resp.Result.(SubscribeEventsResult)
	if !res.Subscribed {
		t.Fatalf("expected subscribed=true, got %+v", res)
	}
}
