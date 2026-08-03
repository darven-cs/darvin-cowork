package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/acp"
	"darvin-cowork/backend/internal/agents"
	"darvin-cowork/backend/internal/agents/session"
	"darvin-cowork/backend/internal/agents/store"
	"darvin-cowork/backend/internal/skills"
)

type skillEntryLoader struct{ entries []*skills.SkillEntry }

func (l skillEntryLoader) LoadAll(context.Context) ([]*skills.SkillEntry, error) {
	return l.entries, nil
}

// skillTestHandler wires a SessionManager + Skills registry + SkillRunner so
// agent.skill.invoke_user runs against production code paths.
func skillTestHandler(t *testing.T) (*Handler, *client) {
	t.Helper()
	prov := &blockingProvider{}
	st := store.NewMemoryStore()
	factory := &acp.AgentFactory{Provider: prov, Store: st, Logger: zap.NewNop()}
	steerAgent, err := agent.New(agent.NewAgentConfig{
		Session:  session.NewSession("steer-placeholder"),
		Provider: prov,
		Store:    st,
	})
	if err != nil {
		t.Fatalf("agent.New steer: %v", err)
	}
	steer := acp.NewSteerControl(steerAgent)
	sessions := NewSessionManager(WithAgentFactory(factory))
	ledger := NewEventLedger(zap.NewNop())
	ledger.fakeDelay = 0

	reg := skills.NewSkillRegistry()
	if err := reg.Load(context.Background(), []skills.SkillSourceLoader{skillEntryLoader{entries: []*skills.SkillEntry{
		{ID: "code-review", Name: "Code Review", Prompt: "review body", Enabled: true, UserInvocable: true},
		{ID: "secret-skill", Name: "Secret", Prompt: "s", Enabled: true, UserInvocable: false},
		{ID: "web-search", Name: "Web Search", Prompt: "s", Enabled: false, UserInvocable: true},
	}}}); err != nil {
		t.Fatalf("skills registry load: %v", err)
	}
	runner := skills.NewSkillRunner(reg, nil)
	handler := NewHandler(sessions, ledger, steer, nil, nil, nil, HandlerOptions{
		Skills:      reg,
		SkillRunner: runner,
		Log:         zap.NewNop(),
	})
	c := &client{sessions: sessions, ledger: ledger, handler: handler, log: zap.NewNop()}
	return handler, c
}

func invokeSkillResp(t *testing.T, c *client, params string) *Response {
	t.Helper()
	return dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`),
		Method: "agent.skill.invoke_user", Params: json.RawMessage(params),
	}, c, c.handler)
}

func TestDispatchInvokeSkillUserMissingConfig(t *testing.T) {
	_, c := newTestHandler(t) // no Skills / SkillRunner wired
	resp := invokeSkillResp(t, c, `{"skillId":"code-review"}`)
	if resp.Error == nil || resp.Error.Code != CodeMethodNotFound {
		t.Fatalf("expected MethodNotFound, got %+v", resp)
	}
}

func TestDispatchInvokeSkillUserNotFound(t *testing.T) {
	_, c := skillTestHandler(t)
	resp := invokeSkillResp(t, c, `{"skillId":"nope","args":"x"}`)
	if resp.Error == nil || resp.Error.Code != CodeSkillNotFound {
		t.Fatalf("expected CodeSkillNotFound, got %+v", resp.Error)
	}
}

func TestDispatchInvokeSkillUserDisabled(t *testing.T) {
	_, c := skillTestHandler(t)
	resp := invokeSkillResp(t, c, `{"skillId":"web-search","args":"x"}`)
	if resp.Error == nil || resp.Error.Code != CodeSkillDisabled {
		t.Fatalf("expected CodeSkillDisabled, got %+v", resp.Error)
	}
}

func TestDispatchInvokeSkillUserNotInvocable(t *testing.T) {
	_, c := skillTestHandler(t)
	resp := invokeSkillResp(t, c, `{"skillId":"secret-skill","args":"x"}`)
	if resp.Error == nil || resp.Error.Code != CodeSkillNotUserInvocable {
		t.Fatalf("expected CodeSkillNotUserInvocable, got %+v", resp.Error)
	}
}

// TestDispatchInvokeSkillUserSuccess verifies the happy path returns a
// prompt-shaped ticket (sessionId + 21-char messageId + runId) so the
// renderer can start an assistant bubble keyed by messageId. The mini loop
// runs asynchronously in the Loop goroutine (blocked on the test provider).
func TestDispatchInvokeSkillUserSuccess(t *testing.T) {
	_, c := skillTestHandler(t)
	resp := invokeSkillResp(t, c, `{"skillId":"code-review","args":"src/api"}`)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	res, ok := resp.Result.(InvokeSkillUserResult)
	if !ok {
		t.Fatalf("result type: %T", resp.Result)
	}
	if !res.OK {
		t.Errorf("ok = false, want true")
	}
	if res.SessionID != DefaultSessionID {
		t.Errorf("sessionId = %q, want %q", res.SessionID, DefaultSessionID)
	}
	if !idRe21.MatchString(res.MessageID) {
		t.Errorf("messageId %q not a 21-char nanoid", res.MessageID)
	}
	if res.RunID == "" {
		t.Errorf("runId empty")
	}
}

func TestDispatchInvokeSkillUserRequiresSkillID(t *testing.T) {
	_, c := skillTestHandler(t)
	resp := invokeSkillResp(t, c, `{"args":"x"}`)
	if resp.Error == nil || resp.Error.Code != CodeInvalidParams {
		t.Fatalf("expected invalid params, got %+v", resp)
	}
}

// TestSkillRunnerErrorSurvives pins the runner-level error into the RPC
// error's Data so main can translate the underlying error.
func TestSkillRunnerErrorSurvives(t *testing.T) {
	_, c := skillTestHandler(t)
	resp := invokeSkillResp(t, c, `{"skillId":"nope","args":"x"}`)
	if resp.Error == nil {
		t.Fatalf("expected an RPC error")
	}
	dataErr, ok := resp.Error.Data.(error)
	if !ok || !errors.Is(dataErr, skills.ErrSkillNotFound) {
		t.Fatalf("expected ErrSkillNotFound in error Data, got %+v", resp.Error.Data)
	}
}
