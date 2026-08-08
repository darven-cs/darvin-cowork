// Tests for capability verification on harnesses.

package harness

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestVerifyCapabilitiesAcceptsImplemented(t *testing.T) {
	h := compactStub{stubHarness: newStub("alpha")}
	h.caps.Compact = true

	if err := VerifyCapabilities(h); err != nil {
		t.Fatalf("VerifyCapabilities: %v", err)
	}
}

func TestVerifyCapabilitiesReportsMissing(t *testing.T) {
	h := newStub("alpha")
	h.caps.Compact = true
	h.caps.SessionFork = true

	err := VerifyCapabilities(h)
	if err == nil {
		t.Fatal("VerifyCapabilities accepted undeclared implementations")
	}
	for _, want := range []string{"compact", "sessionFork"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
}

func TestVerifyCapabilitiesAllowsUndeclaredImplementation(t *testing.T) {
	h := compactStub{stubHarness: newStub("alpha")}

	if err := VerifyCapabilities(h); err != nil {
		t.Fatalf("VerifyCapabilities: %v", err)
	}
	if Implements(h, CapCompact) {
		t.Fatal("Implements reported an undeclared capability as available")
	}
}

func TestVerifyCapabilitiesRejectsNil(t *testing.T) {
	if err := VerifyCapabilities(nil); !errors.Is(err, ErrHarnessRequired) {
		t.Fatalf("err = %v, want ErrHarnessRequired", err)
	}
}

func TestImplementsRequiresBothDeclarationAndInterface(t *testing.T) {
	declaredOnly := newStub("a")
	declaredOnly.caps.Compact = true
	if Implements(declaredOnly, CapCompact) {
		t.Fatal("Implements accepted a declaration with no interface")
	}

	both := compactStub{stubHarness: newStub("b")}
	both.caps.Compact = true
	if !Implements(both, CapCompact) {
		t.Fatal("Implements rejected a declared and implemented capability")
	}
	if Implements(nil, CapCompact) {
		t.Fatal("Implements accepted a nil harness")
	}
}

func TestCompactHelperDelegates(t *testing.T) {
	h := compactStub{
		stubHarness: newStub("alpha"),
		compact: func(_ context.Context, p CompactParams) (*CompactResult, error) {
			return &CompactResult{NewTokens: p.TargetTokens}, nil
		},
	}
	h.caps.Compact = true

	res, err := Compact(context.Background(), h, CompactParams{TargetTokens: 42})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if res.NewTokens != 42 {
		t.Fatalf("NewTokens = %d, want 42", res.NewTokens)
	}
}

func TestCompactHelperUndeclared(t *testing.T) {
	h := compactStub{stubHarness: newStub("alpha")}

	if _, err := Compact(context.Background(), h, CompactParams{}); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("err = %v, want ErrNotImplemented", err)
	}
}

func TestRankSkipsUnhealthy(t *testing.T) {
	resetGlobals(t)

	sick := newStub("sick")
	sick.caps.Healthy = false
	mustRegister(t, sick)
	mustRegister(t, newStub("well"))

	got := Rank(SupportContext{})
	if len(got) != 1 || got[0].Harness.ID() != "well" {
		t.Fatalf("Rank = %v, want only well", ids(got))
	}
}

func TestRankSkipsUnsupported(t *testing.T) {
	resetGlobals(t)

	no := newStub("no")
	no.supports = func(SupportContext) SupportResult {
		return SupportResult{Reason: "wrong provider"}
	}
	mustRegister(t, no)
	mustRegister(t, newStub("yes"))

	got := Rank(SupportContext{Provider: "anthropic"})
	if len(got) != 1 || got[0].Harness.ID() != "yes" {
		t.Fatalf("Rank = %v, want only yes", ids(got))
	}
}

func TestRankFiltersContextEngineHost(t *testing.T) {
	resetGlobals(t)

	scoped := newStub("scoped")
	scoped.caps.ContextEngineHost = []ContextEngineHostCapability{HostAssembleBeforePrompt}
	plain := newStub("plain")
	mustRegister(t, scoped)
	mustRegister(t, plain)

	req := &ContextEngineRequirement{
		EngineID:             "ctxv2",
		Operation:            OpAgentRun,
		RequiredCapabilities: []ContextEngineHostCapability{HostAssembleBeforePrompt},
	}
	if got := Rank(SupportContext{ContextEngine: req}); len(got) != 1 || got[0].Harness.ID() != "scoped" {
		t.Fatalf("Rank = %v, want only scoped", ids(got))
	}
	if got := Rank(SupportContext{}); len(got) != 2 {
		t.Fatalf("Rank = %v, want both when no requirement", ids(got))
	}
}

func TestRankFiltersDelegation(t *testing.T) {
	resetGlobals(t)

	allowed := newStub("allowed")
	allowed.caps.DelegatedExecution = []string{"acpx"}
	mustRegister(t, allowed)
	mustRegister(t, newStub("closed"))

	got := Rank(SupportContext{PluginID: "acpx"})
	if len(got) != 1 || got[0].Harness.ID() != "allowed" {
		t.Fatalf("Rank = %v, want only allowed", ids(got))
	}
}

