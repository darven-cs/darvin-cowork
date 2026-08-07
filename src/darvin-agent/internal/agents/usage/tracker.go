// Package usage holds the API-reported token accounting. Readers (the
// next turn's Assemble call, the snapshot persistence path) outnumber
// writers (turn tail) heavily, so reads take the read side of an RWMutex.
package usage

import (
	"sync"
	"time"

	"darvin-cowork/backend/internal/agents/protocol"
)

// Tracker stores the most recent per-turn Usage plus session-cumulative
// counters and the model that produced the most recent turn. The two
// views support two distinct consumers:
//
//   - LastUsage returns just the latest Usage so the ContextEngine can
//     prefer API token counts over the local rune/4 estimator.
//   - Snapshot returns the full picture (Last + Total + model + count) so
//     the agent's persistence layer can write a row that survives a
//     process restart, and the renderer can rehydrate the context-window
//     fill on session switch.
type Tracker struct {
	mu        sync.RWMutex
	last      *protocol.Usage
	total     protocol.Usage
	lastModel string
	requests  int
}

// Snapshot is the persistable view of a Tracker at one point in time.
// UpdatedAt is unix milliseconds, set by Snapshot itself so callers do
// not have to plumb a clock through every layer.
type Snapshot struct {
	Last         *protocol.Usage
	Total        *protocol.Usage
	LastModel    string
	RequestCount int
	UpdatedAt    int64
}

// NewTracker returns an empty Tracker whose first reader gets the zero
// Usage — the ContextEngine treats that as "fall back to the local
// estimator".
func NewTracker() *Tracker { return &Tracker{} }

// Record stores the latest API-reported Usage. Replaces the previous
// value wholesale (Last semantics). Kept as a thin wrapper around
// RecordWithModel for callers that don't track the model name.
func (t *Tracker) Record(u protocol.Usage) {
	t.RecordWithModel(u, "")
}

// RecordWithModel is the full write path: covers the latest Usage in
// last, accumulates the per-field deltas into total, and remembers the
// model name so Snapshot can give the renderer the context-window fill
// percentage without a separate registry lookup. The model argument is
// the Deps.ModelName() value passed by the executor; "" is acceptable
// when the caller doesn't know it.
func (t *Tracker) RecordWithModel(u protocol.Usage, model string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	cp := u
	t.last = &cp
	t.total.PromptTokens += u.PromptTokens
	t.total.CompletionTokens += u.CompletionTokens
	t.total.TotalTokens += u.TotalTokens
	t.total.CacheReadTokens += u.CacheReadTokens
	t.total.CacheWriteTokens += u.CacheWriteTokens
	t.total.CacheWrite1hTokens += u.CacheWrite1hTokens
	t.lastModel = model
	t.requests++
}

// Last returns the most recent Usage, or the zero value when nothing
// has been recorded yet.
func (t *Tracker) Last() protocol.Usage {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.last == nil {
		return protocol.Usage{}
	}
	return *t.last
}

// LastModel returns the model name attached to the most recent Record
// call. "" when nothing has been recorded or the caller didn't supply
// the model.
func (t *Tracker) LastModel() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lastModel
}

// Snapshot returns a value-copy of the Tracker's full state at this
// instant. Nil Last / Total indicate an empty tracker (no record yet);
// the persistence layer treats that as "skip the row write".
func (t *Tracker) Snapshot() Snapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.last == nil {
		return Snapshot{UpdatedAt: time.Now().UnixMilli()}
	}
	lastCopy := *t.last
	totalCopy := t.total
	return Snapshot{
		Last:         &lastCopy,
		Total:        &totalCopy,
		LastModel:    t.lastModel,
		RequestCount: t.requests,
		UpdatedAt:    time.Now().UnixMilli(),
	}
}

// Reset clears both the per-turn last and the cumulative totals. Called
// on session rebind so an old session's counters do not bleed into a
// newly bound session.
func (t *Tracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.last = nil
	t.total = protocol.Usage{}
	t.lastModel = ""
	t.requests = 0
}