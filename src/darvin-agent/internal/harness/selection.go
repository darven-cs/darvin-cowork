package harness

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// RequestedRuntimeBoost is the priority bump applied when a harness id
// matches SupportContext.RequestedRuntime. The value is chosen to dominate
// any realistic Supports priority without overflowing into overflow-prone
// arithmetic; OpenClaw uses the same shape.
const RequestedRuntimeBoost = 1000

// SelectionParams is the wiring-layer input to SelectHarness.
//
// BuildSupportContext (in cmd/app or internal/harnesswiring) is responsible
// for translating params into a populated SupportContext; the harness
// package itself only consumes the already-resolved facts.
type SelectionParams struct {
	Provider   string
	Model      string
	SessionID  string
	SessionKey string
	Context    SupportContext

	// ExplicitHarnessID pins a harness by id, bypassing all scoring. An
	// empty value falls through to policy-driven selection.
	ExplicitHarnessID string
	// DefaultHarnessID is the harness id from config.agents.defaults.harness,
	// used when the caller's policy does not pin anything explicitly.
	DefaultHarnessID string
}

// Decision is the outcome of one SelectHarness call. It records what was
// chosen, why, and which alternatives were considered so the caller can
// surface the reasoning in diagnostics.
type Decision struct {
	Harness        Harness
	SelectedID     string
	SelectedReason SelectionReason
	Candidates     []CandidateReport
}

// SelectionReason names why a particular harness was selected. The set is
// stable and part of the diagnostic surface.
type SelectionReason string

const (
	ReasonForcedPlugin              SelectionReason = "forced_plugin"
	ReasonForcedDefaultPlugin       SelectionReason = "forced_default_plugin"
	ReasonDefaultPluginUnavailable  SelectionReason = "default_plugin_unavailable"
	ReasonImplicitPluginUnavailable SelectionReason = "implicit_plugin_unavailable"
	ReasonImplicitPluginUnsupported SelectionReason = "implicit_plugin_unsupported"
	ReasonAutoPlugin                SelectionReason = "auto_plugin"
	ReasonAutoEmbeddedFallback      SelectionReason = "auto_embedded_fallback"
)

// CandidateReport is the diagnostic view of one harness considered during
// selection. It is named distinctly from the existing Candidate (which
// carries the Harness and Result for ranking).
type CandidateReport struct {
	ID         string
	Label      string
	PluginID   string
	Considered bool
	Supported  bool
	Priority   int
	Reason     string
}

// SelectHarness resolves params into one harness.
//
// Decision tree (matches the spec, with ExplicitHarnessID given top priority):
//
//  1. ExplicitHarnessID non-empty → must be registered or fail.
//  2. DefaultHarnessID non-empty → resolve; on miss fall back to auto.
//  3. Context.RequestedRuntime non-empty → boost matching harnesses; if the
//     top-scoring candidate after the boost is a hit, take it; otherwise
//     fall back to embedded with ReasonDefaultPluginUnavailable.
//  4. Auto scoring via Rank → pick the top; if Rank is empty, fall back to
//     embedded with ReasonAutoEmbeddedFallback.
func SelectHarness(params SelectionParams) (*Decision, error) {
	candidates := reportCandidates()

	if id := strings.TrimSpace(params.ExplicitHarnessID); id != "" {
		h, ok := Get(id)
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrNotRegistered, id)
		}
		return &Decision{
			Harness:        h,
			SelectedID:     h.ID(),
			SelectedReason: ReasonForcedPlugin,
			Candidates:     candidates,
		}, nil
	}

	defaultMiss := false
	if id := strings.TrimSpace(params.DefaultHarnessID); id != "" {
		if h, ok := Get(id); ok {
			return &Decision{
				Harness:        h,
				SelectedID:     h.ID(),
				SelectedReason: ReasonForcedDefaultPlugin,
				Candidates:     candidates,
			}, nil
		}
		defaultMiss = true
	}

	if rt := strings.TrimSpace(params.Context.RequestedRuntime); rt != "" {
		if hit, ok := Get(rt); ok {
			if hit.Supports(params.Context).Supported {
				return &Decision{
					Harness:        hit,
					SelectedID:     hit.ID(),
					SelectedReason: ReasonAutoPlugin,
					Candidates:     candidates,
				}, nil
			}
			if embedded, ok := Get(EmbeddedID); ok && embedded.Capabilities().Healthy {
				return &Decision{
					Harness:        embedded,
					SelectedID:     embedded.ID(),
					SelectedReason: ReasonImplicitPluginUnsupported,
					Candidates:     candidates,
				}, nil
			}
		}
		if embedded, ok := Get(EmbeddedID); ok && embedded.Capabilities().Healthy {
			return &Decision{
				Harness:        embedded,
				SelectedID:     embedded.ID(),
				SelectedReason: ReasonImplicitPluginUnavailable,
				Candidates:     candidates,
			}, nil
		}
	}

	if len(candidates) == 0 {
		return nil, ErrNoCandidate
	}

	auto := autoSelect(params.Context)
	reason := ReasonAutoPlugin
	switch {
	case defaultMiss:
		reason = ReasonDefaultPluginUnavailable
	case auto.Harness.ID() == EmbeddedID:
		reason = ReasonAutoEmbeddedFallback
	}
	return &Decision{
		Harness:        auto.Harness,
		SelectedID:     auto.Harness.ID(),
		SelectedReason: reason,
		Candidates:     candidates,
	}, nil
}

