// Tests for the GORM-backed agent store and the preset seed data.

package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newTestAgentStore(t *testing.T) *SQLiteAgentStore {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "agents.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := db.AutoMigrate(&Agent{}, &Workspace{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	return NewSQLiteAgentStore(db)
}

func TestPresetSeedContent(t *testing.T) {
	presets := PresetSeed()
	if len(presets) != 9 {
		t.Fatalf("PresetSeed has %d entries, want 9", len(presets))
	}
	seen := map[string]bool{}
	for _, a := range presets {
		if seen[a.ID] {
			t.Errorf("duplicate preset id %q", a.ID)
		}
		seen[a.ID] = true
		if a.Source != "preset" {
			t.Errorf("preset %s source = %q, want preset", a.ID, a.Source)
		}
		if a.PresetID != a.ID {
			t.Errorf("preset %s presetID = %q, want same as ID", a.ID, a.PresetID)
		}
		if a.Icon == "" || a.Color == "" {
			t.Errorf("preset %s missing icon/color (%q/%q)", a.ID, a.Icon, a.Color)
		}
		if a.Name == "" || a.NameEn == "" {
			t.Errorf("preset %s missing name/nameEn", a.ID)
		}
		if a.SystemPrompt == "" || a.Identity == "" {
			t.Errorf("preset %s missing systemPrompt/identity", a.ID)
		}
		if DecodeSkillIDs(a.SkillIDs) == nil {
			t.Errorf("preset %s skillIDs not valid JSON array", a.ID)
		}
	}
	if !PresetIDs()["preset-main"] {
		t.Error("PresetIDs missing preset-main")
	}
	main := MainAgentSeed()
	if main.ID != "preset-main" || main.Name == "" || main.SystemPrompt == "" || main.Identity == "" {
		t.Errorf("MainAgentSeed incomplete: %+v", main)
	}
}

func TestSeedPresetsIdempotent(t *testing.T) {
	ctx := context.Background()
	agents := newTestAgentStore(t)

	if err := agents.SeedPresets(ctx, "ws1"); err != nil {
		t.Fatalf("SeedPresets: %v", err)
	}
	if err := agents.SeedPresets(ctx, "ws1"); err != nil {
		t.Fatalf("SeedPresets second run: %v", err)
	}
	rows, err := agents.ListByWorkspace(ctx, "ws1")
	if err != nil {
		t.Fatalf("ListByWorkspace: %v", err)
	}
	if len(rows) != 9 {
		t.Fatalf("ws1 has %d agent rows after double seed, want 9", len(rows))
	}

	// A second workspace seeds its own copies — the (workspace_id,
	// preset_id) key, not preset_id alone, is what dedupes.
	if err := agents.SeedPresets(ctx, "ws2"); err != nil {
		t.Fatalf("SeedPresets ws2: %v", err)
	}
	rows2, err := agents.ListByWorkspace(ctx, "ws2")
	if err != nil {
		t.Fatalf("ListByWorkspace ws2: %v", err)
	}
	if len(rows2) != 9 {
		t.Fatalf("ws2 has %d agent rows, want 9 (cross-workspace dedupe bug)", len(rows2))
	}
}

func TestEnsureDefaultForWorkspace(t *testing.T) {
	ctx := context.Background()
	agents := newTestAgentStore(t)

	first, err := agents.EnsureDefaultForWorkspace(ctx, "ws1")
	if err != nil {
		t.Fatalf("EnsureDefaultForWorkspace: %v", err)
	}
	if !first.IsDefault {
		t.Error("created default agent has is_default=false")
	}
	if first.PresetID != "preset-main" {
		t.Errorf("default agent presetID = %q, want preset-main", first.PresetID)
	}
	if first.ID != "ws1/preset-main" {
		t.Errorf("default agent id = %q, want ws1/preset-main", first.ID)
	}

	second, err := agents.EnsureDefaultForWorkspace(ctx, "ws1")
	if err != nil {
		t.Fatalf("EnsureDefaultForWorkspace second run: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("second run created a new default (%q vs %q)", second.ID, first.ID)
	}

	got, err := agents.GetDefaultForWorkspace(ctx, "ws1")
	if err != nil {
		t.Fatalf("GetDefaultForWorkspace: %v", err)
	}
	if got.ID != first.ID {
		t.Errorf("GetDefaultForWorkspace = %q, want %q", got.ID, first.ID)
	}
}

func TestAgentStoreCRUDRoundtrip(t *testing.T) {
	ctx := context.Background()
	agents := newTestAgentStore(t)

	created, err := agents.Create(ctx, Agent{
		Name: "自定义", WorkspaceID: "ws1",
		SystemPrompt: "p", Identity: "i", Icon: "qa-doc", Color: "cyan",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("Create did not mint an id")
	}
	if created.Source != "user" {
		t.Errorf("default source = %q, want user", created.Source)
	}

	got, err := agents.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "自定义" || got.SystemPrompt != "p" || got.WorkspaceID != "ws1" {
		t.Errorf("roundtrip mismatch: %+v", got)
	}

	got.Name = "改名"
	if err := agents.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	updated, _ := agents.GetByID(ctx, created.ID)
	if updated.Name != "改名" {
		t.Errorf("Update did not persist name: %q", updated.Name)
	}

	// Workspace scoping: the same agent id is invisible from another
	// workspace listing.
	if err := agents.SeedPresets(ctx, "other"); err != nil {
		t.Fatalf("SeedPresets: %v", err)
	}
	otherRows, _ := agents.ListByWorkspace(ctx, "other")
	for _, r := range otherRows {
		if r.ID == created.ID {
			t.Error("user agent leaked into other workspace listing")
		}
	}

	if err := agents.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := agents.GetByID(ctx, created.ID); err == nil {
		t.Error("deleted agent still readable")
	}
}
