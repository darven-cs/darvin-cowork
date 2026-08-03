// Package agent is the root of the agent runtime. It wires the sub-packages
// (event, queue, session, store, executor, protocol) into a single Agent
// and exposes the public API (Run / Prompt / Steer / FollowUp / Abort /
// Subscribe). Capability implementations (provider, tool registry) are
// injected from outside through the protocol contract.
package agent

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agents/ctxengine"
	"darvin-cowork/backend/internal/agents/event"
	"darvin-cowork/backend/internal/agents/executor"
	"darvin-cowork/backend/internal/agents/protocol"
	"darvin-cowork/backend/internal/agents/queue"
	"darvin-cowork/backend/internal/agents/session"
	"darvin-cowork/backend/internal/agents/store"
)

// permissionTimeout is how long a permission_request waits for the renderer
// before defaulting to deny.
const permissionTimeout = 60 * time.Second

// permissionRule is a "remember this session" auto-allow entry: a request
// that matches (toolName, dangerLevel, reason) skips the modal.
type permissionRule struct {
	toolName string
	level    string
	reason   string
}

// pendingPermission is one in-flight permission_request awaiting the
// renderer's answer. timeout fires the default-deny after 60s.
type pendingPermission struct {
	ch      chan executor.PermissionResult
	timeout *time.Timer
}

// ModelRef identifies a model on a specific provider. The provider name is
// matched against registered ModelProvider implementations.
type ModelRef struct {
	Provider string
	Model    string
}

// Config is the runtime configuration for an Agent.
//
// The fields below MaxTurns/EventBuffer are agent-runtime knobs; the
// ContextEngine block (TokenBudget … SystemPromptAddition) is forwarded
// to the auto-constructed DefaultAssembler at New() time. AssemblerEnabled
// is the post-construction flag: zero means disabled (executor takes the
// legacy d.Session().Messages() fallback); true means the executor
// dispatches prompt construction through the assembler.
//
// Note on the cfg.yaml front-end: that layer maps the YAML key
// `assembler_enabled: true` to AssemblerEnabled: true in the Go struct,
// and `assembler_enabled: false` (or omitted) to false. In Go code,
// callers who want the assembler must set AssemblerEnabled: true
// explicitly — Go's bool zero value maps to "disabled", not "default".
type Config struct {
	MaxTurns       int
	ToolTimeout    time.Duration
	Workdir        string
	ShellAllowlist []string
	EventBuffer    int

	// ContextEngine knobs (mirrors ctxengine.Config subset — spec §FR-12).
	// Populated from the YAML front-end (cfg.yaml `agent:` section) and
	// forwarded to ctxengine.NewDefaultAssembler at construction.
	TokenBudget          int
	CompactTailKeep      int
	ToolResultMaxBytes   int
	CompactMaxRetries    int
	SummarizeMaxTokens   int
	SystemPromptAddition string
	AssemblerEnabled     bool
}

// NewAgentConfig is the constructor input.
type NewAgentConfig struct {
	Name         string
	Instructions string
	Model        ModelRef
	Provider     protocol.ModelProvider
	Session      *session.Session
	Store        store.SessionStore
	// MessageStore is optional. When nil, dispatcher.go skips persistence
	// at every hook point (user message append, assistant accumulation,
	// session metadata save). main.go wires the same SQLiteMessageStore
	// it uses for sessions so a single *gorm.DB powers both.
	MessageStore store.MessageStore
	Logger       *zap.Logger
	Config       Config
	// Executor is optional. If nil, executor.New() is used.
	Executor executor.Executor
	// Tools is the tool registry driving the loop. Required; the concrete
	// built-in registry is constructed by the wiring layer (cmd/app).
	Tools protocol.ToolRegistry

	// Assembler is an optional pre-built ContextEngine. When nil, New
	// constructs a DefaultAssembler from the Config.* fields. Callers who
	// want a custom engine (e.g. for testing or alternative backends) plug
	// it in here.
	Assembler ctxengine.ContextEngine
	// AssemblerEnabled is the explicit on/off switch for the assembler
	// pipeline. When false (the zero value), the executor takes the legacy
	// fallback path even if an assembler was wired. cfg.yaml users get
	// true by default via the YAML front-end's default.
	AssemblerEnabled bool
}

