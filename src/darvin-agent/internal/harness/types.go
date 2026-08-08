// Package harness abstracts "a backend that can run one agent attempt".
// It declares its own Usage / ImageAttachment / ToolCallRecord shapes
// instead of importing internal/agents so any backend (CLI subprocess,
// remote service) can implement it; the wiring layer owns conversion.
package harness

import (
	"context"
	"errors"
	"strings"
)

// ErrNotImplemented is returned for a capability a harness does not support.
var ErrNotImplemented = errors.New("harness: capability not implemented")

var (
	ErrIDRequired               = errors.New("harness: ID is required")
	ErrHarnessRequired          = errors.New("harness: harness is required")
	ErrSessionIDRequired        = errors.New("harness: SessionID is required")
	ErrPromptRequired           = errors.New("harness: Prompt is required")
	ErrNotRegistered            = errors.New("harness: not registered")
	ErrNoCandidate              = errors.New("harness: no supporting harness")
	ErrContextEngineUnsupported = errors.New("harness: context engine unsupported")
	ErrPluginIDMismatch         = errors.New("harness: PluginID disagrees with owner")
)

// Harness is the surface every backend must implement. Optional behaviour
// lives in the capability interfaces below; a harness opts in by
// implementing the interface and declaring it in Capabilities.
//
// Implementations are process-level singletons; RunAttempt is stateless.
type Harness interface {
	// ID is the registry key; Label is the human-readable name;
	// PluginID names the registering plugin ("" for built-ins).
	ID() string
	Label() string
	PluginID() string

	// Capabilities declares what this harness can do (read without
	// instantiating a session). Supports scores it for one context.
	Capabilities() Capabilities
	Supports(SupportContext) SupportResult

	// RunAttempt drives one prompt to completion; callers use
	// RunAttemptWithLifecycle rather than calling this directly.
	RunAttempt(ctx context.Context, params RunAttemptParams) (*AttemptResult, error)

	// Reset drops per-session state; Dispose releases process resources.
	Reset(ctx context.Context, params ResetParams) error
	Dispose(ctx context.Context) error
}

// Optional capability interfaces; a harness opts in by implementing the
// interface and declaring it in Capabilities.
type (
	// Compactor shrinks a session's context.
	Compactor interface {
		Compact(ctx context.Context, params CompactParams) (*CompactResult, error)
	}
	// Classifier labels a finished attempt (drift / stall detection).
	Classifier interface {
		Classify(ctx context.Context, result *AttemptResult, params *RunAttemptParams) Classification
	}
	// SideQuestioner answers a one-off question outside the turn loop.
	SideQuestioner interface {
		RunSideQuestion(ctx context.Context, params SideQuestionParams) (*SideQuestionResult, error)
	}
	// SessionForker branches a session into a new one.
	SessionForker interface {
		SessionFork(ctx context.Context, params SessionForkParams) (*SessionForkResult, error)
	}
	// TurnFinalizer runs after a turn settles.
	TurnFinalizer interface {
		FinalizeSettledTurn(ctx context.Context, params SettledTurnParams) (*SettledTurnResult, error)
	}
	// UsageReporter reports provider-side quota.
	UsageReporter interface {
		UsageSnapshot(ctx context.Context, params UsageSnapshotParams) (*UsageSnapshot, error)
	}
	// AutoSelector narrows a harness to a static provider allowlist.
	AutoSelector interface {
		AutoSelection() *AutoSelectionHint
	}
)

// ContextEngineHostCapability names one host-side facility a context
// engine may require.
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

// LegacyContextEngineID exempts requirements from host capability checks.
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

// DeliveryDefaults is a harness's reply-delivery preference.
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

	// ContextEngineHost lists the host facilities this harness provides;
	// an unadvertised host fails closed (superset test).
	ContextEngineHost []ContextEngineHostCapability
	// DelegatedExecution lists plugin ids allowed to delegate here.
	DelegatedExecution []string
	// DeliveryDefaults is the reply-delivery preference; nil = undeclared.
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
// not advertise. A nil req / empty required set / legacy engine all return
// nothing; otherwise it is a superset test.
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

// ProviderOwnership records which plugins claim ownership of a provider
// (Status: "unowned" | "owned" | "ambiguous").
type ProviderOwnership struct {
	Status    string
	PluginIDs []string
}

// SupportContext is what selection knows about a session before picking a
// harness for it.
type SupportContext struct {
	SessionID  string
	SessionKey string

	Provider string
	Model    string

	// RequestedRuntime is the harness id the caller's config asked for;
	// empty means auto. Non-empty applies a priority boost after hard filters.
	RequestedRuntime string
	// ProviderOwnership is the requested provider's ownership record.
	ProviderOwnership *ProviderOwnership

	// ContextEngine is what the caller's context engine requires; nil = none.
	ContextEngine *ContextEngineRequirement
	// PluginID is set when the call is delegated from a plugin.
	PluginID string
	// RequestedHarnessID pins a harness explicitly, bypassing scoring.
	RequestedHarnessID string
}

// SupportResult is one harness's answer to a SupportContext. Higher
// Priority wins; ties break on harness id so ordering is deterministic.
type SupportResult struct {
	Supported bool
	Priority  int
	// Reason explains a refusal; surfaced in diagnostics only.
	Reason string
}

// AutoSelectionHint is a harness's static provider allowlist; it only
// filters (no priority contribution). A non-nil empty Providers slice
// marks the harness explicit-only.
type AutoSelectionHint struct {
	Providers []string
}

// Eligible reports whether the hint admits provider; nil hint / nil
// Providers defers to Supports, an empty slice marks explicit-only.
func (h *AutoSelectionHint) Eligible(provider string) bool {
	if h == nil || h.Providers == nil {
		return true
	}
	if len(h.Providers) == 0 {
		return false
	}
	return containsProvider(h.Providers, provider)
}

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

func hasHostCapability(list []ContextEngineHostCapability, want ContextEngineHostCapability) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// normalizeProviderID lowercases and trims a provider id so an allowlist
// entry and a request that differ only in case / padding still match.
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
