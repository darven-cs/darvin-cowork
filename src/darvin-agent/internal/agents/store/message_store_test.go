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

// newTestMessageStore returns a SQLiteMessageStore backed by a fresh
// sqlite file in t.TempDir() with all 4 GORM models migrated. Each call
// is isolated from sibling tests.
func newTestMessageStore(t *testing.T) *SQLiteMessageStore {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "messages.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := db.AutoMigrate(&Session{}, &Message{}, &CompactionCheckpoint{}, &SkillSnapshot{}, &AppState{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	return NewSQLiteMessageStore(db)
}

func TestSQLiteMessageStoreSaveAndList(t *testing.T) {
	ctx := context.Background()
	store := newTestMessageStore(t)

	now := time.Now().UnixMilli()
	records := []MessageRecord{
		{ID: "m1", SessionID: "s1", Role: "user", Content: "hi", Timestamp: now},
		{ID: "m2", SessionID: "s1", Role: "assistant", Content: "hello", Timestamp: now + 100},
		{ID: "m3", SessionID: "s1", Role: "user", Content: "ping", Timestamp: now + 200},
	}
	for _, r := range records {
		if err := store.Save(ctx, &r); err != nil {
			t.Fatalf("Save %s: %v", r.ID, err)
		}
	}

	got, err := store.List(ctx, "s1", 0, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}
	// List is timestamp-asc; m1 < m2 < m3.
	if got[0].ID != "m1" || got[1].ID != "m2" || got[2].ID != "m3" {
		t.Errorf("order = [%s, %s, %s], want [m1, m2, m3]",
			got[0].ID, got[1].ID, got[2].ID)
	}
	if got[1].Content != "hello" {
		t.Errorf("got[1].Content = %q, want hello", got[1].Content)
	}
}

