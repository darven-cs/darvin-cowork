// Package protocol defines the shared contract between the agent framework
// (internal/agents) and the capability packages (internal/llm, internal/tools,
// internal/mcp, internal/skills).
//
// It holds the provider-agnostic LLM type model, the streaming event shapes,
// the ModelProvider / Tool / ToolRegistry interfaces and the model metadata
// registry. The framework imports this package only; capability packages
// implement the interfaces and may also import it. It must stay stdlib-only
// so neither side creates a dependency cycle.
package protocol
