// Package agent is the root of the agent runtime. It wires the sub-packages
// (event, queue, session, store, executor, protocol) and the four state
// sub-packages (msgid, perm, runtime, usage) into a single Agent and
// exposes the public API (Run / Prompt / Abort / Subscribe). Capability
// implementations (provider, tool registry) are injected from outside
// through the protocol contract.
package agent

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agents/ctxengine"
	"darvin-cowork/backend/internal/agents/event"
	"darvin-cowork/backend/internal/agents/executor"
	"darvin-cowork/backend/internal/agents/msgid"
	"darvin-cowork/backend/internal/agents/perm"
	"darvin-cowork/backend/internal/agents/protocol"
	"darvin-cowork/backend/internal/agents/queue"
	"darvin-cowork/backend/internal/agents/runtime"
	"darvin-cowork/backend/internal/agents/session"
	"darvin-cowork/backend/internal/agents/store"
	"darvin-cowork/backend/internal/agents/usage"
	"darvin-cowork/backend/internal/memory"
)

// BootstrapReader returns the workspace-level bootstrap file content
// (IDENTITY.md / SOUL.md / USER.md). Declared as an interface here so
// the agents package does not depend on the runtime package (which
// already imports agents) — runtime.WorkspaceBootstrap satisfies it.
//
// The implementation MUST be the workspace-level singleton so
// bootstrap.write invalidation propagates to every session.
type BootstrapReader interface {
	Get(name string) string
}

// ModelRef identifies a model on a specific provider. The provider name is
// matched against registered ModelProvider implementations.
type ModelRef struct {
	Provider string
	Model    string
}

// Config is the runtime configuration for an Agent.
//
// The ContextEngine block (ContextWindow … SystemPromptAddition) is
// forwarded to the auto-constructed DefaultAssembler at New() time.
// AssemblerEnabled is the post-construction flag: zero maps to
// "disabled" (executor takes the legacy fallback); true means the
// executor dispatches prompt construction through the assembler.
type Config struct {
	MaxTurns       int
	ToolTimeout    time.Duration
	Workdir        string
	ShellAllowlist []string
	EventBuffer    int

	// ContextEngine knobs (mirrors ctxengine.Config subset); populated
	// from cfg.yaml and forwarded to ctxengine.NewDefaultAssembler.
	ContextWindow        int
	SoftCompactRatio     float64
	ToolResultSnipRatio  float64
	CompactRatio         float64
	CompactForceRatio    float64
	CompactTailTokens    int
	RecentKeep           int
	ArchiveDir           string
	ToolResultMaxBytes   int
	SummarizeMaxTokens   int
	SystemPromptAddition string
	AssemblerEnabled     bool

	// MemoryFactsLimit caps the FTS hits in the <MEMORY> block; <= 0 disables.
	MemoryFactsLimit int
	// MemoryFactsCacheTTL bounds the per-(sessionID, query) FTS cache; <= 0 disables.
	MemoryFactsCacheTTL time.Duration
}

// NewAgentConfig is the constructor input.
type NewAgentConfig struct {
	Name         string
	Instructions string
	Model        ModelRef
	Provider     protocol.ModelProvider
	Session      *session.Session
	Store        store.SessionStore
	// MessageStore is optional; nil skips persistence at every dispatcher hook.
	MessageStore store.MessageStore
	// UsageStore is optional; when non-nil, Agent writes a snapshot per Run.
	UsageStore store.UsageStore
	Logger     *zap.Logger
	Config     Config
	// Executor is optional; nil falls back to executor.New().
	Executor executor.Executor
	// Tools is the tool registry driving the loop. Required.
	Tools protocol.ToolRegistry

	// Assembler is an optional pre-built ContextEngine. When nil, New
	// constructs a DefaultAssembler from the Config.* fields.
	Assembler ctxengine.ContextEngine
	// AssemblerEnabled is the explicit on/off switch for the assembler
	// pipeline. False (zero) takes the legacy fallback even if Assembler is wired.
	AssemblerEnabled bool

	// Skills / Mcp feed the assembler's discovery lists; nil skips the respective step.
	Skills SkillsLister
	Mcp    McpLister

	// Memory feeds ctxengine.MemoryFacts; nil skips the MEMORY block.
	Memory *memory.Manager
	// WorkspaceBootstrap feeds ctxengine.MemoryBootstrap (IDENTITY/SOUL/USER).
	WorkspaceBootstrap BootstrapReader
	// DigestStore persists compaction summaries; nil disables persistence.
	DigestStore store.DigestStore
}

