package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"darvin-cowork/backend/internal/agent/llm"
	"darvin-cowork/backend/internal/agent/session"
)

// newTestSQLiteStore returns a SQLiteStore backed by a fresh sqlite file
// in t.TempDir() with all 4 GORM models migrated. Each call is isolated
// from sibling tests.
func newTestSQLiteStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "sessions.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := db.AutoMigrate(&Session{}, &Message{}, &CompactionCheckpoint{}, &SkillSnapshot{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	return NewSQLiteStore(db)
}

func TestSQLiteStoreSaveLoad(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)

	src := session.NewSession("s1")
	src.Key = "k1"
	src.AgentID = "a1"
	src.Status = session.StatusSuspended
	if err := store.Save(ctx, src); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Load(ctx, "s1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.ID != "s1" {
		t.Errorf("ID = %q, want s1", got.ID)
	}
	if got.Key != "k1" {
		t.Errorf("Key = %q, want k1", got.Key)
	}
	if got.AgentID != "a1" {
		t.Errorf("AgentID = %q, want a1", got.AgentID)
	}
	if got.Status != session.StatusSuspended {
		t.Errorf("Status = %q, want %q", got.Status, session.StatusSuspended)
	}
	// SQLite DATETIME stores seconds; allow up to 1s drift on round-trip.
	if delta := got.CreatedAt.Sub(src.CreatedAt); delta < -time.Second || delta > time.Second {
		t.Errorf("CreatedAt round-trip drift = %v, want |d| <= 1s", delta)
	}
	if delta := got.UpdatedAt().Sub(src.UpdatedAt()); delta < -time.Second || delta > time.Second {
		t.Errorf("UpdatedAt round-trip drift = %v, want |d| <= 1s", delta)
	}
}

func TestSQLiteStoreListOrderUpdatedDesc(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)

	for _, id := range []string{"a", "b", "c"} {
		if err := store.Save(ctx, session.NewSession(id)); err != nil {
			t.Fatalf("Save %s: %v", id, err)
		}
		// SQLite's autoUpdateTime uses second precision; ensure rows have
		// distinct timestamps.
		time.Sleep(1100 * time.Millisecond)
	}

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("len(list) = %d, want 3", len(list))
	}
	if list[0].ID != "c" || list[1].ID != "b" || list[2].ID != "a" {
		t.Errorf("order = [%s, %s, %s], want [c, b, a] (newest first)",
			list[0].ID, list[1].ID, list[2].ID)
	}
}

func TestSQLiteStoreDeleteIdempotent(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)

	// delete on never-saved id: must not error
	if err := store.Delete(ctx, "never-existed"); err != nil {
		t.Errorf("Delete missing: %v, want nil", err)
	}

	// save → delete → delete: still nil
	if err := store.Save(ctx, session.NewSession("s1")); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, "s1"); err != nil {
		t.Errorf("first Delete: %v", err)
	}
	if err := store.Delete(ctx, "s1"); err != nil {
		t.Errorf("second Delete: %v, want nil (idempotent)", err)
	}
}

func TestSQLiteStoreLoadNotFound(t *testing.T) {
	store := newTestSQLiteStore(t)
	_, err := store.Load(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Load nope = %v, want ErrNotFound", err)
	}
}

func TestSQLiteStoreSaveNil(t *testing.T) {
	store := newTestSQLiteStore(t)
	if err := store.Save(context.Background(), nil); !errors.Is(err, ErrNilSession) {
		t.Errorf("Save(nil) = %v, want ErrNilSession", err)
	}
}

// TestSQLiteStoreSaveDoesNotPersistMessages nails down the P1-1
// contract: SQLiteStore only persists session metadata; messages
// are intentionally dropped on the floor. S4 will add a MessageStore.
func TestSQLiteStoreSaveDoesNotPersistMessages(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)

	src := session.NewSession("s1")
	src.Append(llm.Message{Role: llm.RoleUser, Content: "hello"})
	src.Append(llm.Message{Role: llm.RoleAssistant, Content: "world"})
	if err := store.Save(ctx, src); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Load(ctx, "s1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Len() != 0 {
		t.Errorf("Load.Len = %d, want 0 (SQLiteStore must not persist messages)", got.Len())
	}
}

func TestSQLiteStoreSaveReplace(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)

	src := session.NewSession("s1")
	src.Key = "v1"
	if err := store.Save(ctx, src); err != nil {
		t.Fatal(err)
	}
	src.Key = "v2"
	src.Status = session.StatusArchived
	if err := store.Save(ctx, src); err != nil {
		t.Fatal(err)
	}

	got, err := store.Load(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != "v2" {
		t.Errorf("Key after second Save = %q, want v2", got.Key)
	}
	if got.Status != session.StatusArchived {
		t.Errorf("Status after second Save = %q, want %q", got.Status, session.StatusArchived)
	}
}
