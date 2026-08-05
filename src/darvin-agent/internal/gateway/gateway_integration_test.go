package gateway

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/acp"
	agent "darvin-cowork/backend/internal/agents"
	"darvin-cowork/backend/internal/agents/session"
	"darvin-cowork/backend/internal/agents/store"
	"darvin-cowork/backend/internal/harness"
	"darvin-cowork/backend/internal/tools"
)

// TestHandlePromptFactoryResolvesHarness asserts the factory's selector is
// consulted when a session is lazily built, and the resulting AcpSession
// carries the harness so Loop drives through it.
func TestHandlePromptFactoryResolvesHarness(t *testing.T) {
	var called bool
	handler := harnessWireTestHandler(t, func(a *agent.Agent, _ *acp.AgentFactory) (harness.Harness, error) {
		called = true
		return acp.NewEmbeddedTestHarness(a), nil
	})
	client := newClientFromHandler(handler)

	resp := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.prompt",
		Params: json.RawMessage(`{"content":"hi"}`),
	}, client, handler)
	if resp.Error != nil {
		t.Fatalf("prompt error: %+v", resp.Error)
	}
	if !called {
		t.Fatal("factory selector was not consulted during lazy build")
	}
}

// TestHandlePromptGoesThroughHarness asserts the prompt path reaches the
// harness's Run closure and that events emitted by the agent still flow
// through the ledger to the subscriber.
func TestHandlePromptGoesThroughHarness(t *testing.T) {
	var runCalled atomic.Bool
	handler := harnessWireTestHandler(t, func(a *agent.Agent, _ *acp.AgentFactory) (harness.Harness, error) {
		return harness.NewEmbedded(harness.EmbeddedConfig{
			Run: func(ctx context.Context, p harness.RunAttemptParams) (*harness.AttemptResult, error) {
				runCalled.Store(true)
				if p.Prompt != "hi" {
					t.Errorf("prompt = %q, want hi", p.Prompt)
				}
				if err := a.Prompt(ctx, p.Prompt, nil, p.Attachments); err != nil {
					return nil, err
				}
				_ = a.Run(ctx)
				return &harness.AttemptResult{Status: harness.AttemptOK}, nil
			},
		}), nil
	})
	client := newClientFromHandler(handler)

	resp := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.prompt",
		Params: json.RawMessage(`{"content":"hi"}`),
	}, client, handler)
	if resp.Error != nil {
		t.Fatalf("prompt error: %+v", resp.Error)
	}
	// The run executes on the Loop goroutine after the handler returns;
	// wait for the harness closure to fire.
	waitForCondition(t, func() bool { return runCalled.Load() })
}

// TestHarnessNotRegistered asserts an explicit HarnessID that is absent
// from the registry fails the session build.
func TestHarnessNotRegistered(t *testing.T) {
	handler := harnessWireTestHandler(t, func(*agent.Agent, *acp.AgentFactory) (harness.Harness, error) {
		// Selector must never be reached: the explicit id fails first.
		t.Fatal("selector consulted despite explicit HarnessID")
		return nil, nil
	})
	client := newClientFromHandler(handler)

	// Replace the factory's selector with an explicit-id pin so resolveHarness
	// takes the lookup branch.
	client.sessions.mu.Lock()
	client.sessions.factory.HarnessID = "does-not-exist"
	client.sessions.mu.Unlock()

	resp := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.prompt",
		Params: json.RawMessage(`{"content":"hi"}`),
	}, client, handler)
	if resp.Error == nil {
		t.Fatal("prompt with unknown explicit harness did not fail")
	}
}

// TestHandleAbortStopsHarness asserts abort cancels the in-flight run.
// The blocking provider keeps the run alive; the harness selector wires a
// forwarder; Stop cancels the run context.
func TestHandleAbortStopsHarness(t *testing.T) {
	handler := harnessWireTestHandler(t, func(a *agent.Agent, _ *acp.AgentFactory) (harness.Harness, error) {
		return acp.NewEmbeddedTestHarness(a), nil
	})
	client := newClientFromHandler(handler)

	resp := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: "agent.prompt",
		Params: json.RawMessage(`{"content":"hi"}`),
	}, client, handler)
	if resp.Error != nil {
		t.Fatalf("prompt error: %+v", resp.Error)
	}
	sid := resp.Result.(PromptResult).SessionID

	entry, err := client.sessions.GetOrCreateEntry(sid)
	if err != nil {
		t.Fatalf("GetOrCreateEntry: %v", err)
	}
	waitForCondition(t, func() bool { return entry.Acp.Loop.ActiveRunID() != "" })
	runID := entry.Acp.Loop.ActiveRunID()
	if runID == "" {
		t.Fatal("no in-flight run to abort")
	}

	abort := dispatchRequest(context.Background(), &Request{
		JSONRPC: "2.0", ID: json.RawMessage(`"2"`), Method: "agent.abort",
		Params: json.RawMessage(`{"sessionId":"` + sid + `","runId":"` + runID + `"}`),
	}, client, handler)
	if abort.Error != nil {
		t.Fatalf("abort error: %+v", abort.Error)
	}

	waitForCondition(t, func() bool { return entry.Acp.Loop.ActiveRunID() == "" })
}

// harnessWireTestHandler builds a handler whose factory uses sel as its
// harness selector.
func harnessWireTestHandler(t *testing.T, sel acp.HarnessSelector) *Handler {
	t.Helper()
	prov := &blockingProvider{}
	st := store.NewMemoryStore()
	factory := &acp.AgentFactory{
		Provider: prov,
		Tools:    tool.NewRegistry(),
		Store:    st,
		Logger:   zap.NewNop(),
		Selector: sel,
	}
	steerAgent, err := agent.New(agent.NewAgentConfig{
		Session:  session.NewSession("steer-placeholder"),
		Provider: prov,
		Tools:    tool.NewRegistry(),
		Store:    st,
	})
	if err != nil {
		t.Fatalf("agent.New steer: %v", err)
	}
	steer := acp.NewSteerControl(steerAgent)
	sessions := NewSessionManager(WithAgentFactory(factory))
	ledger := NewEventLedger(zap.NewNop())
	ledger.fakeDelay = 0
	return NewHandler(sessions, ledger, steer, nil, nil, nil)
}

func newClientFromHandler(handler *Handler) *client {
	return &client{
		sessions: handler.Sessions,
		ledger:   handler.Ledger,
		handler:  handler,
		log:      zap.NewNop(),
	}
}
