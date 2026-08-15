// Wires the embedded harness that drives agent prompts and runs.

package runtime

import (
	"context"

	agent "darvin-cowork/backend/internal/agents"
	"darvin-cowork/backend/internal/harness"
	"darvin-cowork/backend/internal/sessionruntime"
)

// newEmbeddedHarness builds the in-process harness that drives
// agent.Prompt + agent.Run. This is the only prompt path the
// darvin-agent process uses: gateway handler receives a prompt →
// Loop.Submit → harness.RunAttemptWithLifecycle → here → a.Prompt
// + a.Run → executor.RunConversation.
func newEmbeddedHarness(a *agent.Agent) harness.Harness {
	return harness.NewEmbedded(harness.EmbeddedConfig{
		Run: func(ctx context.Context, p harness.RunAttemptParams) (*harness.AttemptResult, error) {
			// Apply a per-run provider / model override so the executor
			// resolves Deps.Provider() / ModelName() to the requested preset.
			// An unknown provider surfaces as a clear error rather than
			// sending a foreign model id to the wrong wire (404).
			if p.Provider != "" || p.Model != "" {
				if err := a.SetRunModel(p.Provider, p.Model); err != nil {
					return nil, err
				}
				defer a.ClearRunModel()
			}
			if err := a.Prompt(ctx, p.Prompt, nil, p.Attachments); err != nil {
				return nil, err
			}
			_ = a.Run(ctx)
			return &harness.AttemptResult{Status: harness.AttemptOK}, nil
		},
	})
}

// defaultHarnessSelector is the runtime's default factory.Selector:
// every agent is driven by an embedded harness closure that calls
// back into that exact agent instance.
func defaultHarnessSelector(a *agent.Agent, _ *sessionruntime.AgentFactory) (harness.Harness, error) {
	return newEmbeddedHarness(a), nil
}
