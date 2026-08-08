// Implements the mock CLI harness backend used in selection and ctx-engine tests.

package harness

import (
	"context"
	"fmt"
	"time"
)

// CLIDefaultPriority is the mock CLI's Supports priority; negative so the
// embedded runtime (priority 0) wins ties and the CLI is only picked when
// explicitly requested or the embedded runtime is unhealthy.
const CLIDefaultPriority = -100

// CLIHarness is a mock CLI backend exercising selection and the ctx-engine
// host gate without a real subprocess. It can bootstrap / run / maintain
// but has no assemble-before-prompt or compact facility, so a demanding
// context engine cannot run on it. Stays as a test fixture and a reference
// shape for a future real CLI backend.
type CLIHarness struct {
	// Label overrides the default human-readable name.
	Label string
	// Run overrides the canned RunAttempt; nil echoes the prompt back.
	Run func(ctx context.Context, params RunAttemptParams) (*AttemptResult, error)
}

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

// Capabilities advertises the generic-CLI host verb set, omitting
// HostAssembleBeforePrompt and HostCompact — exactly what the host gate
// rejects a demanding context engine for.
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
	// Mock: consume the prompt and reply immediately (a settled turn).
	return &AttemptResult{
		Status:        AttemptOK,
		AssistantText: fmt.Sprintf("[mock cli] %s", params.Prompt),
		TotalTurns:    1,
		DurationMs:    int64(time.Millisecond),
	}, nil
}

func (c *cli) Reset(context.Context, ResetParams) error { return nil }
func (c *cli) Dispose(context.Context) error            { return nil }
