package ctxengine

import (
	"sync"
	"time"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agents/event"
	"darvin-cowork/backend/internal/llm"
)

// Config is the assembler's runtime configuration. The fields mirror the
// ctxengine-related subset of config.AgentConfig (see
// specs/features/agent-context-engine §FR-12). agent.New copies values
// from config.AgentConfig into a ctxengine.Config at construction time so
// the assembler package does not import internal/config.
type Config struct {
	TokenBudget          int
	CompactTailKeep      int
	ToolResultMaxBytes   int
	CompactMaxRetries    int
	SummarizeMaxTokens   int
	SystemPromptAddition string
	AssemblerEnabled     bool
}

// Deps is the surface the assembler needs from its host (agent.Agent).
// Defined here (rather than imported from agent) to break the cycle
// agent -> executor -> ctxengine -> agent.
type Deps interface {
	Provider() llm.ModelProvider
	ModelName() string
	Logger() *zap.Logger
	// Emit 发布 agent 生命周期事件；DefaultAssembler 在触发压缩后用它
	// 广播 CompactionEvent，让 EventLedger 能把压缩边界推到 renderer。
	Emit(ev event.Event)
}

// DefaultAssembler is the in-process ContextEngine implementation. It is
// goroutine-safe: the outer mu guards cfg / sections / lastIngestAt /
// summarizer / estimator; projectionsMu guards the projections map.
type DefaultAssembler struct {
	mu           sync.RWMutex
	cfg          Config
	deps         Deps
	estimator    TokenEstimator
	summarizer   Summarizer
	sections     []SystemSection
	lastIngestAt map[string]time.Time

	projectionsMu sync.RWMutex
	projections   map[string]ContextProjection
}

// NewDefaultAssembler constructs the assembler with cfg and deps. Defaults
// are applied for CompactTailKeep (6) and CompactMaxRetries (0). When
// deps.Provider() is non-nil, a DefaultSummarizer is auto-wired so Compact
// works out of the box; tests that need a fake summarizer call
// SetSummarizer after construction (overriding the default).
func NewDefaultAssembler(cfg Config, deps Deps) *DefaultAssembler {
	if cfg.CompactTailKeep <= 0 {
		cfg.CompactTailKeep = 6
	}
	if cfg.CompactMaxRetries < 0 {
		cfg.CompactMaxRetries = 0
	}
	a := &DefaultAssembler{
		cfg:          cfg,
		deps:         deps,
		estimator:    EstimateCharsOver4,
		lastIngestAt: map[string]time.Time{},
		projections:  map[string]ContextProjection{},
	}
	if deps != nil && deps.Provider() != nil {
		a.summarizer = NewDefaultSummarizer(deps.Provider())
	}
	return a
}

// Cfg returns a copy of the assembler's config (read-only access).
func (a *DefaultAssembler) Cfg() Config {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg
}

// SetEstimator overrides the token estimator (test injection only).
func (a *DefaultAssembler) SetEstimator(e TokenEstimator) {
	if e == nil {
		return
	}
	a.mu.Lock()
	a.estimator = e
	a.mu.Unlock()
}

// SetSummarizer overrides the summarizer (test injection only).
func (a *DefaultAssembler) SetSummarizer(s Summarizer) {
	if s == nil {
		return
	}
	a.mu.Lock()
	a.summarizer = s
	a.mu.Unlock()
}

// SetSections replaces the registered system sections.
func (a *DefaultAssembler) SetSections(s []SystemSection) {
	a.mu.Lock()
	a.sections = append([]SystemSection(nil), s...)
	a.mu.Unlock()
}

// LastIngestAt returns the timestamp recorded for the most recent Ingest
// (or IngestBatch) call against the given session ID. Returns the zero
// time.Time when no Ingest has happened yet. Test-only helper — production
// callers observe ingestion through session.Messages, not this map.
func (a *DefaultAssembler) LastIngestAt(sessionID string) time.Time {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastIngestAt[sessionID]
}

// Info returns the engine's identity metadata.
func (a *DefaultAssembler) Info() Info {
	return Info{Name: "default", Version: ""}
}
