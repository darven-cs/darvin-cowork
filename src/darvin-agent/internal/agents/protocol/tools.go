package protocol

import (
	"context"
	"encoding/json"
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
//
// Parameters returns raw JSON Schema bytes so every JSON Schema construct
// is preserved through to providers and validators.
type Tool interface {
	Name() string
	Description() string
	Parameters() json.RawMessage
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

// ToolRegistry is the tool surface the agent framework consumes. Concrete
// registries (internal/tools.Registry) implement it; skill mini loops get
// a registry scoped to the skill's allowed tools via ScopedForSkill.
type ToolRegistry interface {
	Get(name string) Tool
	GetEntry(name string) (*Entry, bool)
	Specs() []ToolSpec
	Names() []string
	List() []*Entry
	SetGrantedReads(paths []string)
	ApprovePath(p string)
	EvaluatePermission(toolName string, args map[string]any) PermissionEval
	// ScopedForSkill returns a registry containing only the named tools.
	// An empty allow set yields an empty registry.
	ScopedForSkill(allow []string) ToolRegistry
}

// PermissionEval is the outcome of evaluating whether a tool call needs user
// approval before execution (permission gate).
type PermissionEval struct {
	// Authorized is true when the tool's path arguments stay within the
	// authorized roots (workspace root read+write; attached files read-only).
	Authorized bool
	// Need is true when the executor must request user approval first.
	Need bool
	// Level is one of "safe" | "caution" | "destructive".
	Level string
	// Reason is the human-readable explanation surfaced in the approval modal.
	Reason string
	// EscapedPath is the offending path when Need is a path-escape; the
	// executor grants it one-shot on allow so the tool can actually run.
	EscapedPath string
}
