package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"darvin-cowork/backend/internal/agents/llm"
	"darvin-cowork/backend/internal/agents/session"
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
	if err := db.AutoMigrate(&Session{}, &Message{}, &CompactionCheckpoint{}, &SkillSnapshot{}, &AppState{}); err != nil {
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
// are intentionally dropped on the floor (MessageStore will own them).
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

// TestSessionStore_NewFieldsRoundTrip 覆盖统一数据库 spec FR-1：Title /
// ClaudeSessionID 写入后 GetByID 读回；且重新 Save（模拟 prompt 的元数据
// 保存）不会把 title 清掉 —— title 归 RPC handler 管，agent 的 Save 只刷
// metadata。
func TestSessionStore_NewFieldsRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)

	if err := store.Save(ctx, session.NewSession("s1")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.UpdateTitle(ctx, "s1", "我的会话"); err != nil {
		t.Fatalf("UpdateTitle: %v", err)
	}
	claude := "claude-abc"
	if err := store.SetClaudeSessionID(ctx, "s1", &claude); err != nil {
		t.Fatalf("SetClaudeSessionID: %v", err)
	}

	row, err := store.GetByID(ctx, "s1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if row.Title != "我的会话" {
		t.Errorf("Title = %q, want 我的会话", row.Title)
	}
	if row.ClaudeSessionID == nil || *row.ClaudeSessionID != "claude-abc" {
		t.Errorf("ClaudeSessionID = %v, want claude-abc", row.ClaudeSessionID)
	}

	// 重新 Save 不能清掉 title / claude_session_id（Save 保留现有行）。
	if err := store.Save(ctx, session.NewSession("s1")); err != nil {
		t.Fatalf("re-Save: %v", err)
	}
	row2, err := store.GetByID(ctx, "s1")
	if err != nil {
		t.Fatalf("GetByID after re-Save: %v", err)
	}
	if row2.Title != "我的会话" {
		t.Errorf("Title after re-Save = %q, want 我的会话 (Save must not clobber)", row2.Title)
	}
	if row2.ClaudeSessionID == nil || *row2.ClaudeSessionID != "claude-abc" {
		t.Errorf("ClaudeSessionID after re-Save = %v, want claude-abc", row2.ClaudeSessionID)
	}

	// ListAll 也带 Title。
	all, err := store.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 1 || all[0].Title != "我的会话" {
		t.Errorf("ListAll = %+v, want 1 row titled 我的会话", all)
	}
}

// TestSessionStore_TouchUpdatesOnlyUpdatedAt 覆盖 Touch 只刷 updated_at、
// 不碰 title。
func TestSessionStore_TouchUpdatesOnlyUpdatedAt(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)

	if err := store.Save(ctx, session.NewSession("s1")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.UpdateTitle(ctx, "s1", "标题A"); err != nil {
		t.Fatalf("UpdateTitle: %v", err)
	}
	row1, err := store.GetByID(ctx, "s1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	// SQLite DATETIME 是秒精度，至少等 1.1s 保证 touch 后时间前进。
	time.Sleep(1100 * time.Millisecond)
	now := time.Now().UnixMilli()
	if err := store.Touch(ctx, "s1", now); err != nil {
		t.Fatalf("Touch: %v", err)
	}

	row2, err := store.GetByID(ctx, "s1")
	if err != nil {
		t.Fatalf("GetByID after Touch: %v", err)
	}
	if row2.Title != "标题A" {
		t.Errorf("Title after Touch = %q, want 标题A", row2.Title)
	}
	if row2.UpdatedAt.UnixMilli() <= row1.UpdatedAt.UnixMilli() {
		t.Errorf("UpdatedAt did not advance: %v -> %v", row1.UpdatedAt, row2.UpdatedAt)
	}
}

// TestSessionStore_SearchByTitleAndContent 覆盖标题 + 内容两条搜索路径：
// SQL 注入字符不报错，空 query 返空。
func TestSessionStore_SearchByTitleAndContent(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)

	if err := store.Save(ctx, session.NewSession("a")); err != nil {
		t.Fatalf("Save a: %v", err)
	}
	if err := store.Save(ctx, session.NewSession("b")); err != nil {
		t.Fatalf("Save b: %v", err)
	}
	if err := store.UpdateTitle(ctx, "a", "kubernetes 排障"); err != nil {
		t.Fatalf("UpdateTitle a: %v", err)
	}
	if err := store.UpdateTitle(ctx, "b", "日常闲聊"); err != nil {
		t.Fatalf("UpdateTitle b: %v", err)
	}

	byTitle, err := store.SearchByTitle(ctx, "kubernetes")
	if err != nil {
		t.Fatalf("SearchByTitle: %v", err)
	}
	if len(byTitle) != 1 || byTitle[0].ID != "a" {
		t.Errorf("SearchByTitle = %+v, want session a", byTitle)
	}

	// 内容命中：给 session b 写一条含 kubernetes 的消息。
	msgStore := NewSQLiteMessageStore(store.db)
	if err := msgStore.Save(ctx, &MessageRecord{
		ID: "m1", SessionID: "b", Role: "assistant",
		Content: "kubernetes 集群排障", Timestamp: 1000,
	}); err != nil {
		t.Fatalf("Save message: %v", err)
	}
	byContent, err := store.SearchByContent(ctx, "kubernetes", 100)
	if err != nil {
		t.Fatalf("SearchByContent: %v", err)
	}
	if len(byContent) != 1 {
		t.Fatalf("SearchByContent len = %d, want 1", len(byContent))
	}
	if byContent[0].Message.SessionID != "b" {
		t.Errorf("hit SessionID = %q, want b", byContent[0].Message.SessionID)
	}
	if byContent[0].SessionTitle != "日常闲聊" {
		t.Errorf("hit SessionTitle = %q, want 日常闲聊", byContent[0].SessionTitle)
	}

	// SQL 注入字符当作字面量，不报错、不匹配。
	inject := `'; DROP TABLE sessions; --`
	if rows, err := store.SearchByTitle(ctx, inject); err != nil {
		t.Fatalf("SearchByTitle with injection: %v", err)
	} else if len(rows) != 0 {
		t.Errorf("SearchByTitle injection matched %d rows", len(rows))
	}
	if hits, err := store.SearchByContent(ctx, inject, 100); err != nil {
		t.Fatalf("SearchByContent with injection: %v", err)
	} else if len(hits) != 0 {
		t.Errorf("SearchByContent injection matched %d rows", len(hits))
	}

	// 空 query 返空。
	if rows, err := store.SearchByTitle(ctx, ""); err != nil {
		t.Fatalf("SearchByTitle empty: %v", err)
	} else if len(rows) != 0 {
		t.Errorf("SearchByTitle empty returned %d rows, want 0", len(rows))
	}
	if hits, err := store.SearchByContent(ctx, "   ", 100); err != nil {
		t.Fatalf("SearchByContent whitespace: %v", err)
	} else if len(hits) != 0 {
		t.Errorf("SearchByContent whitespace returned %d rows, want 0", len(hits))
	}
}
