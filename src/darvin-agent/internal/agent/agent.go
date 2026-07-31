// Package agent is the root of the agent runtime. It wires the sub-packages
// (event, queue, session, store, tool, executor, llm) into a single Agent
// and exposes the public API (Run / Prompt / Steer / FollowUp / Abort /
// Subscribe).
package agent

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agent/ctxengine"
	"darvin-cowork/backend/internal/agent/event"
	"darvin-cowork/backend/internal/agent/executor"
	"darvin-cowork/backend/internal/agent/llm"
	"darvin-cowork/backend/internal/agent/queue"
	"darvin-cowork/backend/internal/agent/session"
	"darvin-cowork/backend/internal/agent/store"
	"darvin-cowork/backend/internal/agent/tool"
)

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
	Provider     llm.ModelProvider
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
	// Tools is optional. If nil, the 5 built-in tools are auto-registered
	// (read_file / write_file / edit_file / list_dir / shell).
	Tools *tool.Registry

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
	provider     llm.ModelProvider
	session      *session.Session
	store        store.SessionStore
	msgStore     store.MessageStore
	logger       *zap.Logger
	cfg          Config
	tools        *tool.Registry
	exec         executor.Executor
	bus          *event.Bus
	queue        *queue.Queue

	runMu    sync.Mutex
	state    agentState
	cancelFn context.CancelFunc

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

	assembler        ctxengine.ContextEngine
	assemblerEnabled bool

	// lastUsage holds the most recent API-reported llm.Usage. Updated after
	// every successful LLM turn via RecordUsage (called by the executor);
	// read by the executor during the next turn's Assemble call so the
	// ContextEngine can prefer the provider's reported token count over the
	// local rune/4 estimator. Mutex protects concurrent reads from the
	// executor loop and writes from drainStream's tail.
	lastUsageMu sync.RWMutex
	lastUsage   llm.Usage
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
		reg, err := tool.NewBuiltins(cfg.Config.Workdir, cfg.Config.ShellAllowlist)
		if err != nil {
			return nil, err
		}
		cfg.Tools = reg
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

func (a *Agent) Session() *session.Session   { return a.session }
func (a *Agent) Tools() *tool.Registry       { return a.tools }
func (a *Agent) Provider() llm.ModelProvider { return a.provider }
func (a *Agent) ModelName() string           { return a.model.Model }
func (a *Agent) Instructions() string        { return a.instructions }
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

// RecordUsage stores the API-reported Usage from the just-finished turn.
// Safe to call from the executor goroutine; readers (next turn's Assemble)
// use LastUsage under the same mutex.
func (a *Agent) RecordUsage(u llm.Usage) {
	a.lastUsageMu.Lock()
	a.lastUsage = u
	a.lastUsageMu.Unlock()
}

// LastUsage returns the most recently stored API-reported Usage. Zero value
// when no turn has completed yet (e.g. before the first LLM call), which
// the ContextEngine interprets as "fall back to the local estimator".
func (a *Agent) LastUsage() llm.Usage {
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
