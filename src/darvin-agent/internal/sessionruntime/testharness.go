// Test-only embedded harness that drives an agent prompt + run loop.

package sessionruntime

import (
	"context"

	agent "darvin-cowork/backend/internal/agents"
	"darvin-cowork/backend/internal/harness"
)

// NewEmbeddedTestHarness builds an embedded harness that drives a single
// agent's prompt + run loop, mirroring what production wiring does
// inside the embedded harness's Run closure. The wiring lives here so
// gateway / acp tests can reuse it without depending on harness internals.
//
// The harness id is fixed at "embedded" so a registry lookup returns it
// (and so handlers can use it as a synthetic target when
// resolveHarness is exercised end-to-end).
func NewEmbeddedTestHarness(a *agent.Agent) harness.Harness {
	return harness.NewEmbedded(harness.EmbeddedConfig{
		Run: func(ctx context.Context, p harness.RunAttemptParams) (*harness.AttemptResult, error) {
			if err := a.Prompt(ctx, p.Prompt, nil, p.Attachments); err != nil {
				return nil, err
			}
			_ = a.Run(ctx)
			return &harness.AttemptResult{Status: harness.AttemptOK}, nil
		},
	})
}

// noopHarness is the cheap harness used by tests that only need
// resolveHarness to return *something*. It does not drive any agent.
type noopHarness struct{}

func (*noopHarness) ID() string                         { return "embedded" }
func (*noopHarness) Label() string                      { return "embedded" }
func (*noopHarness) PluginID() string                   { return "" }
func (*noopHarness) Capabilities() harness.Capabilities { return harness.Capabilities{Healthy: true} }
func (*noopHarness) Supports(harness.SupportContext) harness.SupportResult {
	return harness.SupportResult{Supported: true}
}
func (*noopHarness) RunAttempt(context.Context, harness.RunAttemptParams) (*harness.AttemptResult, error) {
	return &harness.AttemptResult{Status: harness.AttemptOK}, nil
}
func (*noopHarness) Reset(context.Context, harness.ResetParams) error { return nil }
func (*noopHarness) Dispose(context.Context) error                    { return nil }

// HarnessForTest is the no-op harness exported for tests that need a
// factory.Selector to return a fixed instance without registering one.
// Gateway tests in particular use it so resolveHarness passes the
// dependency check.
var HarnessForTest harness.Harness = &noopHarness{}
