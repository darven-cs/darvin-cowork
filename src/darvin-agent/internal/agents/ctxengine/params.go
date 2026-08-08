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

	// LastUsage is the API-reported Usage from the previous turn. When
	// LastUsage.PromptTokens > 0, Assemble uses it as the authoritative
	// token estimate (more accurate than the rune/4 character estimator);
	// a zero value triggers the local fallback estimator. Callers usually
	// populate this from executor's per-turn accounting.
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
	// auto-compaction outcome through Assemble so the executor can
	// (a) ReplaceAll the session messages with the compacted slice and
	// (b) hand the boundary to Agent.PersistCompaction for the
	// session_digests write. Only populated when
	// Stats.CompactionTriggered is true.
	CompactSummary      string
	FirstKeptID         string
	FirstKeptTimestamp  int64
}

// AssembleStats is the diagnostic breakdown of what Assemble did.
//
// SoftNoticeEmitted / SnipTriggered / PausedReCompactLoop extend the
// pre-existing TruncatedTools / CompactionTriggered fields with the
// three non-LLM stages of the FR-2 four-tier cascade. Renderers
// typically only consume CompactionTriggered; the others power
// telemetry / debug panels.
type AssembleStats struct {
	TruncatedTools      int
	TruncatedBytes      int64
	CompactionTriggered bool

	// SoftNoticeEmitted is true when the 50% soft-compact notice fired
	// this turn (at most once per window climb).
	SoftNoticeEmitted bool
	// SnipTriggered is true when the 60% stale-tool-result snip ran.
	SnipTriggered bool
	// PausedReCompactLoop is true when the FR-4 stuck latch suppressed
	// auto-compact on this turn.
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
	// LastUsage is the API-reported Usage from the previous turn. When
	// LastUsage.PromptTokens > 0, Compact uses it as the authoritative
	// tokensBefore (rather than the local rune/4 estimator) so the budget
	// check matches what Assemble computed from the same field.
	LastUsage protocol.Usage
}

// CompactResult is the output of Compact.
type CompactResult struct {
	Success          bool
	TokensBefore     int
	TokensAfter      int
	RetainedMessages []protocol.Message
	Summary          string
	// FirstKeptID / FirstKeptTimestamp identify the first message
	// preserved verbatim after Compact. Used by Agent.PersistCompaction
	// to record the digest boundary (FirstKeptID == Message.ID, with
	// FirstKeptTimestamp as fallback when older rows lack ID).
	FirstKeptID        string
	FirstKeptTimestamp int64
	// Reason is the human-readable trigger: "budget_exceeded" | "manual"
	// | "steer_triggered". Persisted as SessionDigest.CompactReason.
	Reason   string
	Checkpoint *CheckPoint
}

// CheckPoint is a snapshot of the messages at the entry of Compact; the
// caller can use it to roll back if Compact fails or is aborted.
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

// Summarizer produces a textual summary of a span of conversation messages.
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
