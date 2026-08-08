package ctxengine

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agents/event"
	"darvin-cowork/backend/internal/agents/protocol"
)

// DefaultContextWindow is the implicit context window used when
// ctxengine.Config.ContextWindow is left at zero. darvin-cowork boots
// without an explicit window during `go run` / unit tests, so a sane
// default keeps the 4-tier cascade meaningful in development.
// Production callers set context_window: <model_context_window> in
// config.yaml.
const DefaultContextWindow = 200000

// Default tail knobs.
const (
	DefaultTailTokens    = 16384 // verbatim recent-tail budget, in tokens
	DefaultRecentKeep    = 2     // minimum recent messages kept verbatim
	DefaultMinFoldTokens = 400   // fold region below this size skips compaction unless forced
)

// minFoldFloor is the lower bound we let compactBudget fall to. The
// Assemble-time target derives from cfg.ContextWindow / 2; for very
// small windows (unit tests / dev), we clamp to this value so the
// compact target always reflects a meaningful budget rather than 0.
const minFoldFloor = 1000

// Default ratio knobs.
const (
	DefaultSoftCompactRatio    = 0.5
	DefaultToolResultSnipRatio = 0.6
	DefaultCompactRatio        = 0.8
	DefaultCompactForceRatio   = 0.9
)

// Config is the assembler's runtime configuration. The fields mirror
// the ctxengine-related subset of config.AgentConfig. agent.New copies
// values from config.AgentConfig into a ctxengine.Config at
// construction time so the assembler package does not import
// internal/config.
type Config struct {
	// ContextWindow is the LLM's hard context cap in tokens. 0
	// disables the entire auto-compact pipeline. When > 0 the
	// four ratios below derive absolute trigger thresholds via
	// `int(float64(ContextWindow) * ratio)`.
	ContextWindow int

	// The four threshold ratios driving the cascade (see assemble.go).
	// Defaults match the constants above; users override via config.yaml.
	SoftCompactRatio    float64
	ToolResultSnipRatio float64
	CompactRatio        float64
	CompactForceRatio   float64

	// CompactTailTokens is the token budget the kept tail fits
	// under. When 0 the assembler falls back to DefaultTailTokens.
	CompactTailTokens int
	// RecentKeep is the message-count floor on the kept tail —
	// compaction never keeps fewer than this many recent messages
	// even if the token budget allows more. When 0 the assembler
	// falls back to DefaultRecentKeep.
	RecentKeep int

	// ArchiveDir, when non-empty, causes Compact to persist the
	// fold region as a timestamped jsonl before the LLM call. Empty
	// disables archive (the most common configuration in fresh
	// installs). Best-effort: write failures emit a Notice but do
	// not block compaction.
	ArchiveDir string

	ToolResultMaxBytes   int
	SummarizeMaxTokens   int
	SystemPromptAddition string
	AssemblerEnabled     bool

	// MemoryFactsLimit clamps the MEMORY block FTS top-N. <= 0
	// disables the MEMORY block.
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
// summarizer / estimator / softNotified / consecutiveCompacts /
// compactStuck; projectionsMu guards the projections map. archiver
// is set via SetArchiver so the constructor signature stays stable.
type DefaultAssembler struct {
	mu           sync.RWMutex
	cfg          Config
	deps         Deps
	estimator    TokenEstimator
	summarizer   Summarizer
	sections     []SystemSection
	lastIngestAt map[string]time.Time
	archiver     Archiver

	softNotified        bool // soft 50% notice latch — emit once per window climb
	snippedThisTurn     bool // 60% snip latch — at most once per turn
	consecutiveCompacts int  // tracks repeated Compact successes → stuck latch
	compactStuck        bool // pause auto-compact when system prompt + tail exceeds budget

	projectionsMu sync.RWMutex
	projections   map[string]ContextProjection
}

// NewDefaultAssembler constructs the assembler with cfg and deps. Defaults
// are applied for the four ratios, RecentKeep, CompactTailTokens, and
// ContextWindow (see each field's doc). When deps.Provider() is non-nil,
// a DefaultSummarizer is auto-wired so Compact works out of the box;
// tests that need a fake summarizer call SetSummarizer after construction
// (overriding the default).
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
		// archive leave ArchiveDir empty. The archiver proxy is
		// weak-typed (text, detail) so agent.Agent can hand its
		// Emit channel through without depending on event.NoticeKind.
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

// SetArchiver installs a custom Archiver (tests inject fakes that record
// calls without touching disk). nil disables archive mid-session.
func (a *DefaultAssembler) SetArchiver(ar Archiver) {
	a.mu.Lock()
	a.archiver = ar
	a.mu.Unlock()
}

// ClearTurnLatches resets the per-turn state that assemble.go's
// softNotice / snip-then-don't-compact logic relies on. Called at the
// end of each Assemble so the next turn can re-emit a soft notice when
// the prompt re-climbs past the threshold, and so the snip latch does
// not suppress a second-stage snip after the user appends a turn.
func (a *DefaultAssembler) ClearTurnLatches() {
	a.mu.Lock()
	a.snippedThisTurn = false
	a.mu.Unlock()
}

// MarkConsecutiveCompact records that Compact ran successfully on this
// turn; called by Compact when it finishes. When the running count
// reaches 2 the latch flips to compactStuck=true so the next Assemble
// skips auto-compact and emits a stuck notice.
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

// ResetConsecutiveCompact clears the consecutive / stuck state. Called
// by Assemble when the prompt is back under the compact threshold so
// the next genuine run can compact again.
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
