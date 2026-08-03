// Package tool defines the Tool interface and Registry, plus the
// 5 built-in tools (read_file / write_file / edit_file / list_dir / shell)
// gated by a workspace sandbox.
package tool

import (
	"context"

	"darvin-cowork/backend/internal/llm"
)

// Result is what a tool returns to the agent loop. IsError=true means the
// tool refused / failed; the LLM will see the Content as a tool message
// and decide what to do next.
type Result struct {
	Content  string
	IsError  bool
	Metadata map[string]any
}

// Tool is the contract every tool implements. Execute must respect ctx —
// if the caller's context is cancelled, Execute should return promptly
// with a Result whose Content indicates cancellation.
type Tool interface {
	Name() string
	Description() string
	Parameters() llm.ParameterSchema
	Execute(ctx context.Context, args map[string]any) Result
}

// Kind classifies where a tool comes from. The registry tags every entry
// with one of these so the executor can attribute tool_start / tool_end
// events and the renderer can route ToolCallGroup rendering.
type Kind string

const (
	KindBuiltIn Kind = "builtin"
	KindSkill   Kind = "skill"
	KindMcp     Kind = "mcp"
)

// Entry is a registered tool plus its classification. Metadata carries
// kind-specific identifiers: skillID for KindSkill, mcpServerID +
// mcpToolName for KindMcp. PluginID names the owning plugin when the entry
// was registered by a plugin (nil for built-ins).
type Entry struct {
	Tool     Tool
	Kind     Kind
	Metadata map[string]any
	PluginID string
}

// Plugin contributes tools to a Registry. Register must pair with
// Unregister so a changed source can be re-applied with a full sweep.
type Plugin interface {
	PluginID() string
	Register(reg ToolRegistrar) error
	Unregister(reg ToolRegistrar) error
}

// ToolRegistrar is the subset of Registry a plugin uses to add and remove
// its tools. Kept separate so plugins depend on a minimal surface.
type ToolRegistrar interface {
	RegisterTool(t Tool, kind Kind, meta map[string]any) error
	UnregisterTool(name string) error
	UnregisterByPlugin(pluginID string) error
}