func TestSQLiteMessageStoreListFiltersBySession(t *testing.T) {
	ctx := context.Background()
	store := newTestMessageStore(t)

	for _, r := range []MessageRecord{
		{ID: "a1", SessionID: "s1", Role: "user", Content: "x", Timestamp: 1},
		{ID: "a2", SessionID: "s2", Role: "user", Content: "y", Timestamp: 2},
		{ID: "a3", SessionID: "s1", Role: "user", Content: "z", Timestamp: 3},
	} {
		if err := store.Save(ctx, &r); err != nil {
			t.Fatalf("Save %s: %v", r.ID, err)
		}
	}

	got, err := store.List(ctx, "s1", 0, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	for _, r := range got {
		if r.SessionID != "s1" {
			t.Errorf("List leaked sessionID = %q, want s1", r.SessionID)
		}
	}
}

func TestSQLiteMessageStoreListPagination(t *testing.T) {
	ctx := context.Background()
	store := newTestMessageStore(t)

	for i := 0; i < 5; i++ {
		rec := MessageRecord{
			ID:        "p" + string(rune('0'+i)),
			SessionID: "s1", Role: "user", Content: "x",
			Timestamp: int64(i + 1),
		}
		if err := store.Save(ctx, &rec); err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}

	first, err := store.List(ctx, "s1", 2, 0)
	if err != nil {
		t.Fatalf("List page1: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("page1 len = %d, want 2", len(first))
	}
	if first[0].ID != "p0" || first[1].ID != "p1" {
		t.Errorf("page1 order = [%s, %s], want [p0, p1]", first[0].ID, first[1].ID)
	}

	second, err := store.List(ctx, "s1", 2, 2)
	if err != nil {
		t.Fatalf("List page2: %v", err)
	}
	if len(second) != 2 {
		t.Fatalf("page2 len = %d, want 2", len(second))
	}
	if second[0].ID != "p2" || second[1].ID != "p3" {
		t.Errorf("page2 order = [%s, %s], want [p2, p3]", second[0].ID, second[1].ID)
	}
}

func TestSQLiteMessageStoreCount(t *testing.T) {
	ctx := context.Background()
	store := newTestMessageStore(t)

	for _, r := range []MessageRecord{
		{ID: "a", SessionID: "s1", Role: "user", Content: "x", Timestamp: 1},
		{ID: "b", SessionID: "s1", Role: "user", Content: "x", Timestamp: 2},
		{ID: "c", SessionID: "s2", Role: "user", Content: "x", Timestamp: 3},
	} {
		if err := store.Save(ctx, &r); err != nil {
			t.Fatalf("Save %s: %v", r.ID, err)
		}
	}

	n, err := store.Count(ctx, "s1")
	if err != nil {
		t.Fatalf("Count s1: %v", err)
	}
	if n != 2 {
		t.Errorf("Count s1 = %d, want 2", n)
	}

	n, err = store.Count(ctx, "s2")
	if err != nil {
		t.Fatalf("Count s2: %v", err)
	}
	if n != 1 {
		t.Errorf("Count s2 = %d, want 1", n)
	}

	n, err = store.Count(ctx, "no-such-session")
	if err != nil {
		t.Fatalf("Count empty: %v", err)
	}
	if n != 0 {
		t.Errorf("Count empty = %d, want 0", n)
	}
}

func TestSQLiteMessageStoreSaveRejectsNil(t *testing.T) {
	store := newTestMessageStore(t)
	if err := store.Save(context.Background(), nil); err == nil {
		t.Error("Save(nil) = nil, want error")
	}
}

func TestSQLiteMessageStoreSaveRejectsEmptyFields(t *testing.T) {
	ctx := context.Background()
	store := newTestMessageStore(t)

	if err := store.Save(ctx, &MessageRecord{SessionID: "s1"}); err == nil {
		t.Error("Save(empty ID) = nil, want error")
	}
	if err := store.Save(ctx, &MessageRecord{ID: "x"}); err == nil {
		t.Error("Save(empty SessionID) = nil, want error")
	}
}

func TestSQLiteMessageStoreSaveUpsert(t *testing.T) {
	ctx := context.Background()
	store := newTestMessageStore(t)

	rec := &MessageRecord{
		ID: "u1", SessionID: "s1", Role: "assistant", Content: "v1", Timestamp: 1,
	}
	if err := store.Save(ctx, rec); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	rec.Content = "v2"
	if err := store.Save(ctx, rec); err != nil {
		t.Fatalf("second Save: %v", err)
	}

	got, err := store.List(ctx, "s1", 0, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1 (Save upserted)", len(got))
	}
	if got[0].Content != "v2" {
		t.Errorf("Content = %q, want v2", got[0].Content)
	}
}

// TestSQLiteMessageStoreErrorMapping is a sanity check that errors
// flow back through the API unchanged — the dispatch hook relies on
// being able to log them verbatim.
func TestSQLiteMessageStoreErrorMapping(t *testing.T) {
	store := newTestMessageStore(t)
	// Saving with an empty ID is the cheapest way to force a non-nil error.
	err := store.Save(context.Background(), &MessageRecord{SessionID: "s1"})
	if err == nil {
		t.Fatal("Save(empty ID) = nil, want error")
	}
	// The dispatch path uses errors.Is / log only — there is no exported
	// sentinel today, but we assert the error is non-nil so callers can
	// log it.
	if errors.Is(err, context.Canceled) {
		t.Errorf("Save returned a context error: %v", err)
	}
}

// TestMessageStore_AppendContent 覆盖 streaming 累加：同 id 多次 append
// 顺序追加；空 delta 不报错；不存在的 id 是 no-op 不报错。
func TestMessageStore_AppendContent(t *testing.T) {
	ctx := context.Background()
	store := newTestMessageStore(t)

	rec := &MessageRecord{
		ID: "m1", SessionID: "s1", Role: "assistant", Content: "Hel", Timestamp: 1,
	}
	if err := store.Save(ctx, rec); err != nil {
		t.Fatalf("Save: %v", err)
	}

	for _, d := range []string{"lo", " ", "world"} {
		if err := store.AppendContent(ctx, "m1", d); err != nil {
			t.Fatalf("AppendContent %q: %v", d, err)
		}
	}
	got, err := store.List(ctx, "s1", 0, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got[0].Content != "Hello world" {
		t.Errorf("Content = %q, want %q", got[0].Content, "Hello world")
	}

	// 空 delta 是 no-op。
	if err := store.AppendContent(ctx, "m1", ""); err != nil {
		t.Fatalf("AppendContent empty: %v", err)
	}
	got2, _ := store.List(ctx, "s1", 0, 0)
	if got2[0].Content != "Hello world" {
		t.Errorf("Content after empty append = %q, want unchanged", got2[0].Content)
	}

	// 不存在的 id：no-op，不报错。
	if err := store.AppendContent(ctx, "never-existed", "x"); err != nil {
		t.Fatalf("AppendContent missing id: %v", err)
	}
}

// TestMessageStore_MarkDoneAndError 覆盖封口写入：MarkDone 只标 done；
// MarkError 标 done + error 字段并读回。
func TestMessageStore_MarkDoneAndError(t *testing.T) {
	ctx := context.Background()
	store := newTestMessageStore(t)

	if err := store.Save(ctx, &MessageRecord{
		ID: "m1", SessionID: "s1", Role: "assistant", Content: "partial", Timestamp: 1,
	}); err != nil {
		t.Fatalf("Save m1: %v", err)
	}
	if err := store.Save(ctx, &MessageRecord{
		ID: "m2", SessionID: "s1", Role: "assistant", Content: "oops", Timestamp: 2,
	}); err != nil {
		t.Fatalf("Save m2: %v", err)
	}

	if err := store.MarkDone(ctx, "m1"); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}
	if err := store.MarkError(ctx, "m2", "llm stream failed"); err != nil {
		t.Fatalf("MarkError: %v", err)
	}

	got, err := store.List(ctx, "s1", 0, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !got[0].Done {
		t.Errorf("m1 Done = false, want true")
	}
	if got[0].Error != nil {
		t.Errorf("m1 Error = %v, want nil", got[0].Error)
	}
	if !got[1].Done {
		t.Errorf("m2 Done = false, want true")
	}
	if got[1].Error == nil || *got[1].Error != "llm stream failed" {
		t.Errorf("m2 Error = %v, want llm stream failed", got[1].Error)
	}
}