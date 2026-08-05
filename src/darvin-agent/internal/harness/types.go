// Package harness abstracts "a backend that can run one agent attempt" so
// callers (gateway, schedulers, other harnesses) stay independent of the
// runtime that actually drives the model loop.
//
// The package deliberately declares its own Usage / ImageAttachment /
// ToolCallRecord shapes instead of importing internal/agents: a harness must
// be implementable by a backend that has nothing to do with the embedded
// runtime (a CLI subprocess, a remote service), and the wiring layer owns the
// conversion.
package harness

import (
	"context"
	"errors"
	"strings"
)

// ErrNotImplemented is returned by a harness for a capability it declares no
// support for.
var ErrNotImplemented = errors.New("harness: capability not implemented")

var (
	// ErrIDRequired is returned by Register for a harness with a blank ID.
	ErrIDRequired = errors.New("harness: ID is required")
	// ErrHarnessRequired is returned when a nil Harness reaches an entry point.
	ErrHarnessRequired = errors.New("harness: harness is required")
	// ErrSessionIDRequired is returned by RunAttemptWithLifecycle when the
	// attempt carries no session to correlate its events with.
	ErrSessionIDRequired = errors.New("harness: SessionID is required")
	// ErrPromptRequired is returned by RunAttemptWithLifecycle for an empty prompt.
	ErrPromptRequired = errors.New("harness: Prompt is required")
	// ErrNotRegistered is returned when a pinned harness id has no registration.
	ErrNotRegistered = errors.New("harness: not registered")
	// ErrNoCandidate is returned when no registered harness supports the context.
	ErrNoCandidate = errors.New("harness: no supporting harness")
	// ErrContextEngineUnsupported is returned when a harness does not advertise
	// every host capability the caller's context engine requires.
	ErrContextEngineUnsupported = errors.New("harness: context engine unsupported")
	// ErrPluginIDMismatch is returned by Register when a harness reports a
	// plugin id that disagrees with the registering owner.
	ErrPluginIDMismatch = errors.New("harness: PluginID disagrees with owner")
)

// Harness is the surface every backend must implement. Optional behaviour
// lives in the capability interfaces below; a harness opts in by implementing
// the interface *and* declaring it in Capabilities.
//
// Implementations are process-level singletons. RunAttempt itself is
// stateless: everything it needs arrives through RunAttemptParams.
type Harness interface {
	// ID is the registry key, e.g. "embedded".
	ID() string
	// Label is the human-readable name shown in diagnostics.
	Label() string
	// PluginID names the plugin that registered this harness; "" for built-ins.
	PluginID() string

	// Capabilities declares what this harness can do. Selection reads it
	// without instantiating a session.
	Capabilities() Capabilities
	// Supports scores this harness for one selection context.
	Supports(SupportContext) SupportResult

	// RunAttempt drives one prompt to completion. Callers go through
	// RunAttemptWithLifecycle rather than calling this directly.
	RunAttempt(ctx context.Context, params RunAttemptParams) (*AttemptResult, error)

	// Reset drops any per-session state the harness holds.
	Reset(ctx context.Context, params ResetParams) error
	// Dispose releases process-level resources.
	Dispose(ctx context.Context) error
}

// Compactor shrinks a session's context. Declared as Capabilities.Compact.
type Compactor interface {
	Compact(ctx context.Context, params CompactParams) (*CompactResult, error)
}

// Classifier labels a finished attempt so callers can react to drift or a
// stalled loop. Declared as Capabilities.Classify.
type Classifier interface {
	Classify(ctx context.Context, result *AttemptResult, params *RunAttemptParams) Classification
}

// SideQuestioner answers a one-off question without running a full turn loop.
// Declared as Capabilities.SideQuestion.
type SideQuestioner interface {
	RunSideQuestion(ctx context.Context, params SideQuestionParams) (*SideQuestionResult, error)
}