// SkillsLister is the narrow surface the agent needs from a skills registry.
type SkillsLister interface {
	ListEnabled() []SkillEntry
}

// SkillEntry mirrors the public fields of internal/skills.SkillEntry.
type SkillEntry struct {
	ID          string
	Name        string
	Description string
	Enabled     bool
}

// McpLister is the narrow surface the agent needs from an MCP registry.
type McpLister interface {
	ListServers() []McpServerSummary
}

// McpServerSummary is the agent-facing shape of one MCP server.
type McpServerSummary struct {
	ServerID  string
	Name      string
	ToolCount int
	Tools     []string
}

// Agent is the runtime. It is goroutine-safe. Transient state lives in
// the four state sub-packages; this type is mostly wiring plus a couple
// of run-scoped fields (imported file note, skill prompt / tools) that
// Run sets and Run tail clears.
type Agent struct {
	name         string
	instructions string
	model        ModelRef
	provider     protocol.ModelProvider
	session      *session.Session
	store        store.SessionStore
	msgStore     store.MessageStore
	usageStore   store.UsageStore
	logger       *zap.Logger
	cfg          Config
	tools        protocol.ToolRegistry
	exec         executor.Executor
	bus          *event.Bus
	queue        *queue.Queue

	controller  *runtime.Controller
	perm        *perm.Gate
	msgidBridge *msgid.Bridge
	tracker     *usage.Tracker

	// runImportedNote is the current prompt's staged imported files note;
	// Instructions() appends it so the LLM perceives them. Cleared after run.
	runImportedNote string

	// runSkillPrompt / runSkillTools are set by RunSkillSession for a
	// user-invoked skill's mini loop; cleared after the run.
	runSkillPrompt string
	runSkillTools  protocol.ToolRegistry

	assembler        ctxengine.ContextEngine
	assemblerEnabled bool

	skills SkillsLister
	mcp    McpLister

	memoryMgr      *memory.Manager
	workspaceBstrp BootstrapReader
	digestStore    store.DigestStore

	// toolTransformer normalises a tool result before the executor forwards
	// it to the LLM (set via SetToolResultTransformer; nil = no transform).
	toolTransformer func(protocol.Result) protocol.Result
}

// agentState / stateIdle / stateRun are local aliases used by the run
// lifecycle so helpers can compare without importing runtime's State enum.
type agentState = int

const (
	stateIdle agentState = 0
	stateRun  agentState = 1
)

// ErrSessionRequired is returned by New when NewAgentConfig.Session is nil.
var ErrSessionRequired = errors.New("agent: Session is required")

// ErrProviderRequired is returned by New when NewAgentConfig.Provider is nil.
var ErrProviderRequired = errors.New("agent: Provider is required")

// ErrToolsRequired is returned by New when NewAgentConfig.Tools is nil. The
// built-in tool set is constructed by the wiring layer (cmd/app).
var ErrToolsRequired = errors.New("agent: Tools is required")

