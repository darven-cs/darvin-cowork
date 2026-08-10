// Tests for SQLiteSubagentStore. Each test uses its own in-memory
// SQLite DSN to keep rows isolated; GORM's Migrator creates the
// subagent_runs table on demand.

package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newTestSubagentStore(t *testing.T) *SQLiteSubagentStore {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "test.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&Subagent{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	return NewSQLiteSubagentStore(db)
}

func TestSubagentStore_InsertAndGet(t *testing.T) {
	s := newTestSubagentStore(t)
	ctx := context.Background()
	row := Subagent{
		ID:          "parent/sub/abc",
		ParentID:    "parent",
		Status:      "running",
		Prompt:      "do thing",
		Description: "thing",
		ScopeJSON:   "[\"read_file\"]",
		Model:       "sonnet",
	}
	if err := s.Insert(ctx, row); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := s.Get(ctx, row.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Prompt != "do thing" || got.Description != "thing" {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
}

func TestSubagentStore_Update(t *testing.T) {
	s := newTestSubagentStore(t)
	ctx := context.Background()
	id := "parent/sub/abc"
	if err := s.Insert(ctx, Subagent{ID: id, ParentID: "parent", Status: "pending"}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	row, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	row.Status = "done"
	row.ResultText = "ok"
	row.EndedAt = time.Now()
	if err := s.Update(ctx, row); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.Status != "done" || got.ResultText != "ok" {
		t.Fatalf("update not persisted: %+v", got)
	}
}

func TestSubagentStore_UpdateMissing(t *testing.T) {
	s := newTestSubagentStore(t)
	err := s.Update(context.Background(), Subagent{ID: "missing/sub/x", Status: "done"})
	if !errors.Is(err, ErrSubagentNotFound) {
		t.Fatalf("want ErrSubagentNotFound, got %v", err)
	}
}

func TestSubagentStore_ListByParent(t *testing.T) {
	s := newTestSubagentStore(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, id := range []string{"a/sub/1", "a/sub/2", "b/sub/3"} {
		row := Subagent{
			ID:        id,
			ParentID:  "a",
			Status:    "running",
			StartedAt: base.Add(time.Duration(i) * time.Minute),
		}
		if id == "b/sub/3" {
			row.ParentID = "b"
		}
		if err := s.Insert(ctx, row); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	rows, err := s.ListByParent(ctx, "a")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows for parent a, got %d", len(rows))
	}
	if rows[0].ID != "a/sub/2" || rows[1].ID != "a/sub/1" {
		t.Fatalf("want started_at desc order, got %s then %s", rows[0].ID, rows[1].ID)
	}
}

func TestSubagentStore_ListStaleRunning(t *testing.T) {
	s := newTestSubagentStore(t)
	ctx := context.Background()
	cutoff := time.Now()
	old := Subagent{ID: "a/sub/old", ParentID: "a", Status: "running", StartedAt: cutoff.Add(-2 * time.Hour)}
	fresh := Subagent{ID: "a/sub/fresh", ParentID: "a", Status: "running", StartedAt: cutoff.Add(time.Minute)}
	done := Subagent{ID: "a/sub/done", ParentID: "a", Status: "done", StartedAt: cutoff.Add(-3 * time.Hour)}
	for _, r := range []Subagent{old, fresh, done} {
		if err := s.Insert(ctx, r); err != nil {
			t.Fatalf("insert %s: %v", r.ID, err)
		}
	}
	rows, err := s.ListStaleRunning(ctx, cutoff)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "a/sub/old" {
		t.Fatalf("want only stale running row, got %+v", rows)
	}
}

func TestSubagentStore_Delete(t *testing.T) {
	s := newTestSubagentStore(t)
	ctx := context.Background()
	if err := s.Insert(ctx, Subagent{ID: "a/sub/x", ParentID: "a", Status: "done"}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.Delete(ctx, "a/sub/x"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err := s.Get(ctx, "a/sub/x")
	if !errors.Is(err, ErrSubagentNotFound) {
		t.Fatalf("want ErrSubagentNotFound, got %v", err)
	}
}

func TestSubagentStore_DeleteByParent(t *testing.T) {
	s := newTestSubagentStore(t)
	ctx := context.Background()
	for _, id := range []string{"a/sub/1", "a/sub/2", "b/sub/3"} {
		row := Subagent{ID: id, ParentID: "a", Status: "done"}
		if id == "b/sub/3" {
			row.ParentID = "b"
		}
		if err := s.Insert(ctx, row); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	if err := s.DeleteByParent(ctx, "a"); err != nil {
		t.Fatalf("delete by parent: %v", err)
	}
	rows, err := s.ListByParent(ctx, "a")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("want 0 rows for a, got %d", len(rows))
	}
	rows, err = s.ListByParent(ctx, "b")
	if err != nil {
		t.Fatalf("list b: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want b untouched, got %d rows", len(rows))
	}
}

func TestSubagentStore_InsertRejectsEmptyID(t *testing.T) {
	s := newTestSubagentStore(t)
	err := s.Insert(context.Background(), Subagent{ParentID: "a", Status: "pending"})
	if err == nil {
		t.Fatalf("want error for empty id")
	}
}
