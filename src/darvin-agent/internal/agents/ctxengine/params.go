package ctxengine

import (
	"context"
	"time"

	"darvin-cowork/backend/internal/agents/protocol"
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
	Message   protocol.Message
}

// IngestBatchParams is the input for IngestBatch.
type IngestBatchParams struct {
	SessionID string
	Messages  []protocol.Message
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
	Messages        []protocol.Message
	SystemSections  []SystemSection
	ToolBudget      int
	AvailableTools  []string
	AvailableSkills []SkillSummary
	AvailableFacts  []Fact
	MCPServers      []MCPServerInfo

	// LastUsage is the API-reported Usage from the previous turn.
	// PromptTokens > 0 → use as the authoritative estimate; zero → local
	// rune/4 estimator.
	LastUsage protocol.Usage
}

// AssembleResult is the output of Assemble.
type AssembleResult struct {
	Messages        []protocol.Message
	EstimatedTokens int
	SystemAddition  string
	Budget          int
	Stats           AssembleStats

	// CompactSummary / FirstKeptID / FirstKeptTimestamp carry the
	// auto-compaction outcome; executor ReplaceAlls the session and
	// persists the digest. Populated only when Stats.CompactionTriggered.
	CompactSummary     string
	FirstKeptID        string
	FirstKeptTimestamp int64
}

// AssembleStats is the diagnostic breakdown of what Assemble did.
// SoftNoticeEmitted / SnipTriggered / PausedReCompactLoop extend the
// pre-existing TruncatedTools / CompactionTriggered with the three
// non-LLM stages of the four-tier cascade.
type AssembleStats struct {
	TruncatedTools      int
	TruncatedBytes      int64
	CompactionTriggered bool
	SoftNoticeEmitted   bool
	SnipTriggered       bool
	PausedReCompactLoop bool
}

// CompactParams is the input for Compact.
type CompactParams struct {
	SessionID  string
	Messages   []protocol.Message
	Budget     int
	Force      bool
	Reason     string
	Checkpoint *CheckPoint
	// LastUsage is the API-reported Usage; PromptTokens > 0 → authoritative.
	LastUsage protocol.Usage
}

// CompactResult is the output of Compact.
type CompactResult struct {
	Success            bool
	TokensBefore       int
	TokensAfter        int
	RetainedMessages   []protocol.Message
	Summary            string
	FirstKeptID        string
	FirstKeptTimestamp int64
	Reason             string
	Checkpoint         *CheckPoint
}

// CheckPoint is a snapshot of the messages at the entry of Compact; the
// caller can use it to roll back on failure.
type CheckPoint struct {
	ID         string
	CapturedAt time.Time
	Snapshot   []protocol.Message
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
	InitialMessages   []protocol.Message
}

// SubagentEndedParams is the input for OnSubagentEnded.
type SubagentEndedParams struct {
	ParentSessionID   string
	SubagentSessionID string
	FinalMessages     []protocol.Message
	StopReason        protocol.FinishReason
}

// Summarizer produces a textual summary of a span of messages.
// DefaultSummarizer (in compact.go) uses protocol.ModelProvider.Complete.
type Summarizer interface {
	Summarize(ctx context.Context, req SummarizeRequest) (string, error)
}

// SummarizeRequest is the input for Summarizer.Summarize.
type SummarizeRequest struct {
	Model     string
	Messages  []protocol.Message
	Hint      string
	MaxTokens int
}
