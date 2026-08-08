// Tests for the skill registry.

package skills

import (
	"context"
	"testing"
)

type stubSource struct {
	entries []*SkillEntry
}

func (s *stubSource) LoadAll(_ context.Context) ([]*SkillEntry, error) {
	return s.entries, nil
}

func newEntry(id, source string) *SkillEntry {
	return &SkillEntry{ID: id, Source: SkillSource(source), Enabled: true}
}

func TestRegistryLoadAndGet(t *testing.T) {
	r := NewSkillRegistry()
	src := &stubSource{entries: []*SkillEntry{
		newEntry("alpha", "bundled"),
		newEntry("beta", "project"),
	}}
	if err := r.Load(context.Background(), []SkillSourceLoader{src}); err != nil {
		t.Fatal(err)
	}
	if got, ok := r.Get("alpha"); !ok || got.ID != "alpha" {
		t.Fatalf("Get(alpha) = %v, %v", got, ok)
	}
	if _, ok := r.Get("missing"); ok {
		t.Fatal("Get(missing) = true, want false")
	}
}

func TestRegistryProjectOverridesBundled(t *testing.T) {
	r := NewSkillRegistry()
	bundled := &stubSource{entries: []*SkillEntry{newEntry("alpha", "bundled")}}
	project := &stubSource{entries: []*SkillEntry{newEntry("alpha", "project")}}
	if err := r.Load(context.Background(), []SkillSourceLoader{bundled, project}); err != nil {
		t.Fatal(err)
	}
	got, _ := r.Get("alpha")
	if got.Source != SkillSourceProject {
		t.Fatalf("Source = %q, want project", got.Source)
	}
}

func TestRegistrySetEnabled(t *testing.T) {
	r := NewSkillRegistry()
	src := &stubSource{entries: []*SkillEntry{newEntry("alpha", "bundled")}}
	if err := r.Load(context.Background(), []SkillSourceLoader{src}); err != nil {
		t.Fatal(err)
	}
	if err := r.SetEnabled("alpha", false); err != nil {
		t.Fatal(err)
	}
	for _, e := range r.ListEnabled() {
		if e.ID == "alpha" {
			t.Fatal("alpha should be disabled")
		}
	}
	if err := r.SetEnabled("missing", true); err != ErrSkillNotFound {
		t.Fatalf("err = %v, want ErrSkillNotFound", err)
	}
}

func TestRegistrySnapshotIsSorted(t *testing.T) {
	r := NewSkillRegistry()
	src := &stubSource{entries: []*SkillEntry{
		newEntry("c", "bundled"),
		newEntry("a", "bundled"),
		newEntry("b", "bundled"),
	}}
	if err := r.Load(context.Background(), []SkillSourceLoader{src}); err != nil {
		t.Fatal(err)
	}
	snap := r.Snapshot()
	if len(snap) != 3 || snap[0].ID != "a" || snap[1].ID != "b" || snap[2].ID != "c" {
		t.Fatalf("snapshot order = %+v", []string{snap[0].ID, snap[1].ID, snap[2].ID})
	}
}

func TestRegistryListBySource(t *testing.T) {
	r := NewSkillRegistry()
	src := &stubSource{entries: []*SkillEntry{
		newEntry("a", "bundled"),
		newEntry("b", "project"),
	}}
	if err := r.Load(context.Background(), []SkillSourceLoader{src}); err != nil {
		t.Fatal(err)
	}
	if got := r.ListBySource(SkillSourceBundled); len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("bundled list = %+v", got)
	}
}
