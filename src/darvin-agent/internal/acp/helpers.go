package acp

import (
	"errors"

	agent "darvin-cowork/backend/internal/agents"
	"darvin-cowork/backend/internal/agents/queue"
	"darvin-cowork/backend/internal/harness"
	"darvin-cowork/backend/internal/llm"
)

// errNoHarness is surfaced when a session has no harness bound. Spec 04
// §4.2: every AcpSession is built with a Harness by factory.resolveHarness.
// A nil here means the wiring is wrong; the renderer's bubble needs an
// explicit AgentErrorEvent or it stays in streaming state.
var errNoHarness = errors.New("acp: session has no harness bound")

// attachmentsToImages converts the queue.ImageRef slice the renderer sent
// into the harness.ImageAttachment shape. Field-for-field conversion; the
// DataURL is forwarded as-is and the harness defers splitting to the
// executor (or the embedded harness's runner closure).
func attachmentsToImages(refs []queue.ImageRef) []harness.ImageAttachment {
	if len(refs) == 0 {
		return nil
	}
	out := make([]harness.ImageAttachment, 0, len(refs))
	for _, r := range refs {
		out = append(out, harness.ImageAttachment{
			Path:    r.Path,
			Name:    r.Name,
			Size:    r.Size,
			DataURL: r.DataURL,
		})
	}
	return out
}

// extractProviderName reads the provider name off the agent. llm.ModelProvider
// is the canonical source: agent.Agent wires the provider through NewAgentConfig
// and exposes it via Provider(). A nil agent (e.g. the factory calls this
// before Build returns) yields the empty string.
func extractProviderName(a *agent.Agent) string {
	if a == nil {
		return ""
	}
	p := a.Provider()
	if p == nil {
		return ""
	}
	if n, ok := p.(interface{ Name() string }); ok {
		return n.Name()
	}
	// Fallback: if the provider does not implement Name(), return the
	// empty string. Selection will treat empty provider as "no constraint"
	// rather than misidentifying it.
	var _ llm.ModelProvider = p
	return ""
}
