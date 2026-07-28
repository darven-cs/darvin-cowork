// Package tool defines the Tool interface and Registry, plus the
// 5 built-in tools (read_file / write_file / edit_file / list_dir / shell)
// gated by a workspace sandbox.
package tool

import (
	"context"

	"darvin-cowork/backend/internal/agent/llm"
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