// SessionForker branches a session into a new one. Declared as
// Capabilities.SessionFork.
type SessionForker interface {
	SessionFork(ctx context.Context, params SessionForkParams) (*SessionForkResult, error)
}

// TurnFinalizer runs after a turn settles, e.g. to flush trailing state.
// Declared as Capabilities.FinalizeSettledTurn.
type TurnFinalizer interface {
	FinalizeSettledTurn(ctx context.Context, params SettledTurnParams) (*SettledTurnResult, error)
}

// UsageReporter reports provider-side quota. Declared as
// Capabilities.UsageSnapshot.
type UsageReporter interface {
	UsageSnapshot(ctx context.Context, params UsageSnapshotParams) (*UsageSnapshot, error)
}

// AutoSelector narrows a harness to a static provider allowlist, letting
// selection skip it without calling Supports.
type AutoSelector interface {
	AutoSelection() *AutoSelectionHint
}

// ContextEngineHostCapability names one host-side facility a context engine
// may require. A harness advertises the set it provides; a context engine
// declares, per operation, the subset it needs.
type ContextEngineHostCapability string

const (
	HostBootstrap            ContextEngineHostCapability = "bootstrap"
	HostAssembleBeforePrompt ContextEngineHostCapability = "assemble-before-prompt"
	HostAfterTurn            ContextEngineHostCapability = "after-turn"
	HostMaintain             ContextEngineHostCapability = "maintain"
	HostCompact              ContextEngineHostCapability = "compact"
	HostRuntimeLLMComplete   ContextEngineHostCapability = "runtime-llm-complete"
)

// ContextEngineOperation is the operation whose host requirements are checked.
type ContextEngineOperation string

const (
	OpAgentRun ContextEngineOperation = "agent-run"
	OpCompact  ContextEngineOperation = "compact"
)

// LegacyContextEngineID names the pre-assembler path. A requirement carrying
// it is exempt from host capability checks, so enabling the real engine is
// what turns the gate on rather than this package's rollout.
const LegacyContextEngineID = "legacy"

// ContextEngineRequirement is what the caller's context engine needs from a
// harness for one operation.
type ContextEngineRequirement struct {
	EngineID  string
	Operation ContextEngineOperation
	// RequiredCapabilities must all be advertised by the harness.
	RequiredCapabilities []ContextEngineHostCapability
	// UnsupportedMessage overrides the generated rejection text.
	UnsupportedMessage string
}

// VisibleReplies is how a harness expects visible replies to be produced.
type VisibleReplies string

const (
	VisibleRepliesAutomatic   VisibleReplies = "automatic"
	VisibleRepliesMessageTool VisibleReplies = "message_tool"
)

// DeliveryDefaults is a harness's reply-delivery preference, consulted when
// configuration does not override it.
type DeliveryDefaults struct {
	VisibleReplies VisibleReplies
}

// Capability names one optional interface for declaration and verification.
type Capability string

const (
	CapCompact             Capability = "compact"
	CapClassify            Capability = "classify"
	CapSideQuestion        Capability = "sideQuestion"
	CapSessionFork         Capability = "sessionFork"
	CapFinalizeSettledTurn Capability = "finalizeSettledTurn"
	CapUsageSnapshot       Capability = "usageSnapshot"
)

// Capabilities is a harness's declaration of what it serves. The boolean
// fields must agree with the capability interfaces the type implements;
// VerifyCapabilities checks that.
type Capabilities struct {
	// Healthy gates the harness out of auto-selection when false.
	Healthy bool

	Compact             bool
	Classify            bool
	SideQuestion        bool
	SessionFork         bool
	FinalizeSettledTurn bool
	UsageSnapshot       bool

	// ContextEngineHost lists the host facilities this harness provides. A
	// harness advertising none cannot run a context engine that declares
	// requirements for the operation: the check is a superset test, so an
	// unadvertised host fails closed.
	ContextEngineHost []ContextEngineHostCapability
	// DelegatedExecution lists the plugin ids allowed to delegate to this
	// harness. Empty means no delegation.
	DelegatedExecution []string
	// DeliveryDefaults is the harness's reply-delivery preference. Nil means
	// undeclared, leaving the caller on its own default.
	DeliveryDefaults *DeliveryDefaults
}

