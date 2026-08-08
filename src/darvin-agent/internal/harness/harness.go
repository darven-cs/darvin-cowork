// Package harness abstracts "a backend that can run one agent attempt".
// It declares its own Usage / ImageAttachment / ToolCallRecord shapes
// instead of importing internal/agents so any backend (CLI subprocess,
// remote service) can implement it; the wiring layer owns conversion.
package harness

import (
	"context"
	"errors"
)

// ErrNotImplemented is returned for a capability a harness does not support.
var ErrNotImplemented = errors.New("harness: capability not implemented")

var (
	ErrIDRequired               = errors.New("harness: ID is required")
	ErrHarnessRequired          = errors.New("harness: harness is required")
	ErrSessionIDRequired        = errors.New("harness: SessionID is required")
	ErrPromptRequired           = errors.New("harness: Prompt is required")
	ErrNotRegistered            = errors.New("harness: not registered")
	ErrNoCandidate              = errors.New("harness: no supporting harness")
	ErrContextEngineUnsupported = errors.New("harness: context engine unsupported")
	ErrPluginIDMismatch         = errors.New("harness: PluginID disagrees with owner")
)

// Harness is the surface every backend must implement. Optional behaviour
// lives in the capability interfaces below; a harness opts in by
// implementing the interface and declaring it in Capabilities.
//
// Implementations are process-level singletons; RunAttempt is stateless.
type Harness interface {
	// ID is the registry key; Label is the human-readable name;
	// PluginID names the registering plugin ("" for built-ins).
	ID() string
	Label() string
	PluginID() string

	// Capabilities declares what this harness can do (read without
	// instantiating a session). Supports scores it for one context.
	Capabilities() Capabilities
	Supports(SupportContext) SupportResult

	// RunAttempt drives one prompt to completion; callers use
	// RunAttemptWithLifecycle rather than calling this directly.
	RunAttempt(ctx context.Context, params RunAttemptParams) (*AttemptResult, error)

	// Reset drops per-session state; Dispose releases process resources.
	Reset(ctx context.Context, params ResetParams) error
	Dispose(ctx context.Context) error
}

// Optional capability interfaces; a harness opts in by implementing the
// interface and declaring it in Capabilities.
type (
	// Compactor shrinks a session's context.
	Compactor interface {
		Compact(ctx context.Context, params CompactParams) (*CompactResult, error)
	}
	// Classifier labels a finished attempt (drift / stall detection).
	Classifier interface {
		Classify(ctx context.Context, result *AttemptResult, params *RunAttemptParams) Classification
	}
	// SideQuestioner answers a one-off question outside the turn loop.
	SideQuestioner interface {
		RunSideQuestion(ctx context.Context, params SideQuestionParams) (*SideQuestionResult, error)
	}
	// SessionForker branches a session into a new one.
	SessionForker interface {
		SessionFork(ctx context.Context, params SessionForkParams) (*SessionForkResult, error)
	}
	// TurnFinalizer runs after a turn settles.
	TurnFinalizer interface {
		FinalizeSettledTurn(ctx context.Context, params SettledTurnParams) (*SettledTurnResult, error)
	}
	// UsageReporter reports provider-side quota.
	UsageReporter interface {
		UsageSnapshot(ctx context.Context, params UsageSnapshotParams) (*UsageSnapshot, error)
	}
	// AutoSelector narrows a harness to a static provider allowlist.
	AutoSelector interface {
		AutoSelection() *AutoSelectionHint
	}
)
