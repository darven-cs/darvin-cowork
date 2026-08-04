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
// A harness is filtered out when it is unhealthy, cannot host the requested
// context engine, refuses the delegating plugin, falls outside its own
// AutoSelection provider allowlist, or answers Supports with false. The
// surviving candidates sort by descending priority (Supports plus the
// AutoSelection bonus) then ascending id.
func Rank(sc SupportContext) []Candidate {
	var out []Candidate
	for _, reg := range List() {
		h := reg.Harness
		caps := h.Capabilities()
		if !caps.Healthy {
			continue
		}
		if !caps.HostsContextEngine(sc.ContextEngine) {
			continue
		}
		if sc.PluginID != "" && !caps.AllowsDelegation(sc.PluginID) {
			continue
		}
		bonus := 0
		if sel, ok := h.(AutoSelector); ok {
			hint := sel.AutoSelection()
			if !hint.Matches(sc.Provider) {
				continue
			}
			if hint != nil {
				bonus = hint.Priority
			}
		}
		res := h.Supports(sc)
		if !res.Supported {
			continue
		}
		res.Priority += bonus
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