// Declares reports whether c claims the given capability.
func (c Capabilities) Declares(cap Capability) bool {
	switch cap {
	case CapCompact:
		return c.Compact
	case CapClassify:
		return c.Classify
	case CapSideQuestion:
		return c.SideQuestion
	case CapSessionFork:
		return c.SessionFork
	case CapFinalizeSettledTurn:
		return c.FinalizeSettledTurn
	case CapUsageSnapshot:
		return c.UsageSnapshot
	default:
		return false
	}
}

// MissingHostCapabilities returns the capabilities req demands that c does
// not advertise. An empty return means the harness can host req.
//
// A nil req, a req with no required capabilities, and the legacy engine all
// return nothing. Everything else is a plain superset test, so a harness that
// advertises no host capabilities fails every non-trivial requirement.
func (c Capabilities) MissingHostCapabilities(req *ContextEngineRequirement) []ContextEngineHostCapability {
	if req == nil || len(req.RequiredCapabilities) == 0 || req.EngineID == LegacyContextEngineID {
		return nil
	}
	var missing []ContextEngineHostCapability
	for _, want := range req.RequiredCapabilities {
		if !hasHostCapability(c.ContextEngineHost, want) {
			missing = append(missing, want)
		}
	}
	return missing
}

// AllowsDelegation reports whether pluginID may delegate execution here.
func (c Capabilities) AllowsDelegation(pluginID string) bool {
	if pluginID == "" {
		return true
	}
	return contains(c.DelegatedExecution, pluginID)
}

// ProviderOwnership records which plugins (if any) claim ownership of a
// provider. Selection uses it to score harness affinity for owned providers.
type ProviderOwnership struct {
	// Status is one of "unowned", "owned", "ambiguous".
	Status string
	// PluginIDs names the owning plugin(s). Empty for "unowned".
	PluginIDs []string
}

// SupportContext is what selection knows about a session before picking a
// harness for it.
type SupportContext struct {
	SessionID  string
	SessionKey string

	Provider string
	Model    string

	// RequestedRuntime is the harness id the caller's config asked for.
	// Empty means auto. When non-empty, selection applies a priority boost
	// to a harness whose ID matches, after hard filters.
	RequestedRuntime string
	// ProviderOwnership is the ownership record for the requested provider.
	// Nil is treated as "unowned".
	ProviderOwnership *ProviderOwnership

	// ContextEngine is what the caller's context engine requires of the
	// harness. Nil means no requirement.
	ContextEngine *ContextEngineRequirement
	// PluginID is set when the call is delegated from a plugin.
	PluginID string
	// RequestedHarnessID pins a harness explicitly, bypassing scoring.
	RequestedHarnessID string
}

// SupportResult is one harness's answer to a SupportContext. Higher Priority
// wins; ties break on harness id so ordering never depends on map iteration.
//
// Supports is the single source of priority: nothing else contributes to the
// ranking score.
type SupportResult struct {
	Supported bool
	Priority  int
	// Reason explains a refusal; it is surfaced in diagnostics only.
	Reason string
}

// AutoSelectionHint is a harness's static provider allowlist. It only ever
// filters; it carries no priority, so a harness cannot have its score counted
// twice.
type AutoSelectionHint struct {
	// Providers restricts the harness to these provider ids. A nil slice
	// leaves eligibility to Supports; a non-nil empty slice marks the harness
	// explicit-only.
	Providers []string
}

