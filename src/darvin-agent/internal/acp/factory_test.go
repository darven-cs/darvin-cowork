package acp

import (
	"context"
	"errors"
	"testing"

	"darvin-cowork/backend/internal/agents"
	"darvin-cowork/backend/internal/llm"
	"darvin-cowork/backend/internal/agents/store"
	"go.uber.org/zap"
)

type nopProvider struct{}

func (nopProvider) Name() string { return "nop" }
func (nopProvider) Complete(_ context.Context, _ *llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return nil, errors.New("nopProvider: Complete not implemented")
}
func (nopProvider) Stream(_ context.Context, _ *llm.CompletionRequest) (*llm.StreamingResponse, error) {
	return nil, errors.New("nopProvider: Stream not implemented")
}

func newTestFactory() *AgentFactory {
	return &AgentFactory{
		Name:         "test-agent",
		Instructions: "test",
		Model:        agent.ModelRef{},
		Provider:     nopProvider{},
		Store:        store.NewMemoryStore(),
		Logger:       zap.NewNop(),
	}
}

func TestFactory_BuildAttachesLoopAndSources(t *testing.T) {
	f := newTestFactory()
	sess, err := f.NewAcpSession("alpha")
	if err != nil {
		t.Fatalf("NewAcpSession: %v", err)
	}
	t.Cleanup(sess.Close)

	if sess.SessionID != "alpha" {
		t.Fatalf("SessionID = %q, want %q", sess.SessionID, "alpha")
	}
	if sess.Agent == nil {
		t.Fatalf("Agent is nil")
	}
	if sess.Loop == nil {
		t.Fatalf("Loop is nil")
	}
	if got := sess.Agent.Session().ID; got != "alpha" {
		t.Fatalf("Agent.Session().ID = %q, want %q", got, "alpha")
	}
	if got := sess.Loop.CurrentMessageID(); got != sess.Agent.CurrentMessageID() {
		t.Errorf("Agent.CurrentMessageID() = %q, Loop.CurrentMessageID() = %q", sess.Agent.CurrentMessageID(), got)
	}
	if got := sess.Loop.CurrentRunID(); got != sess.Agent.CurrentRunID() {
		t.Errorf("Agent.CurrentRunID() = %q, Loop.CurrentRunID() = %q", sess.Agent.CurrentRunID(), got)
	}
}

func TestFactory_DifferentSessionIDsDifferentAgents(t *testing.T) {
	f := newTestFactory()
	a, err := f.NewAcpSession("alpha")
	if err != nil {
		t.Fatalf("NewAcpSession alpha: %v", err)
	}
	t.Cleanup(a.Close)
	b, err := f.NewAcpSession("beta")
	if err != nil {
		t.Fatalf("NewAcpSession beta: %v", err)
	}
	t.Cleanup(b.Close)

	if a.Agent == b.Agent {
		t.Fatalf("two factories returned the same *agent.Agent")
	}
	if a.Loop == b.Loop {
		t.Fatalf("two factories returned the same *Loop")
	}
	if a.Agent.Session().ID == b.Agent.Session().ID {
		t.Fatalf("session ids leaked between sessions: a=%q b=%q", a.Agent.Session().ID, b.Agent.Session().ID)
	}
	if a.Agent.Session().ID != "alpha" {
		t.Errorf("a.Agent.Session().ID = %q, want %q", a.Agent.Session().ID, "alpha")
	}
	if b.Agent.Session().ID != "beta" {
		t.Errorf("b.Agent.Session().ID = %q, want %q", b.Agent.Session().ID, "beta")
	}
}

func TestFactory_CloseIsIdempotent(t *testing.T) {
	f := newTestFactory()
	sess, err := f.NewAcpSession("alpha")
	if err != nil {
		t.Fatalf("NewAcpSession: %v", err)
	}
	sess.Close()
	sess.Close()
	if _, err := sess.Loop.Submit(PromptRequest{Content: "after close"}); !errors.Is(err, ErrLoopClosed) {
		t.Errorf("Submit after Close: err = %v, want ErrLoopClosed", err)
	}
}