// autoSelect picks the best harness by Rank. Caller is responsible for
// fallback semantics.
func autoSelect(sc SupportContext) Candidate {
	ranked := RankWithBoost(sc)
	if len(ranked) == 0 {
		embedded, _ := Get(EmbeddedID)
		return Candidate{Harness: embedded, Result: SupportResult{}}
	}
	return ranked[0]
}

// firstMatching returns the highest-ranked harness whose ID matches the
// requested runtime after the boost is applied.
func firstMatching(sc SupportContext, runtimeID string) (Harness, bool) {
	for _, c := range RankWithBoost(sc) {
		if c.Harness.ID() == runtimeID {
			return c.Harness, true
		}
	}
	return nil, false
}

// reportCandidates snapshots the registered harnesses as diagnostics. The
// "Considered" flag is false until SelectHarness narrows down; we record
// every registration so the caller can see what was on the table.
func reportCandidates() []CandidateReport {
	out := make([]CandidateReport, 0)
	for _, reg := range List() {
		h := reg.Harness
		caps := h.Capabilities()
		out = append(out, CandidateReport{
			ID:         h.ID(),
			Label:      h.Label(),
			PluginID:   h.PluginID(),
			Considered: caps.Healthy,
			Supported:  caps.Healthy,
			Reason:     "",
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// RankWithBoost is Rank plus the RequestedRuntime priority bump. Callers
// that do not care about the boost can keep using Rank.
func RankWithBoost(sc SupportContext) []Candidate {
	ranked := Rank(sc)
	if strings.TrimSpace(sc.RequestedRuntime) == "" {
		return ranked
	}
	for i := range ranked {
		if ranked[i].Harness.ID() == sc.RequestedRuntime {
			ranked[i].Result.Priority += RequestedRuntimeBoost
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Result.Priority != ranked[j].Result.Priority {
			return ranked[i].Result.Priority > ranked[j].Result.Priority
		}
		return ranked[i].Harness.ID() < ranked[j].Harness.ID()
	})
	return ranked
}

// ResolvePolicy turns params into a Policy. DefaultHarnessID pin is
// honoured; an explicit run-level pin lives in SupportContext and is
// handled inside SelectHarness.
//
// Deprecated: callers should pass params.DefaultHarnessID to SelectHarness
// directly. Kept so existing tests and the wiring layer can construct a
// Policy without duplicating the parsing rules.
func ResolvePolicy(params SelectionParams) Policy {
	if id := strings.TrimSpace(params.DefaultHarnessID); id != "" {
		return Policy{HarnessID: id, AllowFallback: true}
	}
	return Policy{AllowFallback: true}
}

// ErrSelectionFailed wraps lower-level lookup failures with selection
// context.
var ErrSelectionFailed = errors.New("harness: selection failed")
