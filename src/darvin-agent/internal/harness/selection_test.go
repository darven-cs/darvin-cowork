package harness

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func resetSelectGlobals(t *testing.T) {
	t.Helper()
	ResetRegistryForTests()
	ResetLifecycleForTests()
}

func registerEmbed(t *testing.T) Harness {
	t.Helper()
	h := NewEmbedded(EmbeddedConfig{
		Run: func(_ context.Context, _ RunAttemptParams) (*AttemptResult, error) {
			return &AttemptResult{Status: AttemptOK}, nil
		},
	})
	if err := Register(h, ""); err != nil {
		t.Fatalf("Register embedded: %v", err)
	}
	return h
}

// --- 5 维评分 + 决策树 ---

func TestExplicitHarnessWins(t *testing.T) {
	resetSelectGlobals(t)
	embed := registerEmbed(t)
	cli := newStub("cli")
	mustRegister(t, cli)

	dec, err := SelectHarness(SelectionParams{
		ExplicitHarnessID: "cli",
		Context:           SupportContext{},
	})
	if err != nil {
		t.Fatalf("SelectHarness: %v", err)
	}
	if dec.Harness != Harness(cli) {
		t.Fatalf("Harness = %v, want cli", dec.Harness)
	}
	if dec.SelectedReason != ReasonForcedPlugin {
		t.Fatalf("SelectedReason = %q, want forced_plugin", dec.SelectedReason)
	}
	_ = embed
}

func TestExplicitHarnessNotRegisteredFails(t *testing.T) {
	resetSelectGlobals(t)
	registerEmbed(t)

	_, err := SelectHarness(SelectionParams{
		ExplicitHarnessID: "missing",
		Context:           SupportContext{},
	})
	if !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("err = %v, want ErrNotRegistered", err)
	}
}

func TestDefaultHarnessFallsThroughWhenMissing(t *testing.T) {
	resetSelectGlobals(t)
	registerEmbed(t)

	dec, err := SelectHarness(SelectionParams{
		DefaultHarnessID: "codex",
		Context:          SupportContext{},
	})
	if err != nil {
		t.Fatalf("SelectHarness: %v", err)
	}
	if dec.Harness.ID() != EmbeddedID {
		t.Fatalf("Harness = %q, want embedded fallback", dec.Harness.ID())
	}
	if dec.SelectedReason != ReasonDefaultPluginUnavailable {
		t.Fatalf("SelectedReason = %q, want default_plugin_unavailable", dec.SelectedReason)
	}
}

func TestDefaultHarnessForcesWhenRegistered(t *testing.T) {
	resetSelectGlobals(t)
	registerEmbed(t)
	defaulted := newStub("defaulted")
	mustRegister(t, defaulted)

	dec, err := SelectHarness(SelectionParams{
		DefaultHarnessID: "defaulted",
		Context:          SupportContext{},
	})
	if err != nil {
		t.Fatalf("SelectHarness: %v", err)
	}
	if dec.Harness.ID() != "defaulted" {
		t.Fatalf("Harness = %q, want defaulted", dec.Harness.ID())
	}
	if dec.SelectedReason != ReasonForcedDefaultPlugin {
		t.Fatalf("SelectedReason = %q, want forced_default_plugin", dec.SelectedReason)
	}
}

func TestImplicitRuntimeFallbackToEmbedded(t *testing.T) {
	resetSelectGlobals(t)
	registerEmbed(t)

	dec, err := SelectHarness(SelectionParams{
		Context: SupportContext{RequestedRuntime: "codex"},
	})
	if err != nil {
		t.Fatalf("SelectHarness: %v", err)
	}
	if dec.Harness.ID() != EmbeddedID {
		t.Fatalf("Harness = %q, want embedded", dec.Harness.ID())
	}
	if dec.SelectedReason != ReasonImplicitPluginUnavailable {
		t.Fatalf("SelectedReason = %q, want implicit_plugin_unavailable", dec.SelectedReason)
	}
}

func TestImplicitRuntimeUnsupportedFallsBack(t *testing.T) {
	resetSelectGlobals(t)
	registerEmbed(t)
	codex := newStub("codex")
	codex.supports = func(SupportContext) SupportResult {
		return SupportResult{Reason: "model not supported"}
	}
	mustRegister(t, codex)

	dec, err := SelectHarness(SelectionParams{
		Context: SupportContext{RequestedRuntime: "codex", Provider: "anthropic"},
	})
	if err != nil {
		t.Fatalf("SelectHarness: %v", err)
	}
	if dec.Harness.ID() != EmbeddedID {
		t.Fatalf("Harness = %q, want embedded fallback", dec.Harness.ID())
	}
	if dec.SelectedReason != ReasonImplicitPluginUnsupported {
		t.Fatalf("SelectedReason = %q, want implicit_plugin_unsupported", dec.SelectedReason)
	}
}

