package harness

import (
	"context"
	"testing"
)

// registerDemoHarnesses registers the embedded runtime and the mock CLI
// harness the way main.go would, so the selection demo runs against the
// production machinery.
func registerDemoHarnesses(t *testing.T) {
	t.Helper()
	ResetRegistryForTests()
	t.Cleanup(ResetRegistryForTests)

	mustRegister(t, NewEmbedded(EmbeddedConfig{
		Run: func(_ context.Context, p RunAttemptParams) (*AttemptResult, error) {
			return &AttemptResult{Status: AttemptOK, AssistantText: "[embedded] " + p.Prompt}, nil
		},
	}))
	mustRegister(t, NewCLI(CLIHarness{}))
}

// TestCLIDemoExplicitSelection: requesting the CLI harness by id selects
// it and its mock Run settles a turn.
func TestCLIDemoExplicitSelection(t *testing.T) {
	registerDemoHarnesses(t)

	dec, err := SelectHarness(SelectionParams{
		ExplicitHarnessID: "cli",
		Context:           SupportContext{},
	})
	if err != nil {
		t.Fatalf("SelectHarness: %v", err)
	}
	if dec.Harness.ID() != "cli" {
		t.Fatalf("Harness = %q, want cli", dec.Harness.ID())
	}
	if dec.SelectedReason != ReasonForcedPlugin {
		t.Fatalf("SelectedReason = %q, want forced_plugin", dec.SelectedReason)
	}

	res, err := RunAttemptWithLifecycle(context.Background(), dec.Harness, RunAttemptParams{
		SessionID: "s1", Prompt: "hello",
	})
	if err != nil {
		t.Fatalf("RunAttemptWithLifecycle: %v", err)
	}
	if res.AssistantText != "[mock cli] hello" {
		t.Fatalf("AssistantText = %q, want the mock cli reply", res.AssistantText)
	}
}

// TestCLIDemoDefaultPrefersEmbedded: with no explicit request the embedded
// runtime wins because the CLI harness's Supports priority is negative.
func TestCLIDemoDefaultPrefersEmbedded(t *testing.T) {
	registerDemoHarnesses(t)

	dec, err := SelectHarness(SelectionParams{Context: SupportContext{}})
	if err != nil {
		t.Fatalf("SelectHarness: %v", err)
	}
	if dec.Harness.ID() != EmbeddedID {
		t.Fatalf("Harness = %q, want embedded by default", dec.Harness.ID())
	}
}

// TestCLIDemoGenericHostLacksAssembler: a context engine that needs
// assemble-before-prompt cannot run on the mock CLI, so selection must
// reject it and pick the embedded runtime.
func TestCLIDemoGenericHostLacksAssembler(t *testing.T) {
	registerDemoHarnesses(t)

	req := &ContextEngineRequirement{
		EngineID:  "ctxv2",
		Operation: OpAgentRun,
		RequiredCapabilities: []ContextEngineHostCapability{
			HostBootstrap, HostAssembleBeforePrompt,
		},
	}
	dec, err := SelectHarness(SelectionParams{Context: SupportContext{ContextEngine: req}})
	if err != nil {
		t.Fatalf("SelectHarness: %v", err)
	}
	if dec.Harness.ID() != EmbeddedID {
		t.Fatalf("Harness = %q, want embedded (cli lacks assemble-before-prompt)", dec.Harness.ID())
	}

	// Direct lifecycle assert on the cli must also fail closed.
	cli, _ := Get("cli")
	if _, err := RunAttemptWithLifecycle(context.Background(), cli, RunAttemptParams{
		SessionID: "s1", Prompt: "hi", ContextEngine: req,
	}); err == nil {
		t.Fatal("RunAttemptWithLifecycle accepted a cli host that cannot run the engine")
	}
}

// TestCLIDemoHostCapabilitySet: the mock CLI advertises exactly the
// generic-CLI verb set (bootstrap / after-turn / maintain) and nothing else.
func TestCLIDemoHostCapabilitySet(t *testing.T) {
	h := NewCLI(CLIHarness{})
	caps := h.Capabilities()
	want := map[ContextEngineHostCapability]bool{
		HostBootstrap: true, HostAfterTurn: true, HostMaintain: true,
	}
	if len(caps.ContextEngineHost) != len(want) {
		t.Fatalf("host verbs = %v, want exactly %d", caps.ContextEngineHost, len(want))
	}
	for _, have := range caps.ContextEngineHost {
		if !want[have] {
			t.Fatalf("unexpected host verb %q", have)
		}
	}
	if caps.Compact {
		t.Fatal("generic CLI must not advertise compact")
	}
}
