package harness

import "context"

// EmbeddedID is the registry id of the in-process harness.
const EmbeddedID = "embedded"

// Runner drives one attempt against a bound agent runtime.
type Runner func(ctx context.Context, params RunAttemptParams) (*AttemptResult, error)

// EmbeddedConfig wires the in-process harness to the agent runtime.
//
// Every hook is optional: an unset hook leaves the matching capability
// undeclared, and calling it returns ErrNotImplemented. This keeps the
// harness constructible by the wiring layer before every capability has a
// backing implementation, without the harness package importing the runtime.
type EmbeddedConfig struct {
	// Label overrides the default human-readable name.
	Label string
	// Run executes an attempt. A nil Run makes RunAttempt return
	// ErrNotImplemented.
	Run Runner
	// Compact shrinks the session context.
	Compact func(ctx context.Context, params CompactParams) (*CompactResult, error)
	// Reset drops per-session runtime state. The lifecycle generation is
	// bumped regardless of whether this hook is set.
	Reset func(ctx context.Context, params ResetParams) error
	// Dispose releases process-level resources.
	Dispose func(ctx context.Context) error

	// Providers restricts the harness to auto-selection for these provider
	// ids. Empty leaves eligibility to Supports rather than marking the
	// harness explicit-only.
	Providers []string
	// Priority biases auto-selection against other registered harnesses. It
	// is reported through Supports, the single source of ranking priority.
	Priority int
	// ContextEngineHost lists the host facilities this harness provides.
	ContextEngineHost []ContextEngineHostCapability
	// DeliveryDefaults is the reply-delivery preference; nil leaves it
	// undeclared.
	DeliveryDefaults *DeliveryDefaults
}

type embedded struct {
	cfg EmbeddedConfig
}

// NewEmbedded builds the harness that runs attempts inside this process.
func NewEmbedded(cfg EmbeddedConfig) Harness {
	return &embedded{cfg: cfg}
}

func (e *embedded) ID() string       { return EmbeddedID }
func (e *embedded) PluginID() string { return "" }

func (e *embedded) Label() string {
	if e.cfg.Label != "" {
		return e.cfg.Label
	}
	return "Darvin-Cowork embedded agent"
}

func (e *embedded) Capabilities() Capabilities {
	return Capabilities{
		Healthy:           e.cfg.Run != nil,
		Compact:           e.cfg.Compact != nil,
		ContextEngineHost: e.cfg.ContextEngineHost,
		DeliveryDefaults:  e.cfg.DeliveryDefaults,
	}
}

// AutoSelection returns nil when no allowlist is configured: an empty
// Providers slice would mark the harness explicit-only, which is the opposite
// of the in-process default.
func (e *embedded) AutoSelection() *AutoSelectionHint {
	if len(e.cfg.Providers) == 0 {
		return nil
	}
	return &AutoSelectionHint{Providers: e.cfg.Providers}
}

func (e *embedded) Supports(sc SupportContext) SupportResult {
	if e.cfg.Run == nil {
		return SupportResult{Reason: "no runner bound"}
	}
	if !e.AutoSelection().Eligible(sc.Provider) {
		return SupportResult{Reason: "provider is not auto-selectable"}
	}
	return SupportResult{Supported: true, Priority: e.cfg.Priority}
}

func (e *embedded) RunAttempt(ctx context.Context, params RunAttemptParams) (*AttemptResult, error) {
	if e.cfg.Run == nil {
		return nil, ErrNotImplemented
	}
	return e.cfg.Run(ctx, params)
}

func (e *embedded) Compact(ctx context.Context, params CompactParams) (*CompactResult, error) {
	if e.cfg.Compact == nil {
		return nil, ErrNotImplemented
	}
	return e.cfg.Compact(ctx, params)
}

// Reset always advances the session's lifecycle generation so an attempt
// still in flight is reported as superseded, even when no Reset hook is set.
func (e *embedded) Reset(ctx context.Context, params ResetParams) error {
	BumpLifecycleGeneration(params.SessionID)
	if e.cfg.Reset == nil {
		return nil
	}
	return e.cfg.Reset(ctx, params)
}

func (e *embedded) Dispose(ctx context.Context) error {
	if e.cfg.Dispose == nil {
		return nil
	}
	return e.cfg.Dispose(ctx)
}