func TestImplicitRuntimeMatchWins(t *testing.T) {
	resetSelectGlobals(t)
	registerEmbed(t)
	codex := newStub("codex")
	codex.supports = func(SupportContext) SupportResult {
		return SupportResult{Supported: true, Priority: 1}
	}
	mustRegister(t, codex)

	dec, err := SelectHarness(SelectionParams{
		Context: SupportContext{RequestedRuntime: "codex", Provider: "anthropic"},
	})
	if err != nil {
		t.Fatalf("SelectHarness: %v", err)
	}
	if dec.Harness.ID() != "codex" {
		t.Fatalf("Harness = %q, want codex (implicit match)", dec.Harness.ID())
	}
	if dec.SelectedReason != ReasonAutoPlugin {
		t.Fatalf("SelectedReason = %q, want auto_plugin", dec.SelectedReason)
	}
}

func TestAutoSelectByPriority(t *testing.T) {
	resetSelectGlobals(t)
	registerEmbed(t)
	a := newStub("a-prio-7")
	a.supports = func(SupportContext) SupportResult { return SupportResult{Supported: true, Priority: 7} }
	b := newStub("b-prio-3")
	b.supports = func(SupportContext) SupportResult { return SupportResult{Supported: true, Priority: 3} }
	mustRegister(t, a)
	mustRegister(t, b)

	dec, err := SelectHarness(SelectionParams{Context: SupportContext{}})
	if err != nil {
		t.Fatalf("SelectHarness: %v", err)
	}
	if dec.Harness.ID() != "a-prio-7" {
		t.Fatalf("Harness = %q, want a-prio-7", dec.Harness.ID())
	}
	if dec.SelectedReason != ReasonAutoPlugin {
		t.Fatalf("SelectedReason = %q, want auto_plugin", dec.SelectedReason)
	}
}

func TestAutoSelectByProviderWhitelistExcludes(t *testing.T) {
	resetSelectGlobals(t)
	registerEmbed(t)
	pinned := autoStub{newStub("pinned")}
	pinned.auto = &AutoSelectionHint{Providers: []string{"anthropic"}}
	pinned.supports = func(SupportContext) SupportResult { return SupportResult{Supported: true, Priority: 99} }
	mustRegister(t, pinned)

	dec, err := SelectHarness(SelectionParams{Context: SupportContext{Provider: "openai"}})
	if err != nil {
		t.Fatalf("SelectHarness: %v", err)
	}
	if dec.Harness.ID() != EmbeddedID {
		t.Fatalf("Harness = %q, want embedded (pinned excluded)", dec.Harness.ID())
	}
}

func TestExplicitOnlyHarnessNeverAutoSelected(t *testing.T) {
	resetSelectGlobals(t)
	registerEmbed(t)
	explicit := autoStub{newStub("explicit")}
	explicit.auto = &AutoSelectionHint{Providers: []string{}}
	explicit.supports = func(SupportContext) SupportResult { return SupportResult{Supported: true, Priority: 99} }
	mustRegister(t, explicit)

	dec, err := SelectHarness(SelectionParams{Context: SupportContext{}})
	if err != nil {
		t.Fatalf("SelectHarness: %v", err)
	}
	if dec.Harness.ID() == "explicit" {
		t.Fatalf("explicit-only harness leaked into auto selection: %v", dec.Harness)
	}
}

func TestSupportsOnlyPriorityNoDoubleCount(t *testing.T) {
	resetSelectGlobals(t)
	registerEmbed(t)
	boosted := autoStub{newStub("boosted")}
	boosted.auto = &AutoSelectionHint{Providers: []string{"anthropic"}}
	boosted.supports = func(SupportContext) SupportResult {
		return SupportResult{Supported: true, Priority: 6}
	}
	plain := newStub("plain")
	plain.supports = func(SupportContext) SupportResult {
		return SupportResult{Supported: true, Priority: 10}
	}
	mustRegister(t, boosted)
	mustRegister(t, plain)

	dec, err := SelectHarness(SelectionParams{Context: SupportContext{Provider: "anthropic"}})
	if err != nil {
		t.Fatalf("SelectHarness: %v", err)
	}
	if dec.Harness.ID() != "plain" {
		t.Fatalf("Harness = %q, want plain (10 > 6)", dec.Harness.ID())
	}
}

