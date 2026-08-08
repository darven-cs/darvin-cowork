// Tests for the assembler lifecycle and ingest hooks.

package ctxengine

import (
	"context"
	"errors"
	"testing"
	"time"

	"darvin-cowork/backend/internal/llm"
)

// TestIngest_ContextCancelled verifies that a cancelled context surfaces
// as a Success=false IngestResult with the ctx.Err() in Warnings.
func TestIngest_ContextCancelled(t *testing.T) {
	a := newTestAssembler()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res := a.Ingest(ctx, IngestParams{SessionID: "s1", Message: msg("hi")})
	if res.Success {
		t.Errorf("Success = true, want false (cancelled ctx)")
	}
	if len(res.Warnings) != 1 || !errors.Is(errFromWarning(res.Warnings[0]), context.Canceled) {
		t.Errorf("Warnings = %v, want [context.Canceled]", res.Warnings)
	}
}

// TestIngestBatch_ContextCancelled mirrors the single-message variant.
func TestIngestBatch_ContextCancelled(t *testing.T) {
	a := newTestAssembler()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res := a.IngestBatch(ctx, IngestBatchParams{SessionID: "s1", Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}})
	if res.Success {
		t.Errorf("Success = true, want false (cancelled ctx)")
	}
}

// TestIngestBatch_EmptyMessagesNoOp verifies that a batch with zero
// messages short-circuits without recording a timestamp.
func TestIngestBatch_EmptyMessagesNoOp(t *testing.T) {
	a := newTestAssembler()
	res := a.IngestBatch(context.Background(), IngestBatchParams{SessionID: "s1"})
	if !res.Success {
		t.Errorf("Success = false, want true (empty batch no-op)")
	}
	if res.TokensProcessed != 0 {
		t.Errorf("TokensProcessed = %d, want 0 (no messages)", res.TokensProcessed)
	}
	if !a.LastIngestAt("s1").IsZero() {
		t.Errorf("lastIngestAt should remain zero on empty batch no-op")
	}
}

// TestIngest_LastIngestAtRecorded verifies that a successful Ingest stamps
// the per-session timestamp; the accessor (test-only helper) exposes the
// map so the assertion stays independent of internal field renames.
func TestIngest_LastIngestAtRecorded(t *testing.T) {
	a := newTestAssembler()
	before := time.Now()
	res := a.Ingest(context.Background(), IngestParams{SessionID: "s1", Message: msg("hi")})
	if !res.Success {
		t.Fatalf("Ingest: Success = false, got %+v", res)
	}
	got := a.LastIngestAt("s1")
	if got.Before(before) {
		t.Errorf("lastIngestAt = %v, want >= %v", got, before)
	}
}

// TestLifecycle_ContextCancelled verifies Bootstrap / Maintain / Dispose
// surface ctx.Err() (rather than swallowing it) so callers can detect
// shutdown mid-call.
func TestLifecycle_ContextCancelled(t *testing.T) {
	a := newTestAssembler()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := a.Bootstrap(ctx, BootstrapParams{SessionID: "s1"}); !errors.Is(err, context.Canceled) {
		t.Errorf("Bootstrap: err = %v, want context.Canceled", err)
	}
	if err := a.Maintain(ctx, MaintainParams{SessionID: "s1"}); !errors.Is(err, context.Canceled) {
		t.Errorf("Maintain: err = %v, want context.Canceled", err)
	}
	if err := a.Dispose(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Dispose: err = %v, want context.Canceled", err)
	}
}

// TestAfterTurn_ContextCancelled mirrors the lifecycle ctx-cancel check.
func TestAfterTurn_ContextCancelled(t *testing.T) {
	a := newTestAssembler()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := a.AfterTurn(ctx, AfterTurnParams{SessionID: "s1", TurnIndex: 1}); !errors.Is(err, context.Canceled) {
		t.Errorf("AfterTurn: err = %v, want context.Canceled", err)
	}
}
