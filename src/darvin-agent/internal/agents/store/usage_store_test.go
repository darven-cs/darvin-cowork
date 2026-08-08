// Tests for the usage store.

package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"darvin-cowork/backend/internal/agents/protocol"
)

// newTestUsageStore returns a SQLiteUsageStore backed by a fresh sqlite
// file in t.TempDir() with the SessionUsage model migrated.
func newTestUsageStore(t *testing.T) *SQLiteUsageStore {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "usage.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := db.AutoMigrate(&SessionUsage{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	return NewSQLiteUsageStore(db)
}

func TestSQLiteUsageStoreSaveAndGet(t *testing.T) {
	ctx := context.Background()
	store := newTestUsageStore(t)

	rec := &UsageRecord{
		SessionID:    "sess-1",
		LastModel:    "claude-opus",
		RequestCount: 3,
		UpdatedAt:    1700000000000,
		Last: &protocol.Usage{
			PromptTokens:       4500,
			CompletionTokens:   200,
			TotalTokens:        4700,
			CacheReadTokens:    3200,
			CacheWriteTokens:   50,
			CacheWrite1hTokens: 10,
		},
		Total: &protocol.Usage{
			PromptTokens:     12500,
			CompletionTokens: 600,
			TotalTokens:      13100,
			CacheReadTokens:  9000,
		},
	}
	if err := store.Save(ctx, rec); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Get(ctx, "sess-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SessionID != "sess-1" {
		t.Fatalf("SessionID = %q, want sess-1", got.SessionID)
	}
	if got.LastModel != "claude-opus" {
		t.Fatalf("LastModel = %q, want claude-opus", got.LastModel)
	}
	if got.RequestCount != 3 {
		t.Fatalf("RequestCount = %d, want 3", got.RequestCount)
	}
	if got.Last == nil {
		t.Fatal("Last = nil, want populated")
	}
	if got.Last.PromptTokens != 4500 || got.Last.CompletionTokens != 200 ||
		got.Last.CacheReadTokens != 3200 || got.Last.CacheWriteTokens != 50 ||
		got.Last.CacheWrite1hTokens != 10 {
		t.Fatalf("Last = %+v, want prompt=4500 completion=200 cacheRead=3200 cacheWrite=50 cacheWrite1h=10", got.Last)
	}
	if gotLastUsed := got.Last.PromptTokens + got.Last.CompletionTokens; gotLastUsed != 4700 {
		// sanity-check the renderer-facing field derivation
		t.Fatalf("Last.PromptTokens + Last.CompletionTokens = %d, want 4700", gotLastUsed)
	}
	if got.Total == nil {
		t.Fatal("Total = nil, want populated")
	}
	if got.Total.PromptTokens != 12500 || got.Total.CompletionTokens != 600 || got.Total.CacheReadTokens != 9000 {
		t.Fatalf("Total = %+v, want prompt=12500 completion=600 cacheRead=9000", got.Total)
	}
	if got.UpdatedAt != 1700000000000 {
		t.Fatalf("UpdatedAt = %d, want 1700000000000", got.UpdatedAt)
	}
}

func TestSQLiteUsageStoreSaveUpserts(t *testing.T) {
	ctx := context.Background()
	store := newTestUsageStore(t)

	first := &UsageRecord{
		SessionID:    "sess-1",
		LastModel:    "claude-opus",
		RequestCount: 1,
		UpdatedAt:    1,
		Last:         &protocol.Usage{PromptTokens: 100, CompletionTokens: 10},
	}
	if err := store.Save(ctx, first); err != nil {
		t.Fatalf("Save first: %v", err)
	}

	second := &UsageRecord{
		SessionID:    "sess-1",
		LastModel:    "claude-opus",
		RequestCount: 5,
		UpdatedAt:    2,
		Last:         &protocol.Usage{PromptTokens: 999, CompletionTokens: 99, CacheReadTokens: 50},
	}
	if err := store.Save(ctx, second); err != nil {
		t.Fatalf("Save second: %v", err)
	}

	got, err := store.Get(ctx, "sess-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.RequestCount != 5 {
		t.Fatalf("RequestCount = %d, want 5 (latest save)", got.RequestCount)
	}
	if got.Last == nil || got.Last.PromptTokens != 999 || got.Last.CompletionTokens != 99 || got.Last.CacheReadTokens != 50 {
		t.Fatalf("Last = %+v, want prompt=999 completion=99 cacheRead=50", got.Last)
	}
	if got.UpdatedAt != 2 {
		t.Fatalf("UpdatedAt = %d, want 2", got.UpdatedAt)
	}
}

