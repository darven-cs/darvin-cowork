package harness

import (
	"context"
	"fmt"
	"time"
)

// CLIDefaultPriority is the Supports priority the mock CLI harness reports.
// Negative so the embedded runtime (priority 0) wins every tie; the CLI
// harness is only picked when explicitly requested or when the embedded
// runtime is absent / unhealthy.
const CLIDefaultPriority = -100

// CLIHarness is a mock CLI backend used to exercise selection (spec 03) and
// the ctx-engine host gate (spec 06) without a real subprocess.
//
// It models a generic CLI host: it can bootstrap a session, run turns and
// maintain state, but it cannot assemble a prompt before the model call and
// it has no compact facility. A context engine whose operation requires
// `assemble-before-prompt` therefore cannot run on it, and selection must
// fall back to the embedded runtime.
//
// The real CLI backend is a future spec; this type stays as a test fixture
// and a reference shape for that implementation.
type CLIHarness struct {
	// Label overrides the default human-readable name.
	Label string
	// Run overrides the canned RunAttempt behaviour. nil uses a mock that
	// echoes the prompt back.
	Run func(ctx context.Context, params RunAttemptParams) (*AttemptResult, error)
}

// NewCLI builds the mock CLI harness.
func NewCLI(cfg CLIHarness) Harness { return &cli{cfg: cfg} }

type cli struct{ cfg CLIHarness }

func (c *cli) ID() string       { return "cli" }
func (c *cli) PluginID() string { return "" }

func (c *cli) Label() string {
	if c.cfg.Label != "" {
		return c.cfg.Label
	}
	return "Mock CLI backend"
}

// Capabilities advertises the generic-CLI host verb set. Notably it omits
// HostAssembleBeforePrompt and HostCompact, so a demanding context engine
// cannot run here — that is exactly what spec 06's host gate exists for.
func (c *cli) Capabilities() Capabilities {
	return Capabilities{
		Healthy: true,
		ContextEngineHost: []ContextEngineHostCapability{
			HostBootstrap,
			HostAfterTurn,
			HostMaintain,
		},
	}
}

func (c *cli) Supports(SupportContext) SupportResult {
	return SupportResult{Supported: true, Priority: CLIDefaultPriority}
}

func (c *cli) RunAttempt(ctx context.Context, params RunAttemptParams) (*AttemptResult, error) {
	if c.cfg.Run != nil {
		return c.cfg.Run(ctx, params)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Mock behaviour: the "CLI" consumes the prompt and returns a canned
	// reply immediately, so a selection demo observes a settled turn.
	return &AttemptResult{
		Status:        AttemptOK,
		AssistantText: fmt.Sprintf("[mock cli] %s", params.Prompt),
		TotalTurns:    1,
		DurationMs:    int64(time.Millisecond),
	}, nil
}

func (c *cli) Reset(context.Context, ResetParams) error { return nil }
func (c *cli) Dispose(context.Context) error            { return nil }
