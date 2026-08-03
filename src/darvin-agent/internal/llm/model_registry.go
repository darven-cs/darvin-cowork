package llm

import "darvin-cowork/backend/internal/agents/protocol"

// ModelRegistry is a process-wide lookup table for ModelDescriptor keyed
// by model ID.
type ModelRegistry = protocol.ModelRegistry

// NewModelRegistry returns an empty registry. Tests use this to build
// isolated instances; production code uses DefaultModelRegistry.
func NewModelRegistry() *ModelRegistry {
	return protocol.NewModelRegistry()
}

// DefaultModelRegistry is the global registry populated by provider init()
// functions. Re-exported from the protocol package, which providers and the
// agent loop now read directly.
var DefaultModelRegistry = protocol.DefaultModelRegistry
