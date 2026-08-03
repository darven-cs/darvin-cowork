// Package ctxengine is the in-process context assembly / compaction layer.
// It owns the messages-to-LLM boundary: token budget enforcement, tool
// result truncation and LLM-based summarisation when the conversation
// grows past the budget.
//
// The package avoids importing internal/agent (the root) by accepting a
// Deps interface; agent.Agent satisfies it implicitly.
package ctxengine

import (
	"context"
)

// Info describes the ContextEngine implementation. Stable across upgrades
// so subscribers can branch on Name.
type Info struct {
	Name    string
	Version string
}

// ContextEngine is the public 10-method interface. agent.Agent holds one
// instance (default *DefaultAssembler) and exposes it via executor.Deps.
type ContextEngine interface {
	// Identity
	Info() Info

	// Lifecycle
	Bootstrap(ctx context.Context, p BootstrapParams) error
	Maintain(ctx context.Context, p MaintainParams) error
	Dispose(ctx context.Context) error

	// Message handling
	Ingest(ctx context.Context, p IngestParams) IngestResult
	IngestBatch(ctx context.Context, p IngestBatchParams) IngestResult
	AfterTurn(ctx context.Context, p AfterTurnParams) error

	// Assembly + compaction
	Assemble(ctx context.Context, p AssembleParams) AssembleResult
	Compact(ctx context.Context, p CompactParams) CompactResult

	// SubAgent (not implemented; returns ErrSubAgentUnsupported)
	PrepareSubagentSpawn(ctx context.Context, p SubagentSpawnParams) (*SubagentSpawnPreparation, error)
	OnSubagentEnded(ctx context.Context, p SubagentEndedParams) error
}

// Compile-time assertion: *DefaultAssembler satisfies ContextEngine.
var _ ContextEngine = (*DefaultAssembler)(nil)
