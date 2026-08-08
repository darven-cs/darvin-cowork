// Token usage tracking for the agent: record, snapshot, and persist
// to SQLite at run tail.

package agent

import (
	"context"

	"darvin-cowork/backend/internal/agents/protocol"
	"darvin-cowork/backend/internal/agents/store"

	"go.uber.org/zap"
)

// IsRunning reports whether Agent.Run is in progress (the gateway refuses
// manual compaction while a turn executes).
func (a *Agent) IsRunning() bool { return a.controller.IsRunning() }

// RecordUsage stores the API-reported Usage for the just-finished turn,
// tagged with the model for context-window selection on rehydrate.
func (a *Agent) RecordUsage(u protocol.Usage, model string) {
	a.tracker.RecordWithModel(u, model)
}

// LastUsage returns the most recent API-reported Usage; zero before any
// turn completes (the ContextEngine falls back to the local estimator).
func (a *Agent) LastUsage() protocol.Usage { return a.tracker.Last() }

// UsageSnapshot returns the Tracker's full state (last + cumulative +
// model) for the persistence layer's Run-tail write.
func (a *Agent) UsageSnapshot() Snapshot { return a.tracker.Snapshot() }

// persistUsageSnapshot writes the current Tracker snapshot to SQLite
// (Run-tail; failures are warn-and-continue). The percent / window reuse
// the context_usage numbers so the renderer rehydrates without recompute.
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