func TestSQLiteUsageStoreGetMissingReturnsErrRecordNotFound(t *testing.T) {
	ctx := context.Background()
	store := newTestUsageStore(t)

	_, err := store.Get(ctx, "missing-session")
	if err == nil {
		t.Fatal("Get(missing) = nil error, want gorm.ErrRecordNotFound")
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("Get(missing) error = %v, want gorm.ErrRecordNotFound", err)
	}
}

func TestSQLiteUsageStoreDeleteBySession(t *testing.T) {
	ctx := context.Background()
	store := newTestUsageStore(t)

	rec := &UsageRecord{
		SessionID: "sess-del",
		UpdatedAt: 1,
		Last:      &protocol.Usage{PromptTokens: 100},
	}
	if err := store.Save(ctx, rec); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.DeleteBySession(ctx, "sess-del"); err != nil {
		t.Fatalf("DeleteBySession: %v", err)
	}
	_, err := store.Get(ctx, "sess-del")
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("after delete Get error = %v, want gorm.ErrRecordNotFound", err)
	}
}

func TestSQLiteUsageStoreDeleteMissingIsNoOp(t *testing.T) {
	ctx := context.Background()
	store := newTestUsageStore(t)

	if err := store.DeleteBySession(ctx, "never-existed"); err != nil {
		t.Fatalf("DeleteBySession on missing row = %v, want nil", err)
	}
}

func TestSQLiteUsageStoreSaveRejectsNilOrEmpty(t *testing.T) {
	ctx := context.Background()
	store := newTestUsageStore(t)

	if err := store.Save(ctx, nil); err == nil {
		t.Fatal("Save(nil) = nil error, want error")
	}
	if err := store.Save(ctx, &UsageRecord{}); err == nil {
		t.Fatal("Save(empty SessionID) = nil error, want error")
	}
}

func TestSQLiteUsageStoreHandlesNilUsage(t *testing.T) {
	ctx := context.Background()
	store := newTestUsageStore(t)

	rec := &UsageRecord{
		SessionID:    "sess-nil",
		LastModel:    "claude-opus",
		RequestCount: 0,
		UpdatedAt:    1,
		Last:         nil,
		Total:        nil,
	}
	if err := store.Save(ctx, rec); err != nil {
		t.Fatalf("Save with nil usage: %v", err)
	}

	got, err := store.Get(ctx, "sess-nil")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// Get cannot distinguish "saved as nil" from "saved as zero-valued
	// struct" without an extra flag, so the contract is zero-valued fields.
	// The renderer's "no usage yet" check is `lastUsedTokens === 0 &&
	// totalPromptTokens === 0`, which is satisfied here.
	if got.Last == nil {
		t.Fatal("Last = nil, want non-nil zero-valued struct (renderer needs a non-null object)")
	}
	if got.Last.PromptTokens != 0 || got.Last.CompletionTokens != 0 || got.Last.CacheReadTokens != 0 {
		t.Fatalf("Last = %+v, want zero-valued", got.Last)
	}
	if got.Total == nil {
		t.Fatal("Total = nil, want non-nil zero-valued struct")
	}
	if got.Total.PromptTokens != 0 || got.Total.CompletionTokens != 0 || got.Total.CacheReadTokens != 0 {
		t.Fatalf("Total = %+v, want zero-valued", got.Total)
	}
}
