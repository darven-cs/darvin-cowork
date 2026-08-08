package protocol

import (
	"context"
	"encoding/json"
)

// Result is what a tool returns to the agent loop. IsError=true means
// the tool refused / failed; the LLM sees Content as a tool message.
type Result struct {
	Content  string
	IsError  bool
	Metadata map[string]any
}

// Tool is the contract every tool implements. Parameters returns raw
// JSON Schema so every construct survives through to providers.
type Tool interface {
	Name() string
	Description() string
	Parameters() json.RawMessage
	// Execute respects ctx — returns promptly on cancellation.
	Execute(ctx context.Context, args map[string]any) Result
}

// Kind classifies where a tool comes from.
type Kind string

const (
	KindBuiltIn Kind = "builtin"
	KindSkill   Kind = "skill"
	KindMcp     Kind = "mcp"
)

// Entry is a registered tool plus its classification.
type Entry struct {
	Tool     Tool
	Kind     Kind
	Metadata map[string]any
	PluginID string
}

// Plugin contributes tools to a Registry. Register pairs with
// Unregister so a changed source can be re-applied with a full sweep.
type Plugin interface {
	PluginID() string
	Register(reg ToolRegistrar) error
	Unregister(reg ToolRegistrar) error
}

// ToolRegistrar is the subset of Registry a plugin uses.
type ToolRegistrar interface {
	RegisterTool(t Tool, kind Kind, meta map[string]any) error
	UnregisterTool(name string) error
	UnregisterByPlugin(pluginID string) error
}

// ToolRegistry is the tool surface the agent framework consumes.
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
	ScopedForSkill(allow []string) ToolRegistry
}

// PermissionEval is the outcome of evaluating whether a tool call needs
// user approval before execution.
type PermissionEval struct {
	Authorized bool
	Need       bool
	Level      string
	Reason     string
	EscapedPath string
}