package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// TestAppStateStore_ActiveSessionRoundTrip 覆盖：写入后 GetActiveSession
// 命中；不存在的 key 返空串（空状态）。
func TestAppStateStore_ActiveSessionRoundTrip(t *testing.T) {
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "app_state.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := db.AutoMigrate(&AppState{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	as := NewAppStateStore(db)

	// 未写入前：空状态。
	got, err := as.GetActiveSession(ctx)
	if err != nil {
		t.Fatalf("GetActiveSession empty: %v", err)
	}
	if got != "" {
		t.Errorf("GetActiveSession empty = %q, want \"\"", got)
	}

	if err := as.SetActiveSession(ctx, "session-a"); err != nil {
		t.Fatalf("SetActiveSession: %v", err)
	}
	got, err = as.GetActiveSession(ctx)
	if err != nil {
		t.Fatalf("GetActiveSession: %v", err)
	}
	if got != "session-a" {
		t.Errorf("GetActiveSession = %q, want session-a", got)
	}

	// upsert：覆盖写入。
	if err := as.SetActiveSession(ctx, "session-b"); err != nil {
		t.Fatalf("SetActiveSession b: %v", err)
	}
	got, _ = as.GetActiveSession(ctx)
	if got != "session-b" {
		t.Errorf("GetActiveSession after overwrite = %q, want session-b", got)
	}
}
