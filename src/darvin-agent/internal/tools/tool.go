// Package tool defines the Tool interface and Registry, plus the
// 5 built-in tools (read_file / write_file / edit_file / list_dir / shell)
// gated by a workspace sandbox.
//
// The Tool contract and its associated types now live in
// internal/agents/protocol; this package re-exports them so existing
// consumers keep compiling unchanged, and provides the concrete Registry
// and built-ins that implement them.
package tool

import "darvin-cowork/backend/internal/agents/protocol"

// Result is what a tool returns to the agent loop. IsError=true means the
// tool refused / failed; the LLM will see the Content as a tool message
// and decide what to do next.
type Result = protocol.Result

// Tool is the contract every tool implements. Execute must respect ctx —
// if the caller's context is cancelled, Execute should return promptly
// with a Result whose Content indicates cancellation.
type Tool = protocol.Tool

// Kind classifies where a tool comes from. The registry tags every entry
// with one of these so the executor can attribute tool_start / tool_end
// events and the renderer can route ToolCallGroup rendering.
type Kind = protocol.Kind

const (
	KindBuiltIn Kind = protocol.KindBuiltIn
	KindSkill   Kind = protocol.KindSkill
	KindMcp     Kind = protocol.KindMcp
)

// Entry is a registered tool plus its classification. Metadata carries
// kind-specific identifiers: skillID for KindSkill, mcpServerID +
// mcpToolName for KindMcp. PluginID names the owning plugin when the entry
// was registered by a plugin (nil for built-ins).
type Entry = protocol.Entry

// Plugin contributes tools to a Registry. Register must pair with
// Unregister so a changed source can be re-applied with a full sweep.
type Plugin = protocol.Plugin

// ToolRegistrar is the subset of Registry a plugin uses to add and remove
// its tools. Kept separate so plugins depend on a minimal surface.
type ToolRegistrar = protocol.ToolRegistrar

// ToolRegistry is the tool surface the agent framework consumes; the
// concrete *Registry satisfies it.
type ToolRegistry = protocol.ToolRegistry
