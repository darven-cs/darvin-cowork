// Tests for harness-selection policy parsing.

package harness

import (
	"errors"
	"testing"
)

func TestParsePolicy(t *testing.T) {
	cases := []struct {
		raw  string
		want Policy
	}{
		{"", Policy{AllowFallback: true}},
		{"   ", Policy{AllowFallback: true}},
		{"auto", Policy{AllowFallback: true}},
		{"AUTO", Policy{AllowFallback: true}},
		{"embedded", Policy{HarnessID: "embedded", AllowFallback: true}},
		{"  embedded  ", Policy{HarnessID: "embedded", AllowFallback: true}},
		{"embedded!", Policy{HarnessID: "embedded"}},
	}
	for _, tc := range cases {
		if got := ParsePolicy(tc.raw); got != tc.want {
			t.Fatalf("ParsePolicy(%q) = %+v, want %+v", tc.raw, got, tc.want)
		}
	}
}

func TestPolicyStringRoundTrips(t *testing.T) {
	for _, raw := range []string{"auto", "embedded", "embedded!"} {
		if got := ParsePolicy(raw).String(); got != raw {
			t.Fatalf("ParsePolicy(%q).String() = %q", raw, got)
		}
	}
}

func TestResolveRequestedHarnessWins(t *testing.T) {
	resetGlobals(t)

	mustRegister(t, newStub("embedded"))
	mustRegister(t, newStub("cli"))

	got, err := Policy{HarnessID: "embedded"}.Resolve(SupportContext{RequestedHarnessID: "cli"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ID() != "cli" {
		t.Fatalf("Resolve = %q, want the explicitly requested cli", got.ID())
	}
}

func TestResolveRequestedHarnessNeverFallsBack(t *testing.T) {
	resetGlobals(t)

	mustRegister(t, newStub("embedded"))

	_, err := Policy{HarnessID: "embedded", AllowFallback: true}.
		Resolve(SupportContext{RequestedHarnessID: "cli"})
	if !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("err = %v, want ErrNotRegistered", err)
	}
}

func TestResolvePinnedHarness(t *testing.T) {
	resetGlobals(t)

	mustRegister(t, newStub("cli"))
	mustRegister(t, newStub("embedded"))

	got, err := Policy{HarnessID: "embedded"}.Resolve(SupportContext{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ID() != "embedded" {
		t.Fatalf("Resolve = %q, want embedded", got.ID())
	}
}

func TestResolveMissingPinFallsBack(t *testing.T) {
	resetGlobals(t)

	mustRegister(t, newStub("embedded"))

	got, err := Policy{HarnessID: "cli", AllowFallback: true}.Resolve(SupportContext{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ID() != "embedded" {
		t.Fatalf("Resolve = %q, want the auto-selected embedded", got.ID())
	}
}

func TestResolveStrictPinErrors(t *testing.T) {
	resetGlobals(t)

	mustRegister(t, newStub("embedded"))

	_, err := ParsePolicy("cli!").Resolve(SupportContext{})
	if !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("err = %v, want ErrNotRegistered", err)
	}
}

func TestResolveAutoPicksBestCandidate(t *testing.T) {
	resetGlobals(t)

	best := newStub("zulu")
	best.supports = func(SupportContext) SupportResult {
		return SupportResult{Supported: true, Priority: 7}
	}
	mustRegister(t, newStub("alpha"))
	mustRegister(t, best)

	got, err := Policy{}.Resolve(SupportContext{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ID() != "zulu" {
		t.Fatalf("Resolve = %q, want zulu", got.ID())
	}
}

func TestResolveNoCandidate(t *testing.T) {
	resetGlobals(t)

	if _, err := (Policy{}).Resolve(SupportContext{}); !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("err = %v, want ErrNoCandidate", err)
	}

	sick := newStub("embedded")
	sick.caps.Healthy = false
	mustRegister(t, sick)
	if _, err := (Policy{}).Resolve(SupportContext{}); !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("err = %v, want ErrNoCandidate for an unhealthy-only registry", err)
	}
}
