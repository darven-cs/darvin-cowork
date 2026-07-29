package ctxengine

import (
	"errors"
	"fmt"
)

// ErrNotImplementedInV0 is returned by ContextEngine methods that are
// reserved for future milestones but exposed as interface seams (so the
// interface signature stays stable across spec iterations).
var ErrNotImplementedInV0 = errors.New("ctxengine: not implemented in v0 (TODO seam)")

// ErrSubAgentUnsupported wraps ErrNotImplementedInV0 with a sub-agent tag
// so callers can disambiguate via errors.Is.
var ErrSubAgentUnsupported = fmt.Errorf("%w (sub-agent)", ErrNotImplementedInV0)

// ErrAssemblerNotConfigured is returned by the agent layer when no
// assembler is wired into Agent.New and cfg.AssemblerEnabled is true.
var ErrAssemblerNotConfigured = errors.New("ctxengine: assembler not configured on Agent")

// ErrCompactUnrecoverable is returned by Compact when repeated retries
// still leave the message set above the budget.
var ErrCompactUnrecoverable = errors.New("ctxengine: compact could not converge under retry budget")
