package ctxengine

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agents/event"
	"darvin-cowork/backend/internal/agents/protocol"
)

// DefaultContextWindow is the implicit window when ContextWindow is 0
// (keeps `go run` / unit tests meaningful); production callers set
// context_window in config.yaml.
const DefaultContextWindow = 200000

// Default tail knobs.
const (
	DefaultTailTokens    = 16384
	DefaultRecentKeep    = 2
	DefaultMinFoldTokens = 400
)

// minFoldFloor is the lower bound compactBudget falls to (clamped for
// very small windows so the target reflects a meaningful budget).
const minFoldFloor = 1000

// Default ratio knobs.
const (
	DefaultSoftCompactRatio    = 0.5
	DefaultToolResultSnipRatio = 0.6
	DefaultCompactRatio        = 0.8
	DefaultCompactForceRatio   = 0.9
)

// Config is the assembler's runtime configuration, mirroring the
// ctxengine subset of config.AgentConfig (copied at construction so the
// assembler package does not import internal/config).
type Config struct {
	// ContextWindow is the LLM's hard cap; 0 disables auto-compact.
	// When > 0 the four ratios derive absolute trigger thresholds.
	ContextWindow int

	// The four threshold ratios driving the cascade; defaults match the
	// constants above.
	SoftCompactRatio    float64
	ToolResultSnipRatio float64
	CompactRatio        float64
	CompactForceRatio   float64

	// CompactTailTokens / RecentKeep: 0 falls back to the Default*.
	CompactTailTokens int
	RecentKeep        int

	// ArchiveDir, when non-empty, persists the fold region as jsonl
	// before the LLM call. Best-effort: failures emit a Notice only.
	ArchiveDir string

	ToolResultMaxBytes   int
	SummarizeMaxTokens   int
	SystemPromptAddition string
	AssemblerEnabled     bool

	// MemoryFactsLimit clamps the MEMORY block FTS top-N; <= 0 disables.
	MemoryFactsLimit int
	// MemoryFactsCacheTTL bounds the per-(sessionID, query) FTS cache; <= 0 disables.
	MemoryFactsCacheTTL time.Duration
}

// Deps is the surface the assembler needs from its host. Defined here
// (rather than imported from agent) to break the cycle
// agent -> executor -> ctxengine -> agent.
//
// MemoryFacts / MemoryBootstrap are the FTS / bootstrap seams; the
// assembler uses them only when AssembleParams.AvailableFacts is empty.
type Deps interface {
	Provider() protocol.ModelProvider
	ModelName() string
	Logger() *zap.Logger
	// Emit broadcasts agent lifecycle events (DefaultAssembler uses
	// it after Compact so EventLedger pushes the compaction boundary).
	Emit(ev event.Event)

	// MemoryFacts returns FTS hits for the current session. nil/empty = no MEMORY block.
	MemoryFacts(ctx context.Context) []Fact

	// MemoryBootstrap returns cached workspace-level bootstrap file
	// content (IDENTITY.md / SOUL.md / USER.md). Implementations MUST
	// proxy through the workspace singleton for change-notification invalidation.
	MemoryBootstrap(name string) string
}

// DefaultAssembler is the in-process ContextEngine implementation.
// Goroutine-safe: outer mu guards cfg / sections / lastIngestAt /
// summarizer / estimator / softNotified / consecutiveCompacts /
// compactStuck; projectionsMu guards the projections map.
type DefaultAssembler struct {
	mu           sync.RWMutex
	cfg          Config
	deps         Deps
	estimator    TokenEstimator
	summarizer   Summarizer
	sections     []SystemSection
	lastIngestAt map[string]time.Time
	archiver     Archiver

	softNotified        bool
	snippedThisTurn     bool
	consecutiveCompacts int
	compactStuck        bool

	projectionsMu sync.RWMutex
	projections   map[string]ContextProjection
}