// New constructs an Agent.
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
	bus := event.NewBus()
	bridge := msgid.NewBridge()
	tracker := usage.NewTracker()
	a := &Agent{
		name:           cfg.Name,
		instructions:   cfg.Instructions,
		model:          cfg.Model,
		provider:       cfg.Provider,
		session:        cfg.Session,
		skills:         cfg.Skills,
		mcp:            cfg.Mcp,
		store:          cfg.Store,
		msgStore:       cfg.MessageStore,
		usageStore:     cfg.UsageStore,
		logger:         cfg.Logger,
		cfg:            cfg.Config,
		tools:          cfg.Tools,
		exec:           cfg.Executor,
		bus:            bus,
		queue:          queue.New(),
		controller:     runtime.NewController(),
		perm:           perm.NewGate(bus, cfg.Logger, nil, perm.DefaultTimeout),
		msgidBridge:    bridge,
		tracker:        tracker,
		memoryMgr:      cfg.Memory,
		workspaceBstrp: cfg.WorkspaceBootstrap,
		digestStore:    cfg.DigestStore,
	}

	// Auto-wire the ContextEngine: caller-supplied Assembler wins, else
	// build a DefaultAssembler from Config.* fields. Either way the
	// assembler is always wired so callers can flip AssemblerEnabled
	// at runtime without rebuilding the engine.
	if cfg.Assembler != nil {
		a.assembler = cfg.Assembler
	} else {
		a.assembler = ctxengine.NewDefaultAssembler(ctxengine.Config{
			ContextWindow:        cfg.Config.ContextWindow,
			SoftCompactRatio:     cfg.Config.SoftCompactRatio,
			ToolResultSnipRatio:  cfg.Config.ToolResultSnipRatio,
			CompactRatio:         cfg.Config.CompactRatio,
			CompactForceRatio:    cfg.Config.CompactForceRatio,
			CompactTailTokens:    cfg.Config.CompactTailTokens,
			RecentKeep:           cfg.Config.RecentKeep,
			ArchiveDir:           cfg.Config.ArchiveDir,
			ToolResultMaxBytes:   cfg.Config.ToolResultMaxBytes,
			SummarizeMaxTokens:   cfg.Config.SummarizeMaxTokens,
			SystemPromptAddition: cfg.Config.SystemPromptAddition,
			AssemblerEnabled:     cfg.Config.AssemblerEnabled,
			MemoryFactsLimit:     cfg.Config.MemoryFactsLimit,
			MemoryFactsCacheTTL:  cfg.Config.MemoryFactsCacheTTL,
		}, a)
	}
	a.assemblerEnabled = cfg.AssemblerEnabled

	// The Gate needs an EventContext that reads turn ids. Wire it once the
	// Agent exists so it can call back into the bridge / Session.
	a.perm.AttachContext(permEventContext{a: a})

	return a, nil
}

// permEventContext adapts an *Agent to perm.EventContext so perm.Gate
// can read live turn ids without importing the agent root.
type permEventContext struct{ a *Agent }

func (p permEventContext) SessionID() string { return p.a.session.ID }
func (p permEventContext) RunID() string     { return p.a.msgidBridge.CurrentRunID() }
func (p permEventContext) MessageID() string { return p.a.msgidBridge.CurrentMessageID() }

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
		MaxTurns:      a.cfg.MaxTurns,
		ToolTimeout:   a.cfg.ToolTimeout,
		ContextWindow: a.cfg.ContextWindow,
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
// d.Session().Messages() path.
func (a *Agent) AssemblerEnabled() bool { return a.assemblerEnabled }

// IsRunning reports whether Agent.Run is currently in progress. The gateway
// uses it to refuse manual context compaction while a turn is executing.
func (a *Agent) IsRunning() bool { return a.controller.IsRunning() }

// RecordUsage stores the API-reported Usage from the just-finished turn
// and tags it with the model name so the persisted snapshot can pick the
// right context window on rehydrate. Safe to call from the executor
// goroutine; readers (next turn's Assemble, Run-tail persistence) use
// LastUsage / Snapshot under the same mutex.
func (a *Agent) RecordUsage(u protocol.Usage, model string) {
	a.tracker.RecordWithModel(u, model)
}

// LastUsage returns the most recently stored API-reported Usage. Zero value
// when no turn has completed yet (e.g. before the first LLM call), which
// the ContextEngine interprets as "fall back to the local estimator".
func (a *Agent) LastUsage() protocol.Usage { return a.tracker.Last() }

// UsageSnapshot returns the Tracker's full state (last + cumulative +
// model) for the persistence layer's Run-tail write. Nil when no record
// has been captured yet — the caller skips the row write in that case.
func (a *Agent) UsageSnapshot() usage.Snapshot { return a.tracker.Snapshot() }

