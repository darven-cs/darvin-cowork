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
	scoped.caps.ContextEngineHost = []string{"assembler"}
	open := newStub("open")
	mustRegister(t, scoped)
	mustRegister(t, open)

	got := Rank(SupportContext{ContextEngine: "other"})
	if len(got) != 1 || got[0].Harness.ID() != "open" {
		t.Fatalf("Rank = %v, want only open", ids(got))
	}
	if got := Rank(SupportContext{ContextEngine: "assembler"}); len(got) != 2 {
		t.Fatalf("Rank = %v, want both", ids(got))
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

func TestRankAddsAutoSelectionPriorityBonus(t *testing.T) {
	resetGlobals(t)

	boosted := autoStub{newStub("boosted")}
	boosted.auto = &AutoSelectionHint{Priority: 5}
	plain := newStub("aaa-plain")
	plain.supports = func(SupportContext) SupportResult {
		return SupportResult{Supported: true, Priority: 3}
	}
	mustRegister(t, boosted)
	mustRegister(t, plain)

	got := ids(Rank(SupportContext{}))
	if got[0] != "boosted" {
		t.Fatalf("Rank = %v, want boosted first", got)
	}
}

func TestCapabilitiesHelpers(t *testing.T) {
	var empty Capabilities
	if !empty.HostsContextEngine("anything") {
		t.Fatal("an empty host list must accept every engine")
	}
	if !empty.AllowsDelegation("") {
		t.Fatal("an absent plugin id must not be treated as delegation")
	}
	if empty.AllowsDelegation("acpx") {
		t.Fatal("an empty delegation list must reject named plugins")
	}
	if empty.Declares(Capability("unknown")) {
		t.Fatal("Declares accepted an unknown capability")
	}

	var hint *AutoSelectionHint
	if !hint.Matches("anything") {
		t.Fatal("a nil hint must accept every provider")
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