// Agent is the runtime. It is goroutine-safe.
type Agent struct {
	name         string
	instructions string
	model        ModelRef
	provider     protocol.ModelProvider
	session      *session.Session
	store        store.SessionStore
	msgStore     store.MessageStore
	logger       *zap.Logger
	cfg          Config
	tools        protocol.ToolRegistry
	exec         executor.Executor
	bus          *event.Bus
	queue        *queue.Queue

	runMu    sync.Mutex
	state    agentState
	cancelFn context.CancelFunc

	// runImportedNote is set by the dispatcher for the current prompt's
	// staged imported files; Instructions() appends it so the LLM perceives
	// them. Cleared after the run (transient, not persisted).
	runImportedNote string

	// runSkillPrompt / runSkillTools are set by RunSkillSession for the
	// duration of a user-invoked skill's mini loop. Instructions() returns
	// the skill's SKILL.md body and Tools() returns the scoped registry so
	// the executor drives the loop against the skill's surface instead of
	// the generic agent instructions. Cleared after the run.
	runSkillPrompt string
	runSkillTools  protocol.ToolRegistry

	// msgIDSrc is wired by the ACP loop via AttachMessageIDSrc. The
	// executor reads it through Deps.CurrentMessageID to populate the
	// EventCommon.MessageID on every emitted event so subscribers can
	// correlate events back to the prompt that produced them.
	msgIDSrc func() string

	// runIDSrc is wired by the ACP loop via AttachRunIDSrc. The executor
	// reads it through Deps.CurrentRunID to populate EventCommon.RunID on
	// every emitted event so the renderer can abort a specific turn and
	// the renderer store can demultiplex events by turn id.
	runIDSrc func() string

	// userMsgIDSrc is wired by the ACP loop via AttachUserMessageIDSrc.
	// The dispatcher reads it through CurrentUserMessageID to key the
	// persisted user row. It is distinct from msgIDSrc so the user row is
	// not overwritten by the assistant row that shares the run's messageID.
	userMsgIDSrc func() string

	assembler        ctxengine.ContextEngine
	assemblerEnabled bool

	// lastUsage holds the most recent API-reported Usage. Updated after
	// every successful LLM turn via RecordUsage (called by the executor);
	// read by the executor during the next turn's Assemble call so the
	// ContextEngine can prefer the provider's reported token count over the
	// local rune/4 estimator. Mutex protects concurrent reads from the
	// executor loop and writes from drainStream's tail.
	lastUsageMu sync.RWMutex
	lastUsage   protocol.Usage

	// Permission gate state (spec 12). pendingPerms maps requestID → the
	// blocked executor call; permRules holds "remember this session" auto-allow
	// entries. Both are goroutine-safe: RequestPermission is called from tool
	// goroutines, ResolvePermission from the gateway RPC handler.
	permMu       sync.Mutex
	pendingPerms map[string]*pendingPermission
	permRules    []permissionRule
}

// agentState is the lifecycle phase the Agent is in.
type agentState int

const (
	stateIdle agentState = iota
	stateRunning
)

// ErrSessionRequired is returned by New when NewAgentConfig.Session is nil.
var ErrSessionRequired = errors.New("agent: Session is required")

// ErrProviderRequired is returned by New when NewAgentConfig.Provider is nil.
var ErrProviderRequired = errors.New("agent: Provider is required")

// ErrToolsRequired is returned by New when NewAgentConfig.Tools is nil. The
// built-in tool set is constructed by the wiring layer (cmd/app).
var ErrToolsRequired = errors.New("agent: Tools is required")