// Eligible reports whether the hint admits provider for auto selection.
//
// A nil hint, or a hint with a nil Providers slice, leaves the decision to
// Supports. A non-nil but empty Providers slice marks the harness
// explicit-only: it is never auto-selected and must be named through
// SupportContext.RequestedHarnessID.
func (h *AutoSelectionHint) Eligible(provider string) bool {
	if h == nil || h.Providers == nil {
		return true
	}
	if len(h.Providers) == 0 {
		return false
	}
	return containsProvider(h.Providers, provider)
}

// ImageAttachment is a base64 image staged for one prompt, carrying the
// `data:<mime>;base64,<payload>` URL the renderer produced.
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

// RunProgress is a progress tick emitted through RunAttemptParams.OnRunProgress.
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

	// TimeoutMs caps the whole attempt. Zero means no timeout.
	TimeoutMs int

	// ContextEngine is what the caller's context engine requires of the
	// harness. RunAttemptWithLifecycle asserts it on every attempt, including
	// the pinned path that never went through Rank. Nil means no requirement.
	ContextEngine *ContextEngineRequirement

	// LifecycleGen is stamped by RunAttemptWithLifecycle; a harness echoes it
	// back so an attempt raced by a session reset can be detected.
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

	// HarnessID attributes the result to the harness that produced it.
	// RunAttemptWithLifecycle stamps it on every path, including panics.
	HarnessID string

	// LifecycleGen echoes the generation the attempt started under.
	LifecycleGen uint64
	// Superseded is set when the session was reset while the attempt ran, so
	// the caller can drop a result that no longer describes live state.
	// Concurrent attempts do not supersede each other: only Reset advances the
	// generation.
	Superseded bool
	DurationMs int64
}

// ResetParams identifies the session state to drop.
type ResetParams struct {
	SessionID string
	Reason    string
}

// CompactParams asks a harness to shrink a session's context.
type CompactParams struct {
	SessionID string
	// TargetTokens is the budget to compact down to. Zero lets the harness
	// pick its configured budget.
	TargetTokens int
}

// CompactResult reports what compaction achieved.
type CompactResult struct {
	NewTokens       int
	RemovedMessages int
	TookMs          int64
}

// SideQuestionParams is a one-off question answered outside the turn loop.
type SideQuestionParams struct {
	SessionID string
	Question  string
	Provider  string
	Model     string
}

// SideQuestionResult carries the answer text.
type SideQuestionResult struct {
	Text  string
	Usage Usage
}

// SessionForkParams branches Source into TargetKey.
type SessionForkParams struct {
	Source    string
	TargetKey string
	Upstream  string
}

// SessionForkResult reports the forked session.
type SessionForkResult struct {
	SessionID string
	Created   bool
}

// SettledTurnParams is handed to a harness after a turn settles.
type SettledTurnParams struct {
	SessionID string
	RunID     string
	MessageID string
	Result    *AttemptResult
}

// SettledTurnResult is the single visible answer produced from a settled tool
// transcript. Producing it is the whole point of the capability, so the
// message and its accounting travel with the result rather than a bare flag.
type SettledTurnResult struct {
	AssistantText string
	Usage         Usage
	// TranscriptOwned marks that the harness already persisted the assistant
	// message, so the caller must not write it again.
	TranscriptOwned bool
	// IdempotencyKey is the key of the harness-owned transcript row.
	IdempotencyKey string
	// MessageIndex correlates the final reply with the assistant stream.
	MessageIndex int
}

// UsageSnapshotParams identifies the provider account to query.
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

func hasHostCapability(list []ContextEngineHostCapability, want ContextEngineHostCapability) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// normalizeProviderID lowercases and trims a provider id so an allowlist entry
// and a request that differ only in case or padding still match.
func normalizeProviderID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

func containsProvider(list []string, want string) bool {
	want = normalizeProviderID(want)
	for _, v := range list {
		if normalizeProviderID(v) == want {
			return true
		}
	}
	return false
}
