// Sub-agent interface seams that return ErrSubAgentUnsupported by default.

package ctxengine

import "context"

// PrepareSubagentSpawn returns ErrSubAgentUnsupported. The interface
// method exists so a SubAgent implementation can override it without
// changing the ContextEngine interface.
func (a *DefaultAssembler) PrepareSubagentSpawn(ctx context.Context, p SubagentSpawnParams) (*SubagentSpawnPreparation, error) {
	return nil, ErrSubAgentUnsupported
}

// OnSubagentEnded returns ErrSubAgentUnsupported.
func (a *DefaultAssembler) OnSubagentEnded(ctx context.Context, p SubagentEndedParams) error {
	return ErrSubAgentUnsupported
}
