// Package usage holds the most recent API-reported token accounting.
// Readers (the next turn's Assemble call) outnumber writers (turn tail)
// heavily, so reads take the read side of an RWMutex.
package usage

import (
	"sync"

	"darvin-cowork/backend/internal/agents/protocol"
)

// Tracker stores the most recent Usage. Construct via NewTracker.
type Tracker struct {
	mu   sync.RWMutex
	last protocol.Usage
}

// NewTracker returns an empty Tracker whose first reader gets the zero
// Usage — the ContextEngine treats that as "fall back to the local
// estimator".
func NewTracker() *Tracker { return &Tracker{} }

// Record stores the latest API-reported Usage. Replaces the previous value
// wholesale.
func (t *Tracker) Record(u protocol.Usage) {
	t.mu.Lock()
	t.last = u
	t.mu.Unlock()
}

// Last returns the most recent Usage, or the zero value when nothing has
// been recorded yet.
func (t *Tracker) Last() protocol.Usage {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.last
}