func TestRankFiltersProviderAllowlist(t *testing.T) {
	resetGlobals(t)

	pinned := autoStub{newStub("pinned")}
	pinned.auto = &AutoSelectionHint{Providers: []string{"anthropic"}}
	mustRegister(t, pinned)

	if got := Rank(SupportContext{Provider: "openai"}); len(got) != 0 {
		t.Fatalf("Rank = %v, want empty", ids(got))
	}
	if got := Rank(SupportContext{Provider: "anthropic"}); len(got) != 1 {
		t.Fatalf("Rank = %v, want pinned", ids(got))
	}
}

func TestRankOrdersByPriorityThenID(t *testing.T) {
	resetGlobals(t)

	high := newStub("zulu")
	high.supports = func(SupportContext) SupportResult {
		return SupportResult{Supported: true, Priority: 10}
	}
	tieA := newStub("alpha")
	tieB := newStub("bravo")
	for _, h := range []*stubHarness{tieB, high, tieA} {
		mustRegister(t, h)
	}

	got := ids(Rank(SupportContext{}))
	want := []string{"zulu", "alpha", "bravo"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Rank = %v, want %v", got, want)
		}
	}
}

func TestRankNoPriorityDoubleCount(t *testing.T) {
	resetGlobals(t)

	// boosted carries an AutoSelection hint and a Supports priority of 6; the
	// hint must filter only, never add to the score. plain scores 10 and wins.
	boosted := autoStub{newStub("boosted")}
	boosted.auto = &AutoSelectionHint{Providers: []string{"anthropic"}}
	boosted.supports = func(SupportContext) SupportResult {
		return SupportResult{Supported: true, Priority: 6}
	}
	plain := newStub("aaa-plain")
	plain.supports = func(SupportContext) SupportResult {
		return SupportResult{Supported: true, Priority: 10}
	}
	mustRegister(t, boosted)
	mustRegister(t, plain)

	got := ids(Rank(SupportContext{Provider: "anthropic"}))
	if got[0] != "aaa-plain" {
		t.Fatalf("Rank = %v, want aaa-plain (10) first, not boosted (6 doubled)", got)
	}
}

func TestAutoSelectionNilHintProbes(t *testing.T) {
	resetGlobals(t)

	// A nil hint leaves eligibility to Supports: the harness must still be
	// considered and scored by its Supports answer.
	probe := autoStub{newStub("probe")}
	probe.supports = func(SupportContext) SupportResult {
		return SupportResult{Supported: true, Priority: 5}
	}
	mustRegister(t, probe)

	got := Rank(SupportContext{Provider: "openai"})
	if len(got) != 1 || got[0].Harness.ID() != "probe" {
		t.Fatalf("Rank = %v, want probe to fall through to Supports", ids(got))
	}
}

func TestAutoSelectionEmptyListExplicitOnly(t *testing.T) {
	resetGlobals(t)

	// A non-nil but empty Providers slice marks the harness explicit-only:
	// it must never be auto-selected, regardless of its Supports score.
	explicit := autoStub{newStub("explicit")}
	explicit.auto = &AutoSelectionHint{Providers: []string{}}
	explicit.supports = func(SupportContext) SupportResult {
		return SupportResult{Supported: true, Priority: 99}
	}
	mustRegister(t, explicit)
	mustRegister(t, newStub("auto"))

	got := ids(Rank(SupportContext{}))
	if len(got) != 1 || got[0] != "auto" {
		t.Fatalf("Rank = %v, want only auto; explicit-only harness must not be selected", got)
	}
}

func TestAutoSelectionProviderCaseInsensitive(t *testing.T) {
	resetGlobals(t)

	pinned := autoStub{newStub("pinned")}
	pinned.auto = &AutoSelectionHint{Providers: []string{"Anthropic"}}
	mustRegister(t, pinned)

	if got := Rank(SupportContext{Provider: "anthropic"}); len(got) != 1 {
		t.Fatalf("Rank = %v, want pinned to match case-insensitively", ids(got))
	}
}

func TestRankFiltersMissingHostCapability(t *testing.T) {
	resetGlobals(t)

	weak := newStub("weak")
	weak.caps.ContextEngineHost = []ContextEngineHostCapability{HostBootstrap, HostAfterTurn}
	full := newStub("full")
	full.caps.ContextEngineHost = []ContextEngineHostCapability{
		HostBootstrap, HostAssembleBeforePrompt, HostAfterTurn, HostMaintain, HostCompact,
	}
	mustRegister(t, weak)
	mustRegister(t, full)

	req := &ContextEngineRequirement{
		EngineID:             "ctxv2",
		Operation:            OpAgentRun,
		RequiredCapabilities: []ContextEngineHostCapability{HostBootstrap, HostAssembleBeforePrompt},
	}
	got := Rank(SupportContext{ContextEngine: req})
	if len(got) != 1 || got[0].Harness.ID() != "full" {
		t.Fatalf("Rank = %v, want only full (weak lacks assemble-before-prompt)", ids(got))
	}
}

