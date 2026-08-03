package ctxengine

import (
	"context"
	"time"

	"darvin-cowork/backend/internal/agents/llm"
)

// BootstrapParams is the input for Bootstrap.
type BootstrapParams struct {
	SessionID string
	Workdir   string
}

// MaintainParams is the input for Maintain.
type MaintainParams struct {
	SessionID string
}

// IngestParams is the input for Ingest.
type IngestParams struct {
	SessionID string
	Message   llm.Message
}

// IngestBatchParams is the input for IngestBatch.
type IngestBatchParams struct {
	SessionID string
	Messages  []llm.Message
}

// IngestResult is the output of Ingest / IngestBatch.
type IngestResult struct {
	Success         bool
	TokensProcessed int
	Warnings        []string
}

// AfterTurnParams is the input for AfterTurn.
type AfterTurnParams struct {
	SessionID string
	TurnIndex int
}

// AssembleParams is the input for Assemble.
type AssembleParams struct {
	SessionID       string
	Messages        []llm.Message
	SystemSections  []SystemSection
	ToolBudget      int
	AvailableTools  []string
	AvailableSkills []SkillSummary
	AvailableFacts  []Fact
	MCPServers      []MCPServerInfo

	// LastUsage is the API-reported Usage from the previous turn. When
	// LastUsage.PromptTokens > 0, Assemble uses it as the authoritative
	// token estimate (more accurate than the rune/4 character estimator);
	// a zero value triggers the local fallback estimator. Callers usually
	// populate this from executor's per-turn accounting.
	LastUsage llm.Usage
}

// AssembleResult is the output of Assemble.
type AssembleResult struct {
	Messages        []llm.Message
	EstimatedTokens int
	SystemAddition  string
	Budget          int
	Stats           AssembleStats
}

// AssembleStats is the diagnostic breakdown of what Assemble did.
type AssembleStats struct {
	TruncatedTools      int
	TruncatedBytes      int64
	CompactionTriggered bool
}

// CompactParams is the input for Compact.
type CompactParams struct {
	SessionID  string
	Messages   []llm.Message
	Budget     int
	Force      bool
	Reason     string
	Checkpoint *CheckPoint
	// LastUsage is the API-reported Usage from the previous turn. When
	// LastUsage.PromptTokens > 0, Compact uses it as the authoritative
	// tokensBefore (rather than the local rune/4 estimator) so the budget
	// check matches what Assemble computed from the same field.
	LastUsage llm.Usage
}

// CompactResult is the output of Compact.
type CompactResult struct {
	Success          bool
	TokensBefore     int
	TokensAfter      int
	RetainedMessages []llm.Message
	Summary          string
	Checkpoint       *CheckPoint
}

// CheckPoint is a snapshot of the messages at the entry of Compact; the
// caller can use it to roll back if Compact fails or is aborted.
type CheckPoint struct {
	ID         string
	CapturedAt time.Time
	Snapshot   []llm.Message
}

// SubagentSpawnParams is the input for PrepareSubagentSpawn.
type SubagentSpawnParams struct {
	ParentSessionID string
	SubagentName    string
	Instructions    string
}

// SubagentSpawnPreparation is the output of PrepareSubagentSpawn.
type SubagentSpawnPreparation struct {
	SubagentSessionID string
	InitialMessages   []llm.Message
}

// SubagentEndedParams is the input for OnSubagentEnded.
type SubagentEndedParams struct {
	ParentSessionID   string
	SubagentSessionID string
	FinalMessages     []llm.Message
	StopReason        llm.FinishReason
}

// Summarizer produces a textual summary of a span of conversation messages.
// DefaultSummarizer (in compact.go) uses llm.ModelProvider.Complete.
type Summarizer interface {
	Summarize(ctx context.Context, req SummarizeRequest) (string, error)
}

// SummarizeRequest is the input for Summarizer.Summarize.
type SummarizeRequest struct {
	Model     string
	Messages  []llm.Message
	Hint      string
	MaxTokens int
}