// persistUsageSnapshot writes the current Tracker snapshot to SQLite.
// Called from Run tail; failures are warn-and-continue (snapshot is
// best-effort — the live event stream still carries usage for the
// active session). The percent / context-window fields reuse the same
// numbers the context_usage event carries so the renderer can
// rehydrate the indicator on session switch without recomputing them.
func (a *Agent) persistUsageSnapshot(ctx context.Context) {
	if a.usageStore == nil {
		return
	}
	snap := a.tracker.Snapshot()
	if snap.Last == nil {
		return
	}
	used, ctxTokens := a.contextUsageInputs()
	percent := 0
	if used > 0 && ctxTokens > 0 {
		percent = int(float64(used) / float64(ctxTokens) * 100)
	}
	rec := &store.UsageRecord{
		SessionID:         a.session.ID,
		Last:              snap.Last,
		Total:             snap.Total,
		LastContextTokens: ctxTokens,
		LastPercent:       percent,
		LastModel:         snap.LastModel,
		RequestCount:      snap.RequestCount,
		UpdatedAt:         snap.UpdatedAt,
	}
	if err := a.usageStore.Save(ctx, rec); err != nil && a.logger != nil {
		a.logger.Warn("persist usage snapshot failed",
			zap.String("session_id", a.session.ID),
			zap.Error(err))
	}
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
// a method value of agentloop.Loop.CurrentMessageID so every emitted event's
// EventCommon.MessageID matches the prompt that triggered the run.
func (a *Agent) AttachMessageIDSrc(src func() string) {
	a.msgidBridge.AttachMessageID(src)
}

// AttachRunIDSrc wires the function the executor and dispatcher query
// (via Deps.CurrentRunID) to read the in-flight runID. main.go passes a
// method value of agentloop.Loop.CurrentRunID so every emitted event's
// EventCommon.RunID matches the prompt that triggered the run.
func (a *Agent) AttachRunIDSrc(src func() string) {
	a.msgidBridge.AttachRunID(src)
}

// AttachUserMessageIDSrc wires the function the dispatcher queries (via
// CurrentUserMessageID) to read the messageID minted for the current turn's
// user message. main.go passes a method value of agentloop.Loop.CurrentUserMessageID.
func (a *Agent) AttachUserMessageIDSrc(src func() string) {
	a.msgidBridge.AttachUserMessageID(src)
}

// CurrentMessageID satisfies executor.Deps. Returns "" when no messageID
// source has been wired or when the agent is idle.
func (a *Agent) CurrentMessageID() string { return a.msgidBridge.CurrentMessageID() }

// CurrentRunID satisfies executor.Deps. Returns "" when no runID source
// has been wired or when the agent is idle.
func (a *Agent) CurrentRunID() string { return a.msgidBridge.CurrentRunID() }

// CurrentUserMessageID returns the user-message id of the in-flight turn.
// Returns "" when no userMsgID source has been wired (unit-test fast path
// where nothing is persisted anyway).
func (a *Agent) CurrentUserMessageID() string { return a.msgidBridge.CurrentUserMessageID() }

// SetGrantedReads replaces the run's granted-read set (absolute paths the
// user attached for the current message). Called by the dispatcher before
// RunConversation and cleared after.
func (a *Agent) SetGrantedReads(paths []string) {
	a.perm.SetGrantedReads(paths, a.tools)
}

// ApprovePath satisfies executor.Deps — grants the sandbox one-shot access
// to a path the user allowed via the permission modal.
func (a *Agent) ApprovePath(path string) { a.perm.ApprovePath(path, a.tools) }

// EvaluatePermission satisfies executor.Deps. Delegates to the tool
// registry's combined path-containment + danger classification.
func (a *Agent) EvaluatePermission(toolName string, args map[string]any) protocol.PermissionEval {
	return a.perm.EvaluatePermission(toolName, args, a.tools)
}

// RequestPermission satisfies executor.Deps. Emits a permission_request event
// and blocks until the renderer answers via ResolvePermission, the timeout
// fires (default deny), or ctx is cancelled.
func (a *Agent) RequestPermission(ctx context.Context, req executor.PermissionRequest) (executor.PermissionResult, error) {
	return a.perm.RequestPermission(ctx, req, a.tools)
}

// ResolvePermission delivers the renderer's answer to the blocked executor
// call. Unknown requestID (already timed out / cancelled) is a no-op.
func (a *Agent) ResolvePermission(requestID string, result executor.PermissionResult) {
	a.perm.ResolvePermission(requestID, result)
}

// HasPermissionRule satisfies executor.Deps — whether an identical
// (tool, level, reason) request was allowed + remembered this session.
func (a *Agent) HasPermissionRule(toolName, level, reason string) bool {
	return a.perm.HasRule(toolName, level, reason)
}

// AddPermissionRule satisfies executor.Deps — records an auto-allow rule.
func (a *Agent) AddPermissionRule(toolName, level, reason string) {
	a.perm.AddRule(toolName, level, reason)
}

// ResultTransformer satisfies executor.Deps — returns the harness's tool
// result normaliser, or nil when no harness is wired.
func (a *Agent) ResultTransformer() func(protocol.Result) protocol.Result {
	return a.toolTransformer
}

// SetToolResultTransformer installs the tool-result normaliser the
// executor will call after every tool call. Intended for the wiring
// layer: the harness feeds a tooldridge.Surface.ApplyMiddleware closure
// in here, and the embedded runtime's output is normalised consistently
// with future CLI / plugin backends.
func (a *Agent) SetToolResultTransformer(t func(protocol.Result) protocol.Result) {
	a.toolTransformer = t
}

// SkillSummaries satisfies executor.Deps. nil registry → nil slice.
func (a *Agent) SkillSummaries() []ctxengine.SkillSummary {
	if a.skills == nil {
		return nil
	}
	entries := a.skills.ListEnabled()
	out := make([]ctxengine.SkillSummary, 0, len(entries))
	for _, e := range entries {
		name := e.Name
		if name == "" {
			name = e.ID
		}
		out = append(out, ctxengine.SkillSummary{Name: name, Description: e.Description})
	}
	return out
}

// McpServers satisfies executor.Deps. nil registry → nil slice.
func (a *Agent) McpServers() []ctxengine.MCPServerInfo {
	if a.mcp == nil {
		return nil
	}
	servers := a.mcp.ListServers()
	out := make([]ctxengine.MCPServerInfo, 0, len(servers))
	for _, s := range servers {
		out = append(out, ctxengine.MCPServerInfo{Name: s.Name, Tools: s.Tools})
	}
	return out
}

// MemoryFacts satisfies ctxengine.Deps. nil manager / empty hits /
// ctx error collapse to nil so BuiltInSections skips the MEMORY
// block.
func (a *Agent) MemoryFacts(ctx context.Context) []ctxengine.Fact {
	if a.memoryMgr == nil || !a.memoryMgr.Enabled() {
		return nil
	}
	q := a.recentUserQuery(3)
	if q == "" {
		return nil
	}
	hits := a.memoryMgr.Search(ctx, q, a.cfg.MemoryFactsLimit)
	if len(hits) == 0 {
		return nil
	}
	out := make([]ctxengine.Fact, 0, len(hits))
	for _, h := range hits {
		out = append(out, ctxengine.Fact{Content: h.Text, Source: h.Section})
	}
	return out
}

// MemoryBootstrap satisfies ctxengine.Deps. MUST proxy through
// workspaceBstrp.Get — bypassing the singleton (e.g. via
// memoryMgr.ReadBootstrap) defeats the change-notification machinery
// so bootstrap.write RPCs never reach the LLM.
func (a *Agent) MemoryBootstrap(name string) string {
	if a.workspaceBstrp == nil {
		return ""
	}
	return a.workspaceBstrp.Get(name)
}

// PersistCompaction writes a new digest row to session_digests.
// Sequence is allocated by DigestStore.Save so concurrent saves
// cannot duplicate. Failures are warn-and-continue.
func (a *Agent) PersistCompaction(ctx context.Context, res ctxengine.CompactResult) error {
	if a.digestStore == nil || !res.Success {
		return nil
	}
	checkpointID := ""
	if res.Checkpoint != nil {
		checkpointID = res.Checkpoint.ID
	}
	rec := &store.SessionDigest{
		ID:                 "digest-" + checkpointID,
		SessionID:          a.session.ID,
		Summary:            res.Summary,
		TokensBefore:       res.TokensBefore,
		TokensAfter:        res.TokensAfter,
		FirstKeptID:        res.FirstKeptID,
		FirstKeptTimestamp: res.FirstKeptTimestamp,
		CompactReason:      res.Reason,
		SourceCompactID:    checkpointID,
		CreatedAt:          time.Now().UnixMilli(),
	}
	if err := a.digestStore.Save(ctx, rec); err != nil && a.logger != nil {
		a.logger.Warn("persist compaction failed",
			zap.String("session_id", a.session.ID),
			zap.String("digest_id", rec.ID),
			zap.Error(err))
		return err
	}
	return nil
}

// recentUserQuery concatenates the last n user messages' Content into a
// single query string for MEMORY FTS. Empty when the session has no
// user turns yet — the assembler skips the MEMORY block in that case.
func (a *Agent) recentUserQuery(n int) string {
	if n <= 0 || a.session == nil {
		return ""
	}
	msgs := a.session.Messages()
	parts := make([]string, 0, n)
	for i := len(msgs) - 1; i >= 0 && len(parts) < n; i-- {
		if msgs[i].Role == protocol.RoleUser && msgs[i].Content != "" {
			parts = append(parts, msgs[i].Content)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	// Reverse to chronological order so the query reads naturally.
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}