func TestMissingHostCapabilitiesEmptyRequirement(t *testing.T) {
	resetGlobals(t)

	bare := newStub("bare")
	mustRegister(t, bare)

	if got := Rank(SupportContext{}); len(got) != 1 {
		t.Fatalf("Rank = %v, want bare with nil requirement", ids(got))
	}
	empty := &ContextEngineRequirement{EngineID: "ctxv2", Operation: OpAgentRun}
	if got := Rank(SupportContext{ContextEngine: empty}); len(got) != 1 {
		t.Fatalf("Rank = %v, want bare with empty requirement", ids(got))
	}
	legacy := &ContextEngineRequirement{
		EngineID:             LegacyContextEngineID,
		Operation:            OpAgentRun,
		RequiredCapabilities: []ContextEngineHostCapability{HostAssembleBeforePrompt},
	}
	if got := Rank(SupportContext{ContextEngine: legacy}); len(got) != 1 {
		t.Fatalf("Rank = %v, want bare with legacy engine exempt", ids(got))
	}
}

func TestCapabilitiesNoHostAdvertisedFailsClosed(t *testing.T) {
	resetGlobals(t)

	bare := newStub("bare")
	mustRegister(t, bare)

	req := &ContextEngineRequirement{
		EngineID:             "ctxv2",
		Operation:            OpAgentRun,
		RequiredCapabilities: []ContextEngineHostCapability{HostAssembleBeforePrompt},
	}
	if got := Rank(SupportContext{ContextEngine: req}); len(got) != 0 {
		t.Fatalf("Rank = %v, want empty: unadvertised host capabilities must fail closed", ids(got))
	}
	if missing := bare.caps.MissingHostCapabilities(req); len(missing) != 1 || missing[0] != HostAssembleBeforePrompt {
		t.Fatalf("MissingHostCapabilities = %v, want [assemble-before-prompt]", missing)
	}
}

func TestCapabilitiesHelpers(t *testing.T) {
	var empty Capabilities
	if !empty.AllowsDelegation("") {
		t.Fatal("an absent plugin id must not be treated as delegation")
	}
	if empty.AllowsDelegation("acpx") {
		t.Fatal("an empty delegation list must reject named plugins")
	}
	if empty.Declares(Capability("unknown")) {
		t.Fatal("Declares accepted an unknown capability")
	}

	var nilHint *AutoSelectionHint
	if !nilHint.Eligible("anything") {
		t.Fatal("a nil hint must leave eligibility to Supports")
	}
	nilProviders := &AutoSelectionHint{}
	if !nilProviders.Eligible("anything") {
		t.Fatal("a nil Providers slice must leave eligibility to Supports")
	}
	explicitOnly := &AutoSelectionHint{Providers: []string{}}
	if explicitOnly.Eligible("anything") {
		t.Fatal("a non-nil empty Providers slice must be explicit-only")
	}
	cased := &AutoSelectionHint{Providers: []string{"Anthropic"}}
	if !cased.Eligible("anthropic") {
		t.Fatal("allowlist matching must normalize provider ids")
	}
	if cased.Eligible("openai") {
		t.Fatal("allowlist matched a provider outside the list")
	}

	if missing := empty.MissingHostCapabilities(nil); len(missing) != 0 {
		t.Fatalf("nil requirement = %v, want none missing", missing)
	}
	if missing := empty.MissingHostCapabilities(&ContextEngineRequirement{}); len(missing) != 0 {
		t.Fatalf("empty requirement = %v, want none missing", missing)
	}
	legacy := &ContextEngineRequirement{
		EngineID:             LegacyContextEngineID,
		Operation:            OpAgentRun,
		RequiredCapabilities: []ContextEngineHostCapability{HostAssembleBeforePrompt},
	}
	if missing := empty.MissingHostCapabilities(legacy); len(missing) != 0 {
		t.Fatalf("legacy engine = %v, want exempt", missing)
	}
	demanding := &ContextEngineRequirement{
		EngineID:             "ctxv2",
		Operation:            OpAgentRun,
		RequiredCapabilities: []ContextEngineHostCapability{HostAssembleBeforePrompt},
	}
	if missing := empty.MissingHostCapabilities(demanding); len(missing) != 1 {
		t.Fatalf("demanding requirement = %v, want [assemble-before-prompt]", missing)
	}
}

func mustRegister(t *testing.T, h Harness) {
	t.Helper()
	if err := Register(h, ""); err != nil {
		t.Fatalf("Register %q: %v", h.ID(), err)
	}
}

func ids(candidates []Candidate) []string {
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.Harness.ID())
	}
	return out
}
