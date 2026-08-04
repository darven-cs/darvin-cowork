package harness

import (
	"context"
	"errors"
	"testing"
)

// stubHarness implements only the required Harness surface, so a test that
// declares an optional capability on it exercises the verification path.
type stubHarness struct {
	id       string
	label    string
	pluginID string
	caps     Capabilities
	auto     *AutoSelectionHint

	supports func(SupportContext) SupportResult
	run      func(context.Context, RunAttemptParams) (*AttemptResult, error)
}

func newStub(id string) *stubHarness {
	return &stubHarness{id: id, caps: Capabilities{Healthy: true}}
}

func (s *stubHarness) ID() string                 { return s.id }
func (s *stubHarness) Label() string              { return s.label }
func (s *stubHarness) PluginID() string           { return s.pluginID }
func (s *stubHarness) Capabilities() Capabilities { return s.caps }

func (s *stubHarness) Supports(sc SupportContext) SupportResult {
	if s.supports != nil {
		return s.supports(sc)
	}
	return SupportResult{Supported: true}
}

func (s *stubHarness) RunAttempt(ctx context.Context, params RunAttemptParams) (*AttemptResult, error) {
	if s.run != nil {
		return s.run(ctx, params)
	}
	return &AttemptResult{Status: AttemptOK}, nil
}

func (s *stubHarness) Reset(context.Context, ResetParams) error { return nil }
func (s *stubHarness) Dispose(context.Context) error            { return nil }

// autoStub adds the AutoSelector interface to a stub.
type autoStub struct{ *stubHarness }

func (a autoStub) AutoSelection() *AutoSelectionHint { return a.auto }

// compactStub adds the Compactor interface to a stub.
type compactStub struct {
	*stubHarness
	compact func(context.Context, CompactParams) (*CompactResult, error)
}

func (c compactStub) Compact(ctx context.Context, p CompactParams) (*CompactResult, error) {
	if c.compact != nil {
		return c.compact(ctx, p)
	}
	return &CompactResult{}, nil
}

// classifyStub adds the Classifier interface to a stub.
type classifyStub struct {
	*stubHarness
	label Classification
}

func (c classifyStub) Classify(context.Context, *AttemptResult, *RunAttemptParams) Classification {
	return c.label
}

func resetGlobals(t *testing.T) {
	t.Helper()
	ResetRegistryForTests()
	ResetLifecycleForTests()
	t.Cleanup(func() {
		ResetRegistryForTests()
		ResetLifecycleForTests()
	})
}

func TestEmbeddedWithoutRunnerIsUnhealthy(t *testing.T) {
	h := NewEmbedded(EmbeddedConfig{})

	if h.ID() != EmbeddedID {
		t.Fatalf("ID = %q, want %q", h.ID(), EmbeddedID)
	}
	if h.Label() == "" {
		t.Fatal("Label is empty")
	}
	if h.PluginID() != "" {
		t.Fatalf("PluginID = %q, want empty for a built-in", h.PluginID())
	}
	if h.Capabilities().Healthy {
		t.Fatal("harness without a runner reports healthy")
	}
	if res := h.Supports(SupportContext{}); res.Supported {
		t.Fatal("harness without a runner reports Supported")
	}
}

func TestEmbeddedRunAttemptWithoutRunner(t *testing.T) {
	h := NewEmbedded(EmbeddedConfig{})

	if _, err := h.RunAttempt(context.Background(), RunAttemptParams{}); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("RunAttempt err = %v, want ErrNotImplemented", err)
	}
	if _, err := Compact(context.Background(), h, CompactParams{}); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("Compact err = %v, want ErrNotImplemented", err)
	}
}

func TestEmbeddedRegistersAndRuns(t *testing.T) {
	resetGlobals(t)

	var got RunAttemptParams
	h := NewEmbedded(EmbeddedConfig{
		Run: func(_ context.Context, p RunAttemptParams) (*AttemptResult, error) {
			got = p
			return &AttemptResult{Status: AttemptOK, AssistantText: "hi", TotalTurns: 1}, nil
		},
	})
	if err := Register(h, ""); err != nil {
		t.Fatalf("Register: %v", err)
	}

	selected, err := Policy{}.Resolve(SupportContext{Provider: "anthropic"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	res, err := RunAttemptWithLifecycle(context.Background(), selected, RunAttemptParams{
		SessionID: "s1",
		Prompt:    "hello",
	})
	if err != nil {
		t.Fatalf("RunAttemptWithLifecycle: %v", err)
	}
	if res.Status != AttemptOK || res.AssistantText != "hi" {
		t.Fatalf("result = %+v", res)
	}
	if got.RunID == "" || got.MessageID == "" || got.UserMessageID == "" {
		t.Fatalf("correlation ids not minted: %+v", got)
	}
}

func TestEmbeddedCompact(t *testing.T) {
	h := NewEmbedded(EmbeddedConfig{
		Run: func(context.Context, RunAttemptParams) (*AttemptResult, error) { return nil, nil },
		Compact: func(_ context.Context, p CompactParams) (*CompactResult, error) {
			return &CompactResult{NewTokens: p.TargetTokens, RemovedMessages: 3}, nil
		},
	})

	if !h.Capabilities().Compact {
		t.Fatal("Compact capability not declared with a hook wired")
	}
	if err := VerifyCapabilities(h); err != nil {
		t.Fatalf("VerifyCapabilities: %v", err)
	}
	res, err := Compact(context.Background(), h, CompactParams{TargetTokens: 100})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if res.NewTokens != 100 || res.RemovedMessages != 3 {
		t.Fatalf("result = %+v", res)
	}
}

func TestEmbeddedResetBumpsGeneration(t *testing.T) {
	resetGlobals(t)

	h := NewEmbedded(EmbeddedConfig{})
	before := LifecycleGeneration("s1")
	if err := h.Reset(context.Background(), ResetParams{SessionID: "s1"}); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if after := LifecycleGeneration("s1"); after != before+1 {
		t.Fatalf("generation = %d, want %d", after, before+1)
	}
}

func TestEmbeddedDisposeWithoutHook(t *testing.T) {
	h := NewEmbedded(EmbeddedConfig{})
	if err := h.Dispose(context.Background()); err != nil {
		t.Fatalf("Dispose: %v", err)
	}
}