// New constructs an Agent and auto-registers the built-in tool set if
// NewAgentConfig.Tools is nil.
func New(cfg NewAgentConfig) (*Agent, error) {
	if cfg.Session == nil {
		return nil, ErrSessionRequired
	}
	if cfg.Provider == nil {
		return nil, ErrProviderRequired
	}
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}
	if cfg.Store == nil {
		cfg.Store = store.NewMemoryStore()
	}
	if cfg.Tools == nil {
		return nil, ErrToolsRequired
	}
	if cfg.Executor == nil {
		cfg.Executor = executor.New()
	}
	if cfg.Config.MaxTurns <= 0 {
		cfg.Config.MaxTurns = 25
	}
	if cfg.Config.ToolTimeout <= 0 {
		cfg.Config.ToolTimeout = 30 * time.Second
	}
	if cfg.Config.EventBuffer <= 0 {
		cfg.Config.EventBuffer = 64
	}
	a := &Agent{
		name:         cfg.Name,
		instructions: cfg.Instructions,
		model:        cfg.Model,
		provider:     cfg.Provider,
		session:      cfg.Session,
		store:        cfg.Store,
		msgStore:     cfg.MessageStore,
		logger:       cfg.Logger,
		cfg:          cfg.Config,
		tools:        cfg.Tools,
		exec:         cfg.Executor,
		bus:          event.NewBus(),
		queue:        queue.New(),
		pendingPerms: map[string]*pendingPermission{},
	}

	// Auto-wire the ContextEngine (spec §4.10 / §6.2). Two paths:
	//   1. caller-supplied Assembler → use as-is
	//   2. nil → construct a DefaultAssembler from the Config.* fields
	// Either way the assembler is always wired so callers can flip
	// AssemblerEnabled at runtime without rebuilding the engine.
	if cfg.Assembler != nil {
		a.assembler = cfg.Assembler
	} else {
		a.assembler = ctxengine.NewDefaultAssembler(ctxengine.Config{
			TokenBudget:          cfg.Config.TokenBudget,
			CompactTailKeep:      cfg.Config.CompactTailKeep,
			ToolResultMaxBytes:   cfg.Config.ToolResultMaxBytes,
			CompactMaxRetries:    cfg.Config.CompactMaxRetries,
			SummarizeMaxTokens:   cfg.Config.SummarizeMaxTokens,
			SystemPromptAddition: cfg.Config.SystemPromptAddition,
			AssemblerEnabled:     cfg.Config.AssemblerEnabled,
		}, a)
	}
	a.assemblerEnabled = cfg.AssemblerEnabled

	return a, nil
}

func (a *Agent) Session() *session.Session        { return a.session }
func (a *Agent) Provider() protocol.ModelProvider { return a.provider }
func (a *Agent) ModelName() string                { return a.model.Model }

// Tools returns the active tool registry. During a skill mini loop it is
// the skill's scoped registry so the executor only sees the skill's allowed
// surface; otherwise it is the agent's full registry.
func (a *Agent) Tools() protocol.ToolRegistry {
	if a.runSkillTools != nil {
		return a.runSkillTools
	}
	return a.tools
}

// Instructions returns the system prompt. During a skill mini loop it is the
// skill's SKILL.md body (self-contained); otherwise the agent instructions,
// plus the transient imported-files note for the current prompt.
func (a *Agent) Instructions() string {
	if a.runSkillPrompt != "" {
		return a.runSkillPrompt
	}
	if a.runImportedNote != "" {
		return a.instructions + "\n\n" + a.runImportedNote
	}
	return a.instructions
}
func (a *Agent) Config() executor.Config {
	return executor.Config{
		MaxTurns:    a.cfg.MaxTurns,
		ToolTimeout: a.cfg.ToolTimeout,
		TokenBudget: a.cfg.TokenBudget,
	}
}
func (a *Agent) Logger() *zap.Logger { return a.logger }
func (a *Agent) Emit(ev event.Event) { a.bus.Emit(ev) }

// Assembler returns the ContextEngine wired into the Agent, or nil if none
// has been configured. Combined with AssemblerEnabled, this lets the
// executor opt in / out of assembler-driven prompt construction.
func (a *Agent) Assembler() ctxengine.ContextEngine { return a.assembler }