// NewDefaultAssembler constructs the assembler. Defaults are applied for
// the four ratios, RecentKeep, CompactTailTokens, and ContextWindow.
// When deps.Provider() is non-nil, a DefaultSummarizer is auto-wired;
// tests override via SetSummarizer.
func NewDefaultAssembler(cfg Config, deps Deps) *DefaultAssembler {
	if cfg.RecentKeep <= 0 {
		cfg.RecentKeep = DefaultRecentKeep
	}
	if cfg.CompactTailTokens <= 0 {
		cfg.CompactTailTokens = DefaultTailTokens
	}
	if cfg.ContextWindow <= 0 {
		cfg.ContextWindow = DefaultContextWindow
	}
	if cfg.SoftCompactRatio <= 0 {
		cfg.SoftCompactRatio = DefaultSoftCompactRatio
	}
	if cfg.ToolResultSnipRatio <= 0 {
		cfg.ToolResultSnipRatio = DefaultToolResultSnipRatio
	}
	if cfg.CompactRatio <= 0 {
		cfg.CompactRatio = DefaultCompactRatio
	}
	if cfg.CompactForceRatio <= 0 {
		cfg.CompactForceRatio = DefaultCompactForceRatio
	}
	if cfg.ArchiveDir != "" {
		// Wire the default file archiver; tests that want to suppress
		// archive leave ArchiveDir empty.
		archiver := NewFileArchiver(cfg.ArchiveDir, nil)
		cfg.ArchiveDir = archiver.dir // resolved lazily inside Archive
		_ = archiver
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
	if cfg.ArchiveDir != "" {
		a.archiver = NewFileArchiver(cfg.ArchiveDir, nil)
	}
	return a
}

// Cfg returns a copy of the assembler's config.
func (a *DefaultAssembler) Cfg() Config {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg
}

// SetEstimator overrides the token estimator (test injection).
func (a *DefaultAssembler) SetEstimator(e TokenEstimator) {
	if e == nil {
		return
	}
	a.mu.Lock()
	a.estimator = e
	a.mu.Unlock()
}

// SetSummarizer overrides the summarizer (test injection).
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

// SetArchiver installs a custom Archiver (tests inject recording fakes);
// nil disables archive mid-session.
func (a *DefaultAssembler) SetArchiver(ar Archiver) {
	a.mu.Lock()
	a.archiver = ar
	a.mu.Unlock()
}

// ClearTurnLatches resets the per-turn soft-notice / snip state so the
// next Assemble can re-emit a notice when the prompt re-climbs the
// threshold.
func (a *DefaultAssembler) ClearTurnLatches() {
	a.mu.Lock()
	a.snippedThisTurn = false
	a.mu.Unlock()
}

// MarkConsecutiveCompact records a successful Compact; after 2 the latch
// flips compactStuck so the next Assemble skips auto-compact.
func (a *DefaultAssembler) MarkConsecutiveCompact() (stuckNow bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.consecutiveCompacts++
	if a.consecutiveCompacts >= 2 && !a.compactStuck {
		a.compactStuck = true
		stuckNow = true
	}
	return
}

// ResetConsecutiveCompact clears the consecutive / stuck state (called
// when the prompt drops back under the compact threshold).
func (a *DefaultAssembler) ResetConsecutiveCompact() {
	a.mu.Lock()
	a.consecutiveCompacts = 0
	a.compactStuck = false
	a.mu.Unlock()
}

// Stuck reports whether the auto-compact latch is currently engaged.
func (a *DefaultAssembler) Stuck() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.compactStuck
}

// BuildSystemSections assembles the ordered system-section list the LLM
// sees this turn, in priority order (see sections.go). Non-empty facts
// win (caller override); otherwise it falls through to Deps.MemoryFacts.
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

// LastIngestAt returns the most recent Ingest timestamp for a session,
// or the zero time when none has happened. Test-only helper.
func (a *DefaultAssembler) LastIngestAt(sessionID string) time.Time {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastIngestAt[sessionID]
}

// Info returns the engine's identity metadata.
func (a *DefaultAssembler) Info() Info {
	return Info{Name: "default", Version: ""}
}
