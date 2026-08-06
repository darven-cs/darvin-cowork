package tool

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type stubTool struct{ name string }

func (s *stubTool) Name() string                  { return s.name }
func (s *stubTool) Description() string           { return "stub" }
func (s *stubTool) Parameters() json.RawMessage   { return json.RawMessage(`{"type":"object"}`) }
func (s *stubTool) Execute(_ context.Context, _ map[string]any) Result {
	return Result{}
}

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stubTool{name: "alpha"}); err != nil {
		t.Fatal(err)
	}
	if got := r.Get("alpha"); got == nil {
		t.Error("Get(alpha) = nil, want tool")
	}
	if got := r.Get("missing"); got != nil {
		t.Errorf("Get(missing) = %v, want nil", got)
	}
}

func TestRegistryDuplicate(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&stubTool{name: "dup"})
	if err := r.Register(&stubTool{name: "dup"}); !errors.Is(err, ErrAlreadyRegistered) {
		t.Errorf("err = %v, want ErrAlreadyRegistered", err)
	}
}

func TestRegistrySpecsSorted(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&stubTool{name: "c"})
	_ = r.Register(&stubTool{name: "a"})
	_ = r.Register(&stubTool{name: "b"})
	specs := r.Specs()
	if len(specs) != 3 {
		t.Fatalf("len(specs) = %d, want 3", len(specs))
	}
	got := []string{specs[0].Name, specs[1].Name, specs[2].Name}
	want := []string{"a", "b", "c"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("specs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRegistryNilTool(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(nil); err == nil {
		t.Error("Register(nil) should return error")
	}
}

func TestRegistryRegisterToolWithKind(t *testing.T) {
	r := NewRegistry()
	if err := r.RegisterTool(&stubTool{name: "s:skill-a"}, KindSkill, map[string]any{"skillID": "skill-a"}); err != nil {
		t.Fatal(err)
	}
	e, ok := r.GetEntry("s:skill-a")
	if !ok {
		t.Fatal("GetEntry(s:skill-a) missing")
	}
	if e.Kind != KindSkill {
		t.Errorf("Kind = %q, want %q", e.Kind, KindSkill)
	}
	if e.Metadata["skillID"] != "skill-a" {
		t.Errorf("Metadata[skillID] = %v, want skill-a", e.Metadata["skillID"])
	}
	if e.PluginID != "" {
		t.Errorf("PluginID = %q, want empty", e.PluginID)
	}
	// Get still returns the underlying tool.
	if r.Get("s:skill-a") == nil {
		t.Error("Get(s:skill-a) = nil, want tool")
	}
}

func TestRegistryRegisterToolMissingEntry(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.GetEntry("nope"); ok {
		t.Error("GetEntry(nope) ok, want false")
	}
}

func TestRegistryRegisterToolDuplicate(t *testing.T) {
	r := NewRegistry()
	_ = r.RegisterTool(&stubTool{name: "dup"}, KindSkill, nil)
	if err := r.RegisterTool(&stubTool{name: "dup"}, KindMcp, nil); !errors.Is(err, ErrAlreadyRegistered) {
		t.Errorf("err = %v, want ErrAlreadyRegistered", err)
	}
}

func TestRegistryUnregister(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&stubTool{name: "alpha"})
	if err := r.Unregister("alpha"); err != nil {
		t.Fatal(err)
	}
	if r.Get("alpha") != nil {
		t.Error("Get(alpha) after unregister != nil")
	}
	// Unregister is idempotent for missing names.
	if err := r.Unregister("alpha"); err != nil {
		t.Errorf("second unregister err = %v, want nil", err)
	}
}

func TestRegistryUnregisterByPlugin(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&stubTool{name: "builtin"})
	_ = r.RegisterTool(&stubTool{name: "skill__a"}, KindSkill, map[string]any{"pluginID": "skill"})
	_ = r.RegisterTool(&stubTool{name: "skill__b"}, KindSkill, map[string]any{"pluginID": "skill"})
	_ = r.RegisterTool(&stubTool{name: "mcp__c"}, KindMcp, map[string]any{"pluginID": "mcp"})

	if err := r.UnregisterByPlugin("skill"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"skill__a", "skill__b"} {
		if r.Get(name) != nil {
			t.Errorf("Get(%s) after UnregisterByPlugin(skill) != nil", name)
		}
	}
	if r.Get("mcp__c") == nil {
		t.Error("Get(mcp__c) = nil, want surviving tool")
	}
	if r.Get("builtin") == nil {
		t.Error("Get(builtin) = nil, want surviving tool")
	}
}

func TestRegistryListByKind(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&stubTool{name: "shell"})
	_ = r.RegisterTool(&stubTool{name: "skill__z"}, KindSkill, nil)
	_ = r.RegisterTool(&stubTool{name: "skill__a"}, KindSkill, nil)
	_ = r.RegisterTool(&stubTool{name: "mcp__x"}, KindMcp, nil)

	skills := r.ListByKind(KindSkill)
	if len(skills) != 2 {
		t.Fatalf("len(ListByKind(skill)) = %d, want 2", len(skills))
	}
	if skills[0].Tool.Name() != "skill__a" || skills[1].Tool.Name() != "skill__z" {
		t.Errorf("skill order = %q, %q, want skill__a, skill__z", skills[0].Tool.Name(), skills[1].Tool.Name())
	}
	if len(r.ListByKind(KindBuiltIn)) != 1 {
		t.Errorf("len(ListByKind(builtin)) = %d, want 1", len(r.ListByKind(KindBuiltIn)))
	}
	if len(r.ListByKind(KindMcp)) != 1 {
		t.Errorf("len(ListByKind(mcp)) = %d, want 1", len(r.ListByKind(KindMcp)))
	}
}

func TestRegistryRegisterTagsBuiltInKind(t *testing.T) {
	reg, err := NewBuiltins(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	entries := reg.ListByKind(KindBuiltIn)
	if len(entries) != 5 {
		t.Fatalf("len(ListByKind(builtin)) = %d, want 5", len(entries))
	}
	// Every entry from NewBuiltins must be classified as a built-in.
	if n := len(reg.List()); n != 5 {
		t.Errorf("len(List()) = %d, want 5", n)
	}
}
