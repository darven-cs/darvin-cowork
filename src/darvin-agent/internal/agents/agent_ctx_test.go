// Tests for the agent's context-assembly wiring (ctxengine / skills / mcp).

package agent

import (
	"testing"

	"darvin-cowork/backend/internal/agents/ctxengine"
	"darvin-cowork/backend/internal/agents/protocol"
	"darvin-cowork/backend/internal/agents/session"
)

type stubSkills struct {
	entries []SkillEntry
}

func (s stubSkills) ListEnabled() []SkillEntry {
	out := make([]SkillEntry, 0, len(s.entries))
	for _, e := range s.entries {
		if e.Enabled {
			out = append(out, e)
		}
	}
	return out
}

type stubMcp struct {
	servers []McpServerSummary
}

func (m stubMcp) ListServers() []McpServerSummary { return m.servers }

func newAgentWithRegistries(t *testing.T, skills SkillsLister, mcp McpLister) *Agent {
	t.Helper()
	a, err := New(NewAgentConfig{
		Session:  session.NewSession("s"),
		Provider: &scriptedProvider{},
		Tools:    mustToolRegistry(),
		Skills:   skills,
		Mcp:      mcp,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

func mustToolRegistry() protocol.ToolRegistry { return &toolRegistryShim{} }

func TestSkillSummariesNilRegistry(t *testing.T) {
	a := newAgentWithRegistries(t, nil, nil)
	if got := a.SkillSummaries(); got != nil {
		t.Fatalf("SkillSummaries() = %v, want nil", got)
	}
	if got := a.McpServers(); got != nil {
		t.Fatalf("McpServers() = %v, want nil", got)
	}
}

func TestSkillSummariesPopulated(t *testing.T) {
	skills := stubSkills{entries: []SkillEntry{
		{ID: "docx", Name: "docx", Description: "Word docs", Enabled: true},
		{ID: "testing", Name: "testing", Description: "run tests", Enabled: true},
		{ID: "disabled", Name: "disabled", Description: "off", Enabled: false},
	}}
	a := newAgentWithRegistries(t, skills, nil)
	got := a.SkillSummaries()
	if len(got) != 2 {
		t.Fatalf("SkillSummaries len = %d, want 2 (disabled dropped)", len(got))
	}
	want := map[string]string{"docx": "Word docs", "testing": "run tests"}
	for _, s := range got {
		if s.Description != want[s.Name] {
			t.Fatalf("Skill %q description = %q, want %q", s.Name, s.Description, want[s.Name])
		}
	}
}

func TestSkillSummariesFallsBackToID(t *testing.T) {
	skills := stubSkills{entries: []SkillEntry{
		{ID: "docx", Name: "", Description: "x", Enabled: true},
	}}
	a := newAgentWithRegistries(t, skills, nil)
	got := a.SkillSummaries()
	if len(got) != 1 || got[0].Name != "docx" {
		t.Fatalf("SkillSummaries = %+v, want name=\"docx\" from ID fallback", got)
	}
}

func TestMcpServersPopulated(t *testing.T) {
	mcp := stubMcp{servers: []McpServerSummary{
		{ServerID: "fs", Name: "filesystem", Tools: []string{"read", "write"}},
		{ServerID: "gh", Name: "github", Tools: []string{"search"}},
	}}
	a := newAgentWithRegistries(t, nil, mcp)
	got := a.McpServers()
	if len(got) != 2 {
		t.Fatalf("McpServers len = %d, want 2", len(got))
	}
	if got[0].Name != "filesystem" || len(got[0].Tools) != 2 {
		t.Fatalf("McpServers[0] = %+v", got[0])
	}
	if got[1].Name != "github" {
		t.Fatalf("McpServers[1] = %+v", got[1])
	}
}

func TestSkillSummariesNotCoupledToCtxengine(t *testing.T) {
	// Boundary sanity: the agent returns ctxengine.SkillSummary values
	// but its registry interfaces are owned by the agent package.
	_ = []ctxengine.SkillSummary{}
	var _ SkillsLister = stubSkills{}
	var _ McpLister = stubMcp{}
}

// toolRegistryShim is a no-op registry so the NewAgentConfig compiles in
// tests that exercise SkillSummaries / McpServers without touching a
// real tool set.
type toolRegistryShim struct{}

func (*toolRegistryShim) Get(string) protocol.Tool                { return nil }
func (*toolRegistryShim) GetEntry(string) (*protocol.Entry, bool) { return nil, false }
func (*toolRegistryShim) Specs() []protocol.ToolSpec              { return nil }
func (*toolRegistryShim) Names() []string                         { return nil }
func (*toolRegistryShim) List() []*protocol.Entry                 { return nil }
func (*toolRegistryShim) SetGrantedReads([]string)                {}
func (*toolRegistryShim) ApprovePath(string)                      {}
func (*toolRegistryShim) EvaluatePermission(string, map[string]any) protocol.PermissionEval {
	return protocol.PermissionEval{}
}
func (*toolRegistryShim) ScopedForSkill([]string) protocol.ToolRegistry {
	return &toolRegistryShim{}
}
