// Verifies capability declarations and carries the selection support
// types (SupportContext / SupportResult / AutoSelectionHint) used to
// rank harnesses for a session.

package harness

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// allCapabilities is the verification order; it doubles as the set
// VerifyCapabilities walks.
var allCapabilities = []Capability{
	CapCompact,
	CapClassify,
	CapSideQuestion,
	CapSessionFork,
	CapFinalizeSettledTurn,
	CapUsageSnapshot,
}

// VerifyCapabilities reports every capability h declares without implementing
// the matching interface. The reverse is legal: a harness may implement an
// interface and leave it undeclared when the backing dependency is absent.
func VerifyCapabilities(h Harness) error {
	if h == nil {
		return ErrHarnessRequired
	}
	caps := h.Capabilities()
	var missing []string
	for _, cap := range allCapabilities {
		if caps.Declares(cap) && !implementsCapability(h, cap) {
			missing = append(missing, string(cap))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("harness %q declares unimplemented capabilities: %s",
		h.ID(), strings.Join(missing, ", "))
}

// Implements reports whether h both declares cap and satisfies its interface.
// Callers use it instead of a bare type assertion so an undeclared capability
// stays inert even when the concrete type happens to carry the method.
func Implements(h Harness, cap Capability) bool {
	if h == nil || !h.Capabilities().Declares(cap) {
		return false
	}
	return implementsCapability(h, cap)
}

// Compact runs h's compaction, or returns ErrNotImplemented when h does not
// serve the capability.
func Compact(ctx context.Context, h Harness, params CompactParams) (*CompactResult, error) {
	if !Implements(h, CapCompact) {
		return nil, ErrNotImplemented
	}
	return h.(Compactor).Compact(ctx, params)
}

// Candidate is a harness scored against one SupportContext.
type Candidate struct {
	Harness Harness
	Result  SupportResult
}

// Rank returns every registered harness that supports sc, best first.
//
// A harness is filtered out when it is unhealthy, misses a host capability the
// caller's context engine requires, refuses the delegating plugin, falls
// outside its own AutoSelection allowlist, or answers Supports with false.
//
// Priority comes from Supports and nowhere else, so a harness cannot have the
// same score counted twice. Ties break on ascending id.
func Rank(sc SupportContext) []Candidate {
	var out []Candidate
	for _, reg := range List() {
		h := reg.Harness
		caps := h.Capabilities()
		if !caps.Healthy {
			continue
		}
		if len(caps.MissingHostCapabilities(sc.ContextEngine)) > 0 {
			continue
		}
		if sc.PluginID != "" && !caps.AllowsDelegation(sc.PluginID) {
			continue
		}
		if sel, ok := h.(AutoSelector); ok && !sel.AutoSelection().Eligible(sc.Provider) {
			continue
		}
		res := h.Supports(sc)
		if !res.Supported {
			continue
		}
		out = append(out, Candidate{Harness: h, Result: res})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Result.Priority != out[j].Result.Priority {
			return out[i].Result.Priority > out[j].Result.Priority
		}
		return out[i].Harness.ID() < out[j].Harness.ID()
	})
	return out
}

// describeMissingHostCapabilities renders a rejection that names the missing
// verbs alongside what was required and what the harness actually provides,
// so a failure says which facility is absent rather than only that one is.
func describeMissingHostCapabilities(h Harness, req *ContextEngineRequirement, missing []ContextEngineHostCapability) string {
	if req.UnsupportedMessage != "" {
		return req.UnsupportedMessage
	}
	return fmt.Sprintf(
		"context engine %q cannot run operation %q on harness %q: missing host capabilities: %s; required: %s; host provides: %s",
		req.EngineID, req.Operation, h.ID(),
		joinHostCapabilities(missing),
		joinHostCapabilities(req.RequiredCapabilities),
		joinHostCapabilities(h.Capabilities().ContextEngineHost),
	)
}

func joinHostCapabilities(list []ContextEngineHostCapability) string {
	if len(list) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(list))
	for _, v := range list {
		parts = append(parts, string(v))
	}
	return strings.Join(parts, ", ")
}

func implementsCapability(h Harness, cap Capability) bool {
	switch cap {
	case CapCompact:
		_, ok := h.(Compactor)
		return ok
	case CapClassify:
		_, ok := h.(Classifier)
		return ok
	case CapSideQuestion:
		_, ok := h.(SideQuestioner)
		return ok
	case CapSessionFork:
		_, ok := h.(SessionForker)
		return ok
	case CapFinalizeSettledTurn:
		_, ok := h.(TurnFinalizer)
		return ok
	case CapUsageSnapshot:
		_, ok := h.(UsageReporter)
		return ok
	default:
		return false
	}
}

// ProviderOwnership records which plugins claim ownership of a provider
// (Status: "unowned" | "owned" | "ambiguous").
type ProviderOwnership struct {
	Status    string
	PluginIDs []string
}

// SupportContext is what selection knows about a session before picking a
// harness for it.
type SupportContext struct {
	SessionID  string
	SessionKey string

	Provider string
	Model    string

	// RequestedRuntime is the harness id the caller's config asked for;
	// empty means auto. Non-empty applies a priority boost after hard filters.
	RequestedRuntime string
	// ProviderOwnership is the requested provider's ownership record.
	ProviderOwnership *ProviderOwnership

	// ContextEngine is what the caller's context engine requires; nil = none.
	ContextEngine *ContextEngineRequirement
	// PluginID is set when the call is delegated from a plugin.
	PluginID string
	// RequestedHarnessID pins a harness explicitly, bypassing scoring.
	RequestedHarnessID string
}

// SupportResult is one harness's answer to a SupportContext. Higher
// Priority wins; ties break on harness id so ordering is deterministic.
type SupportResult struct {
	Supported bool
	Priority  int
	// Reason explains a refusal; surfaced in diagnostics only.
	Reason string
}

// AutoSelectionHint is a harness's static provider allowlist; it only
// filters (no priority contribution). A non-nil empty Providers slice
// marks the harness explicit-only.
type AutoSelectionHint struct {
	Providers []string
}

// Eligible reports whether the hint admits provider; nil hint / nil
// Providers defers to Supports, an empty slice marks explicit-only.
func (h *AutoSelectionHint) Eligible(provider string) bool {
	if h == nil || h.Providers == nil {
		return true
	}
	if len(h.Providers) == 0 {
		return false
	}
	return containsProvider(h.Providers, provider)
}

// normalizeProviderID lowercases and trims a provider id so an allowlist
// entry and a request that differ only in case / padding still match.
func normalizeProviderID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

func containsProvider(list []string, want string) bool {
	want = normalizeProviderID(want)
	for _, v := range list {
		if normalizeProviderID(v) == want {
			return true
		}
	}
	return false
}
