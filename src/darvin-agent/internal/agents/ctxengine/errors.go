// Sentinel errors for the context engine seams.

package ctxengine

import (
	"errors"
	"fmt"
)

// ErrNotImplemented is returned by ContextEngine methods that are exposed as interface seams but have no implementation yet.
var ErrNotImplemented = errors.New("ctxengine: not implemented (TODO seam)")

// ErrSubAgentUnsupported wraps ErrNotImplemented with a sub-agent tag
// so callers can disambiguate via errors.Is.
var ErrSubAgentUnsupported = fmt.Errorf("%w (sub-agent)", ErrNotImplemented)

// ErrAssemblerNotConfigured is returned by the agent layer when no
// assembler is wired into Agent.New and cfg.AssemblerEnabled is true.
var ErrAssemblerNotConfigured = errors.New("ctxengine: assembler not configured on Agent")

// ErrCompactUnrecoverable is returned by Compact when repeated retries
// still leave the message set above the budget.
var ErrCompactUnrecoverable = errors.New("ctxengine: compact could not converge under retry budget")
