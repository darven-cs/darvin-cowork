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
type Config struct {
	MaxTurns       int
	ToolTimeout    time.Duration
	Workdir        string
	ShellAllowlist []string
	EventBuffer    int
}

// NewAgentConfig is the constructor input.
type NewAgentConfig struct {
	Name         string
	Instructions string
	Model        ModelRef
	Provider     llm.ModelProvider
	Session      *session.Session
	Store        store.SessionStore
	Logger       *zap.Logger
	Config       Config
	// Executor is optional. If nil, executor.New() is used.
	Executor executor.Executor
	// Tools is optional. If nil, the 5 built-in tools are auto-registered
	// (read_file / write_file / edit_file / list_dir / shell).
	Tools *tool.Registry
}

// Agent is the runtime. It is goroutine-safe.
type Agent struct {
	name         string
	instructions string
	model        ModelRef
	provider     llm.ModelProvider
	session      *session.Session
	store        store.SessionStore
	logger       *zap.Logger
	cfg          Config
	tools        *tool.Registry
	exec         executor.Executor
	bus          *event.Bus
	queue        *queue.Queue

	runMu    sync.Mutex
	state    agentState
	cancelFn context.CancelFunc
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
	return &Agent{
		name:         cfg.Name,
		instructions: cfg.Instructions,
		model:        cfg.Model,
		provider:     cfg.Provider,
		session:      cfg.Session,
		store:        cfg.Store,
		logger:       cfg.Logger,
		cfg:          cfg.Config,
		tools:        cfg.Tools,
		exec:         cfg.Executor,
		bus:          event.NewBus(),
		queue:        queue.New(),
	}, nil
}

// --- executor.Deps implementation ---

func (a *Agent) Session() *session.Session   { return a.session }
func (a *Agent) Tools() *tool.Registry       { return a.tools }
func (a *Agent) Provider() llm.ModelProvider { return a.provider }
func (a *Agent) ModelName() string           { return a.model.Model }
func (a *Agent) Instructions() string        { return a.instructions }
func (a *Agent) Config() executor.Config {
	return executor.Config{
		MaxTurns:    a.cfg.MaxTurns,
		ToolTimeout: a.cfg.ToolTimeout,
	}
}
func (a *Agent) Emit(ev event.Event) { a.bus.Emit(ev) }

// --- public API delegates to dispatcher.go ---

// Session returns the agent's session (read-only access pattern; mutators
// are reserved for the executor).
func (a *Agent) SessionHandle() *session.Session { return a.session }

// Subscribe registers a new event subscriber.
func (a *Agent) Subscribe(buffer int) *event.Subscription {
	return a.bus.Subscribe(buffer)
}