// SystemSections returns caller-supplied system prompt sections merged
// into the assembler's output. Returns nil today (no caller-supplied
// sections; SystemPromptAddition is covered via cfg → default assembler).
func (a *Agent) SystemSections() []ctxengine.SystemSection { return nil }

// AssemblerEnabled reports whether the host opted into the assembler
// pipeline. Returning false forces the executor to take the legacy
// d.Session().Messages() path; see cfg.AssemblerEnabled in
// specs/features/agent-context-engine §FR-12.
func (a *Agent) AssemblerEnabled() bool { return a.assemblerEnabled }

// IsRunning reports whether Agent.Run is currently in progress. The gateway
// uses it to refuse manual context compaction while a turn is executing.
func (a *Agent) IsRunning() bool {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	return a.state == stateRunning
}

// RecordUsage stores the API-reported Usage from the just-finished turn.
// Safe to call from the executor goroutine; readers (next turn's Assemble)
// use LastUsage under the same mutex.
func (a *Agent) RecordUsage(u protocol.Usage) {
	a.lastUsageMu.Lock()
	a.lastUsage = u
	a.lastUsageMu.Unlock()
}

// LastUsage returns the most recently stored API-reported Usage. Zero value
// when no turn has completed yet (e.g. before the first LLM call), which
// the ContextEngine interprets as "fall back to the local estimator".
func (a *Agent) LastUsage() protocol.Usage {
	a.lastUsageMu.RLock()
	defer a.lastUsageMu.RUnlock()
	return a.lastUsage
}

// Session returns the agent's session (read-only access pattern; mutators
// are reserved for the executor).
func (a *Agent) SessionHandle() *session.Session { return a.session }

// Subscribe registers a new event subscriber.
func (a *Agent) Subscribe(buffer int) *event.Subscription {
	return a.bus.Subscribe(buffer)
}

// AttachMessageIDSrc wires the function the executor queries (via
// Deps.CurrentMessageID) to read the in-flight messageID. main.go passes
// a method value of acp.Loop.CurrentMessageID so every emitted event's
// EventCommon.MessageID matches the prompt that triggered the run.
func (a *Agent) AttachMessageIDSrc(src func() string) {
	a.msgIDSrc = src
}

// AttachRunIDSrc wires the function the executor and dispatcher query
// (via Deps.CurrentRunID) to read the in-flight runID. main.go passes a
// method value of acp.Loop.CurrentRunID so every emitted event's
// EventCommon.RunID matches the prompt that triggered the run.
func (a *Agent) AttachRunIDSrc(src func() string) {
	a.runIDSrc = src
}

// AttachUserMessageIDSrc wires the function the dispatcher queries (via
// CurrentUserMessageID) to read the messageID minted for the current turn's
// user message. main.go passes a method value of acp.Loop.CurrentUserMessageID.
func (a *Agent) AttachUserMessageIDSrc(src func() string) {
	a.userMsgIDSrc = src
}

// CurrentMessageID satisfies executor.Deps. Returns "" when no messageID
// source has been wired or when the agent is idle.
func (a *Agent) CurrentMessageID() string {
	if a.msgIDSrc == nil {
		return ""
	}
	return a.msgIDSrc()
}

// CurrentRunID satisfies executor.Deps. Returns "" when no runID source
// has been wired or when the agent is idle.
func (a *Agent) CurrentRunID() string {
	if a.runIDSrc == nil {
		return ""
	}
	return a.runIDSrc()
}

// CurrentUserMessageID returns the user-message id of the in-flight turn.
// Returns "" when no userMsgID source has been wired (e.g. the steer agent
// or the unit-test fast path, where nothing is persisted anyway).
func (a *Agent) CurrentUserMessageID() string {
	if a.userMsgIDSrc == nil {
		return ""
	}
	return a.userMsgIDSrc()
}

// SetGrantedReads replaces the run's granted-read set (absolute paths the
// user attached for the current message). Called by the dispatcher before
// RunConversation and cleared after.
func (a *Agent) SetGrantedReads(paths []string) {
	a.tools.SetGrantedReads(paths)
}

