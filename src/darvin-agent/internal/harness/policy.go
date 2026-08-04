package harness

import (
	"fmt"
	"strings"
)

// Policy is the configured preference for which harness serves a session.
// The zero Policy means "auto": rank every registered harness and take the
// best candidate.
type Policy struct {
	// HarnessID pins one harness by id. Empty means auto-selection.
	HarnessID string
	// AllowFallback lets a pinned-but-missing harness fall back to
	// auto-selection instead of failing.
	AllowFallback bool
}

// ParsePolicy reads a configured policy string. A bare id pins that harness
// and falls back to auto-selection when it is absent; a trailing "!" makes
// the pin strict, so a missing harness is an error. "auto" and "" both mean
// auto-selection.
func ParsePolicy(raw string) Policy {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.EqualFold(trimmed, "auto") {
		return Policy{AllowFallback: true}
	}
	if strict := strings.TrimSuffix(trimmed, "!"); strict != trimmed {
		return Policy{HarnessID: strings.TrimSpace(strict)}
	}
	return Policy{HarnessID: trimmed, AllowFallback: true}
}

// String renders the policy back into its ParsePolicy form.
func (p Policy) String() string {
	if p.HarnessID == "" {
		return "auto"
	}
	if p.AllowFallback {
		return p.HarnessID
	}
	return p.HarnessID + "!"
}

// Resolve picks the harness for sc.
//
// An explicit sc.RequestedHarnessID always wins and never falls back — a
// caller naming a harness wants that harness or an error. Otherwise the
// policy's pin is tried, then auto-selection via Rank.
func (p Policy) Resolve(sc SupportContext) (Harness, error) {
	if id := strings.TrimSpace(sc.RequestedHarnessID); id != "" {
		h, ok := Get(id)
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrNotRegistered, id)
		}
		return h, nil
	}
	if p.HarnessID != "" {
		if h, ok := Get(p.HarnessID); ok {
			return h, nil
		}
		if !p.AllowFallback {
			return nil, fmt.Errorf("%w: %q", ErrNotRegistered, p.HarnessID)
		}
	}
	ranked := Rank(sc)
	if len(ranked) == 0 {
		return nil, ErrNoCandidate
	}
	return ranked[0].Harness, nil
}
