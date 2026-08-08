package ctxengine

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agents/event"
	"darvin-cowork/backend/internal/agents/protocol"
)

// Config is the assembler's runtime configuration. The fields mirror
// the ctxengine-related subset of config.AgentConfig. agent.New copies
// values from config.AgentConfig into a ctxengine.Config at
// construction time so the assembler package does not import
// internal/config.
type Config struct {
	TokenBudget          int
	CompactTailKeep      int
	CompactTailTokens    int
	ToolResultMaxBytes   int
	CompactMaxRetries    int
	SummarizeMaxTokens   int
	SystemPromptAddition string
	AssemblerEnabled     bool

	// MemoryFactsLimit clamps the MEMORY block FTS top-N. <= 0
	// disables the MEMORY block (consistent with FR-12 degrade).
	MemoryFactsLimit int
	// MemoryFactsCacheTTL bounds the per-(sessionID, query) FTS cache.
	// <= 0 disables caching — every Assemble re-queries FTS.
	MemoryFactsCacheTTL time.Duration
}

// Deps is the surface the assembler needs from its host (agent.Agent).
// Defined here (rather than imported from agent) to break the cycle
// agent -> executor -> ctxengine -> agent.
//
// MemoryFacts / MemoryBootstrap are the FTS / bootstrap seams. The
// assembler uses them only when AssembleParams.AvailableFacts is
// empty (caller override wins).
type Deps interface {
	Provider() protocol.ModelProvider
	ModelName() string
	Logger() *zap.Logger
	// Emit broadcasts agent lifecycle events. DefaultAssembler uses
	// it after triggering Compact so EventLedger can push the
	// compaction boundary to the renderer.
	Emit(ev event.Event)

	// MemoryFacts returns the FTS hits for the agent's current
	// session. nil/empty means "no MEMORY block". sessionID is
	// implicit (Agent is per-session).
	MemoryFacts(ctx context.Context) []Fact

	// MemoryBootstrap returns the cached workspace-level bootstrap
	// file content (IDENTITY.md / SOUL.md / USER.md). Empty when the
	// memory subsystem is disabled or the file is missing.
	// Implementations MUST proxy through the workspace singleton so
	// change-notification invalidation propagates.
	MemoryBootstrap(name string) string
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

// BuildSystemSections assembles the ordered system-section list the
// LLM sees on this turn, in spec priority order (see sections.go).
// When facts is non-empty it is used verbatim (caller override);
// otherwise the assembler falls through to Deps.MemoryFacts for FTS.
func (a *DefaultAssembler) BuildSystemSections(
	ctx context.Context,
	_ string,
	skills []SkillSummary,
	facts []Fact,
	mcp []MCPServerInfo,
) []SystemSection {
	a.mu.RLock()
	cfg := a.cfg
	registered := append([]SystemSection(nil), a.sections...)
	a.mu.RUnlock()

	out := make([]SystemSection, 0, len(registered)+8)
	out = append(out, registered...)

	if a.deps != nil {
		if v := a.deps.MemoryBootstrap("IDENTITY.md"); v != "" {
			if c := renderIdentitySection(v); c != "" {
				out = append(out, SystemSection{Name: "identity", Content: c, Priority: PriorityIdentity})
			}
		}
		if v := a.deps.MemoryBootstrap("SOUL.md"); v != "" {
			if c := renderSoulSection(v); c != "" {
				out = append(out, SystemSection{Name: "soul", Content: c, Priority: PrioritySoul})
			}
		}
		if v := a.deps.MemoryBootstrap("USER.md"); v != "" {
			if c := renderUserSection(v); c != "" {
				out = append(out, SystemSection{Name: "user", Content: c, Priority: PriorityUser})
			}
		}
	}

	effectiveFacts := facts
	if len(effectiveFacts) == 0 && a.deps != nil && cfg.MemoryFactsLimit > 0 {
		effectiveFacts = a.deps.MemoryFacts(ctx)
	}
	out = append(out, BuiltInSections(skills, effectiveFacts, mcp)...)
	return out
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
