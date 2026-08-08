// Package usage holds the API-reported token accounting. Reads outnumber
// writes so reads take the read side of an RWMutex.
package usage

import (
	"sync"
	"time"

	"darvin-cowork/backend/internal/agents/protocol"
)

// Tracker stores the most recent per-turn Usage plus session-cumulative
// counters and the model that produced the most recent turn. LastUsage
// supports the ContextEngine; Snapshot supports persistence + rehydrate.
type Tracker struct {
	mu        sync.RWMutex
	last      *protocol.Usage
	total     protocol.Usage
	lastModel string
	requests  int
}

// Snapshot is the persistable view of a Tracker at one point in time.
// UpdatedAt is unix ms, set by Snapshot itself.
type Snapshot struct {
	Last         *protocol.Usage
	Total        *protocol.Usage
	LastModel    string
	RequestCount int
	UpdatedAt    int64
}

// NewTracker returns an empty Tracker.
func NewTracker() *Tracker { return &Tracker{} }

// Record stores the latest API-reported Usage (replaces previous).
func (t *Tracker) Record(u protocol.Usage) {
	t.RecordWithModel(u, "")
}

// RecordWithModel is the full write path: covers the latest Usage in
// last, accumulates per-field deltas into total, and remembers the
// model name. The model arg comes from Deps.ModelName(); "" is allowed.
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

// Last returns the most recent Usage, or zero when nothing recorded.
func (t *Tracker) Last() protocol.Usage {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.last == nil {
		return protocol.Usage{}
	}
	return *t.last
}

// LastModel returns the model name attached to the most recent Record.
func (t *Tracker) LastModel() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lastModel
}

// Snapshot returns a value-copy of the Tracker's full state. Nil Last /
// Total indicate an empty tracker; the persistence layer skips the row.
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

// Reset clears both last and totals (called on session rebind so old
// counters do not bleed into a newly bound session).
func (t *Tracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.last = nil
	t.total = protocol.Usage{}
	t.lastModel = ""
	t.requests = 0
}