// ApprovePath satisfies executor.Deps — grants the sandbox one-shot access
// to a path the user allowed via the permission modal.
func (a *Agent) ApprovePath(path string) {
	a.tools.ApprovePath(path)
}

// EvaluatePermission satisfies executor.Deps. Delegates to the tool
// registry's combined path-containment + danger classification.
func (a *Agent) EvaluatePermission(toolName string, args map[string]any) protocol.PermissionEval {
	return a.tools.EvaluatePermission(toolName, args)
}

// RequestPermission satisfies executor.Deps. Emits a permission_request event
// and blocks until the renderer answers via ResolvePermission, the 60s timeout
// fires (default deny), or ctx is cancelled.
func (a *Agent) RequestPermission(ctx context.Context, req executor.PermissionRequest) (executor.PermissionResult, error) {
	id := uuid.NewString()
	ch := make(chan executor.PermissionResult, 1)
	pp := &pendingPermission{ch: ch}
	pp.timeout = time.AfterFunc(permissionTimeout, func() {
		a.deliverPermission(id, ch, executor.PermissionResult{Behavior: "deny", Message: "审批超时"})
	})
	a.permMu.Lock()
	a.pendingPerms[id] = pp
	a.permMu.Unlock()

	a.bus.Emit(event.PermissionRequestEvent{
		EventBase: event.EventBase{EventCommon: event.EventCommon{
			SessionID: a.session.ID,
			RunID:     a.CurrentRunID(),
			MessageID: a.CurrentMessageID(),
		}},
		RequestID:   id,
		ToolName:    req.ToolName,
		ToolInput:   req.ToolInput,
		DangerLevel: req.DangerLevel,
		Reason:      req.Reason,
	})

	select {
	case r := <-ch:
		return r, nil
	case <-ctx.Done():
		a.permMu.Lock()
		if cur, ok := a.pendingPerms[id]; ok {
			delete(a.pendingPerms, id)
			cur.timeout.Stop()
		}
		a.permMu.Unlock()
		return executor.PermissionResult{Behavior: "deny", Message: "运行已中断"}, ctx.Err()
	}
}

// ResolvePermission delivers the renderer's answer to the blocked executor
// call. Unknown requestID (already timed out / cancelled) is a no-op.
func (a *Agent) ResolvePermission(requestID string, result executor.PermissionResult) {
	a.permMu.Lock()
	pp, ok := a.pendingPerms[requestID]
	if ok {
		delete(a.pendingPerms, requestID)
	}
	a.permMu.Unlock()
	if !ok {
		return
	}
	pp.timeout.Stop()
	select {
	case pp.ch <- result:
	default:
	}
}

// deliverPermission removes the pending entry (idempotent) and sends the
// result to the waiting channel. Used by the timeout path and ResolvePermission.
func (a *Agent) deliverPermission(id string, ch chan executor.PermissionResult, r executor.PermissionResult) {
	a.permMu.Lock()
	if _, ok := a.pendingPerms[id]; ok {
		delete(a.pendingPerms, id)
	}
	a.permMu.Unlock()
	select {
	case ch <- r:
	default:
	}
}

// HasPermissionRule satisfies executor.Deps — whether an identical
// (tool, level, reason) request was allowed + remembered this session.
func (a *Agent) HasPermissionRule(toolName, level, reason string) bool {
	a.permMu.Lock()
	defer a.permMu.Unlock()
	for _, r := range a.permRules {
		if r.toolName == toolName && r.level == level && r.reason == reason {
			return true
		}
	}
	return false
}

// AddPermissionRule satisfies executor.Deps — records an auto-allow rule.
func (a *Agent) AddPermissionRule(toolName, level, reason string) {
	a.permMu.Lock()
	defer a.permMu.Unlock()
	for _, r := range a.permRules {
		if r.toolName == toolName && r.level == level && r.reason == reason {
			return
		}
	}
	a.permRules = append(a.permRules, permissionRule{toolName: toolName, level: level, reason: reason})
}
