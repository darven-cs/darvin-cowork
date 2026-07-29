package ctxengine

import "context"

// PrepareSubagentSpawn returns ErrSubAgentUnsupported in v0. The interface
// method exists so a future SubAgent spec can override it without
// changing the ContextEngine interface.
func (a *DefaultAssembler) PrepareSubagentSpawn(ctx context.Context, p SubagentSpawnParams) (*SubagentSpawnPreparation, error) {
	return nil, ErrSubAgentUnsupported
}

// OnSubagentEnded returns ErrSubAgentUnsupported in v0.
func (a *DefaultAssembler) OnSubagentEnded(ctx context.Context, p SubagentEndedParams) error {
	return ErrSubAgentUnsupported
}
