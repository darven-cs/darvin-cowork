package skills

import (
	"context"
	"testing"

	"darvin-cowork/backend/internal/agents/tool"
)

func TestSkillPluginRegisterEnabledOnly(t *testing.T) {
	reg := NewSkillRegistry()
	src := &stubSource{entries: []*SkillEntry{
		newEntry("web-search", "bundled"),
		newEntry("code-review", "bundled"),
	}}
	if err := reg.Load(context.Background(), []SkillSourceLoader{src}); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetEnabled("code-review", false); err != nil {
		t.Fatal(err)
	}
	toolReg := tool.NewRegistry()
	p := NewSkillPlugin(reg, nil)
	if err := p.Register(toolReg); err != nil {
		t.Fatal(err)
	}
	if toolReg.Get("skill:web-search") == nil {
		t.Error("skill:web-search not registered")
	}
	if toolReg.Get("skill:code-review") != nil {
		t.Error("disabled skill registered")
	}
	entry, ok := toolReg.GetEntry("skill:web-search")
	if !ok {
		t.Fatal("GetEntry(skill:web-search) missing")
	}
	if entry.Kind != tool.KindSkill {
		t.Errorf("Kind = %q, want skill", entry.Kind)
	}
	if entry.Metadata["skillID"] != "web-search" {
		t.Errorf("Metadata[skillID] = %v, want web-search", entry.Metadata["skillID"])
	}
}

func TestSkillPluginUnregister(t *testing.T) {
	reg := NewSkillRegistry()
	src := &stubSource{entries: []*SkillEntry{newEntry("web-search", "bundled")}}
	if err := reg.Load(context.Background(), []SkillSourceLoader{src}); err != nil {
		t.Fatal(err)
	}
	toolReg := tool.NewRegistry()
	p := NewSkillPlugin(reg, nil)
	if err := p.Register(toolReg); err != nil {
		t.Fatal(err)
	}
	if err := p.Unregister(toolReg); err != nil {
		t.Fatal(err)
	}
	if toolReg.Get("skill:web-search") != nil {
		t.Error("skill tool survives Unregister")
	}
}

func TestSkillToolName(t *testing.T) {
	st := &SkillTool{skillEntry: &SkillEntry{ID: "web-search"}}
	if got := st.Name(); got != "skill:web-search" {
		t.Errorf("Name() = %q, want skill:web-search", got)
	}
}

func TestSkillToolParameters(t *testing.T) {
	st := &SkillTool{skillEntry: &SkillEntry{ID: "x"}}
	ps := st.Parameters()
	if ps.Type != "object" {
		t.Errorf("Type = %q, want object", ps.Type)
	}
	if _, ok := ps.Properties["args"]; !ok {
		t.Error("missing args property")
	}
}

func TestSkillToolExecuteNoRunner(t *testing.T) {
	st := &SkillTool{skillEntry: &SkillEntry{ID: "web-search"}}
	res := st.Execute(context.Background(), map[string]any{})
	if res.IsError {
		t.Error("unexpected IsError without runner")
	}
	if res.Metadata["skillID"] != "web-search" {
		t.Errorf("Metadata[skillID] = %v, want web-search", res.Metadata["skillID"])
	}
}

func TestSkillToolExecuteDisabled(t *testing.T) {
	reg := NewSkillRegistry()
	src := &stubSource{entries: []*SkillEntry{newEntry("web-search", "bundled")}}
	if err := reg.Load(context.Background(), []SkillSourceLoader{src}); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetEnabled("web-search", false); err != nil {
		t.Fatal(err)
	}
	runner := NewSkillRunner(reg, tool.NewRegistry())
	st := &SkillTool{skillEntry: &SkillEntry{ID: "web-search"}, runner: runner}
	res := st.Execute(context.Background(), map[string]any{"args": "go"})
	if !res.IsError {
		t.Error("expected IsError for disabled skill")
	}
}

func TestSkillToolExecuteResolved(t *testing.T) {
	reg := NewSkillRegistry()
	src := &stubSource{entries: []*SkillEntry{newEntry("web-search", "bundled")}}
	if err := reg.Load(context.Background(), []SkillSourceLoader{src}); err != nil {
		t.Fatal(err)
	}
	runner := NewSkillRunner(reg, tool.NewRegistry())
	st := &SkillTool{skillEntry: &SkillEntry{ID: "web-search"}, runner: runner}
	res := st.Execute(context.Background(), map[string]any{"args": "go 1.23"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if res.Metadata["skillID"] != "web-search" {
		t.Errorf("Metadata[skillID] = %v, want web-search", res.Metadata["skillID"])
	}
}