func TestRequestedRuntimeBoostDominated(t *testing.T) {
	resetSelectGlobals(t)
	registerEmbed(t)
	heavy := newStub("heavy")
	heavy.supports = func(SupportContext) SupportResult {
		return SupportResult{Supported: true, Priority: 50}
	}
	codex := newStub("codex")
	codex.supports = func(SupportContext) SupportResult {
		return SupportResult{Supported: true, Priority: 10}
	}
	mustRegister(t, heavy)
	mustRegister(t, codex)

	dec, err := SelectHarness(SelectionParams{Context: SupportContext{RequestedRuntime: "codex"}})
	if err != nil {
		t.Fatalf("SelectHarness: %v", err)
	}
	if dec.Harness.ID() != "codex" {
		t.Fatalf("Harness = %q, want codex (boost must dominate)", dec.Harness.ID())
	}
}

func TestMissingHostCapabilityFiltered(t *testing.T) {
	resetSelectGlobals(t)
	registerEmbed(t)
	weak := newStub("weak")
	weak.caps.ContextEngineHost = []ContextEngineHostCapability{HostBootstrap, HostAfterTurn}
	mustRegister(t, weak)

	req := &ContextEngineRequirement{
		EngineID:             "ctxv2",
		Operation:            OpAgentRun,
		RequiredCapabilities: []ContextEngineHostCapability{HostAssembleBeforePrompt},
	}
	dec, err := SelectHarness(SelectionParams{Context: SupportContext{ContextEngine: req}})
	if err != nil {
		t.Fatalf("SelectHarness: %v", err)
	}
	if dec.Harness.ID() != EmbeddedID {
		t.Fatalf("Harness = %q, want embedded fallback (weak excluded)", dec.Harness.ID())
	}
}

func TestStableSortByIDOnTie(t *testing.T) {
	resetSelectGlobals(t)
	registerEmbed(t)
	bravo := newStub("bravo")
	bravo.supports = func(SupportContext) SupportResult { return SupportResult{Supported: true, Priority: 5} }
	alpha := newStub("alpha")
	alpha.supports = func(SupportContext) SupportResult { return SupportResult{Supported: true, Priority: 5} }
	mustRegister(t, bravo)
	mustRegister(t, alpha)

	for i := 0; i < 10; i++ {
		dec, err := SelectHarness(SelectionParams{Context: SupportContext{}})
		if err != nil {
			t.Fatalf("SelectHarness: %v", err)
		}
		if dec.Harness.ID() != "alpha" {
			t.Fatalf("iteration %d: Harness = %q, want alpha (stable tie-break)", i, dec.Harness.ID())
		}
	}
}

func TestEmptyRegistryFailsNoFallback(t *testing.T) {
	resetSelectGlobals(t)

	_, err := SelectHarness(SelectionParams{Context: SupportContext{}})
	if !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("err = %v, want ErrNoCandidate", err)
	}
}

func TestDecisionIncludesCandidates(t *testing.T) {
	resetSelectGlobals(t)
	registerEmbed(t)
	other := newStub("other")
	mustRegister(t, other)

	dec, err := SelectHarness(SelectionParams{Context: SupportContext{}})
	if err != nil {
		t.Fatalf("SelectHarness: %v", err)
	}
	if len(dec.Candidates) != 2 {
		t.Fatalf("Candidates len = %d, want 2", len(dec.Candidates))
	}
	ids := []string{dec.Candidates[0].ID, dec.Candidates[1].ID}
	if strings.Join(ids, ",") != "embedded,other" {
		t.Fatalf("Candidates = %v, want [embedded other]", ids)
	}
}

func TestRankWithBoostLeavesOthersUntouched(t *testing.T) {
	resetSelectGlobals(t)
	registerEmbed(t)
	heavy := newStub("heavy")
	heavy.supports = func(SupportContext) SupportResult {
		return SupportResult{Supported: true, Priority: 50}
	}
	mustRegister(t, heavy)

	ranked := RankWithBoost(SupportContext{})
	if len(ranked) != 2 {
		t.Fatalf("Rank len = %d, want 2", len(ranked))
	}
	if ranked[0].Harness.ID() != "heavy" {
		t.Fatalf("first = %q, want heavy", ranked[0].Harness.ID())
	}
	for _, c := range ranked {
		if c.Result.Priority > 50 && c.Harness.ID() != "heavy" {
			t.Fatalf("unexpected boost on %q (prio=%d)", c.Harness.ID(), c.Result.Priority)
		}
	}
}
