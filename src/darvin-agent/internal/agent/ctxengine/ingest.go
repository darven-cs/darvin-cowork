package ctxengine

import (
	"context"
	"time"
)

// Ingest records the wall-clock time of the call (v0 = no-op fact
// extraction). Real fact extraction is wired in the Memory spec.
func (a *DefaultAssembler) Ingest(ctx context.Context, p IngestParams) IngestResult {
	if err := ctx.Err(); err != nil {
		return IngestResult{Success: false, Warnings: []string{err.Error()}}
	}
	if p.SessionID == "" {
		return IngestResult{Success: true}
	}
	a.mu.Lock()
	a.lastIngestAt[p.SessionID] = time.Now()
	a.mu.Unlock()
	return IngestResult{Success: true, TokensProcessed: 0}
}

// IngestBatch records the wall-clock time of the last call (v0 = no-op
// fact extraction). Real batch extraction is wired in the Memory spec.
func (a *DefaultAssembler) IngestBatch(ctx context.Context, p IngestBatchParams) IngestResult {
	if err := ctx.Err(); err != nil {
		return IngestResult{Success: false, Warnings: []string{err.Error()}}
	}
	if p.SessionID == "" || len(p.Messages) == 0 {
		return IngestResult{Success: true}
	}
	a.mu.Lock()
	a.lastIngestAt[p.SessionID] = time.Now()
	a.mu.Unlock()
	return IngestResult{Success: true, TokensProcessed: len(p.Messages)}
}
