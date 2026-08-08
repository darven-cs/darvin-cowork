// Input / output shapes for one run attempt and the optional harness
// operations that wrap it (compact / classify / fork / settle).

package harness

// ImageAttachment is a base64 image staged for one prompt.
type ImageAttachment struct {
	Path    string
	Name    string
	Size    int64
	DataURL string
}

// Usage is the token accounting an attempt reports back.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int

	CacheReadTokens  int
	CacheWriteTokens int
}

// ToolCallRecord summarises one tool invocation an attempt made.
type ToolCallRecord struct {
	ID      string
	Name    string
	IsError bool
}

// ExecutionPhase is the coarse stage an in-flight attempt reports.
type ExecutionPhase string

const (
	PhaseQueued   ExecutionPhase = "queued"
	PhaseStarting ExecutionPhase = "starting"
	PhaseModel    ExecutionPhase = "model"
	PhaseTools    ExecutionPhase = "tools"
	PhaseSettling ExecutionPhase = "settling"
)

// RunProgress is a progress tick via RunAttemptParams.OnRunProgress.
type RunProgress struct {
	Turn   int
	Phase  ExecutionPhase
	Detail string
}

// RunAttemptParams is the input to one attempt. An attempt covers a whole
// turn loop: the prompt, every tool round trip it triggers, and the final
// assistant message.
type RunAttemptParams struct {
	SessionID  string
	SessionKey string

	Prompt      string
	Images      []ImageAttachment
	Attachments []string

	Provider string
	Model    string

	WorkspaceDir string
	Cwd          string

	// RunID / MessageID / UserMessageID correlate the attempt's events. Blank
	// values are minted by RunAttemptWithLifecycle.
	RunID         string
	MessageID     string
	UserMessageID string

	// TimeoutMs caps the whole attempt; zero means no timeout.
	TimeoutMs int

	// ContextEngine is the caller's requirement, asserted by
	// RunAttemptWithLifecycle on every attempt. Nil = none.
	ContextEngine *ContextEngineRequirement

	// LifecycleGen is stamped by RunAttemptWithLifecycle so an attempt
	// raced by a session reset can be detected.
	LifecycleGen uint64

	OnExecutionStarted func()
	OnExecutionPhase   func(ExecutionPhase)
	OnRunProgress      func(RunProgress)
	OnAttemptTimeout   func()
	OnAttemptAbort     func()
}

// AttemptStatus is the terminal state of an attempt.
type AttemptStatus string

const (
	AttemptOK      AttemptStatus = "ok"
	AttemptError   AttemptStatus = "error"
	AttemptAborted AttemptStatus = "aborted"
	AttemptTimeout AttemptStatus = "timeout"
)

// Classification labels a finished attempt beyond its status.
type Classification string

const (
	ClassificationOK      Classification = "ok"
	ClassificationDrift   Classification = "drift"
	ClassificationStalled Classification = "stalled"
	ClassificationFailed  Classification = "failed"
)

// AttemptResult is the outcome of one attempt.
type AttemptResult struct {
	Status        AttemptStatus
	StopReason    string
	AssistantText string
	TotalTurns    int
	TotalUsage    Usage
	ToolCalls     []ToolCallRecord

	Classification Classification
	LastError      error

	// HarnessID attributes the result to the producing harness.
	HarnessID string

	// LifecycleGen echoes the generation the attempt started under.
	LifecycleGen uint64
	// Superseded is set when the session was reset while the attempt ran;
	// only Reset advances the generation.
	Superseded bool
	DurationMs int64
}

type ResetParams struct {
	SessionID string
	Reason    string
}

// CompactParams asks a harness to shrink a session's context.
type CompactParams struct {
	SessionID string
	// TargetTokens is the budget to compact down to; zero lets the
	// harness pick its configured budget.
	TargetTokens int
}

type CompactResult struct {
	NewTokens       int
	RemovedMessages int
	TookMs          int64
}

type SideQuestionParams struct {
	SessionID string
	Question  string
	Provider  string
	Model     string
}

type SideQuestionResult struct {
	Text  string
	Usage Usage
}

type SessionForkParams struct {
	Source    string
	TargetKey string
	Upstream  string
}

type SessionForkResult struct {
	SessionID string
	Created   bool
}

type SettledTurnParams struct {
	SessionID string
	RunID     string
	MessageID string
	Result    *AttemptResult
}

// SettledTurnResult is the visible answer produced from a settled tool
// transcript, with its accounting. TranscriptOwned marks that the harness
// already persisted the message (caller must not write it again).
type SettledTurnResult struct {
	AssistantText   string
	Usage           Usage
	TranscriptOwned bool
	IdempotencyKey  string
	MessageIndex    int
}

type UsageSnapshotParams struct {
	Provider string
	Model    string
}

// UsageSnapshot is provider-reported quota state.
type UsageSnapshot struct {
	Provider     string
	WindowUsed   int
	WindowLimit  int
	ResetsAtUnix int64
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
