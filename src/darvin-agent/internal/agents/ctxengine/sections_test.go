// Tests for system-prompt section rendering.

package ctxengine

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agents/protocol"
)

func newAssemblerForSectionsTest(t *testing.T) *DefaultAssembler {
	t.Helper()
	return NewDefaultAssembler(Config{ToolResultMaxBytes: 0}, fakeDeps{logger: zap.NewNop()})
}

func testCtx() context.Context { return context.Background() }

func TestRenderAvailableSkillsEmpty(t *testing.T) {
	got := renderAvailableSkillsSection(nil)
	if got != "<available_skills>\n(none registered)\n</available_skills>" {
		t.Fatalf("got = %q, want stub", got)
	}
}

func TestRenderAvailableSkillsNonEmpty(t *testing.T) {
	got := renderAvailableSkillsSection([]SkillSummary{
		{Name: "docx", Description: "read/write Word docs"},
		{Name: "code-review", Description: "review a diff"},
		{Name: "testing", Description: "run tests"},
	})
	for _, want := range []string{"- docx: read/write Word docs", "- code-review: review a diff", "- testing: run tests"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
	if !strings.HasPrefix(got, "<available_skills>") || !strings.HasSuffix(got, "</available_skills>") {
		t.Fatalf("output missing wrapper tags:\n%s", got)
	}
}

func TestRenderAvailableSkillsTruncates(t *testing.T) {
	var skills []SkillSummary
	for i := 0; i < 25; i++ {
		skills = append(skills, SkillSummary{Name: "s" + string(rune('a'+i)), Description: "x"})
	}
	got := renderAvailableSkillsSection(skills)
	if !strings.Contains(got, "(+5 more, use list_skills to see all)") {
		t.Fatalf("truncation marker missing:\n%s", got)
	}
}

func TestRenderAvailableSkillsSkipsBlankName(t *testing.T) {
	got := renderAvailableSkillsSection([]SkillSummary{
		{Name: "", Description: "ignored"},
		{Name: "real", Description: "kept"},
	})
	if strings.Contains(got, "ignored") {
		t.Fatalf("blank-name entry was not skipped:\n%s", got)
	}
	if !strings.Contains(got, "- real: kept") {
		t.Fatalf("real entry missing:\n%s", got)
	}
}

func TestRenderAvailableFacts(t *testing.T) {
	if got := renderAvailableFactsSection(nil); got != "<available_facts>\n(none registered)\n</available_facts>" {
		t.Fatalf("empty facts got %q", got)
	}
	got := renderAvailableFactsSection([]Fact{
		{Content: "alpha", Source: "memory"},
		{Content: "beta"},
	})
	if !strings.Contains(got, "- (memory) alpha") {
		t.Fatalf("source-tagged fact missing:\n%s", got)
	}
	if !strings.Contains(got, "- beta") {
		t.Fatalf("sourceless fact missing:\n%s", got)
	}
}

func TestRenderAvailableMCP(t *testing.T) {
	if got := renderAvailableMCPSection(nil); got != "<available_mcp>\n(none registered)\n</available_mcp>" {
		t.Fatalf("empty mcp got %q", got)
	}
	got := renderAvailableMCPSection([]MCPServerInfo{
		{Name: "filesystem", Tools: []string{"read", "write"}},
		{Name: "github", Tools: []string{"search", "create_issue", "list_prs"}},
	})
	if !strings.Contains(got, "- filesystem (2 tools)") {
		t.Fatalf("filesystem row missing:\n%s", got)
	}
	if !strings.Contains(got, "- github (3 tools)") {
		t.Fatalf("github row missing:\n%s", got)
	}
}

func TestBuiltInSectionsSkipsEmpty(t *testing.T) {
	if got := BuiltInSections(nil, nil, nil); len(got) != 0 {
		t.Fatalf("BuiltInSections(nil) len = %d, want 0", len(got))
	}
	all := BuiltInSections(
		[]SkillSummary{{Name: "x"}},
		[]Fact{{Content: "y"}},
		[]MCPServerInfo{{Name: "z"}},
	)
	if len(all) != 3 {
		t.Fatalf("BuiltInSections(all) len = %d, want 3", len(all))
	}
	want := []string{"available_skills", "available_facts", "available_mcp"}
	for i, s := range all {
		if s.Name != want[i] {
			t.Fatalf("section[%d].Name = %q, want %q", i, s.Name, want[i])
		}
	}
}

func TestAssembleAttachesBuiltInSections(t *testing.T) {
	a := newAssemblerForSectionsTest(t)
	got := a.Assemble(testCtx(), AssembleParams{
		SessionID:       "s",
		Messages:        []protocol.Message{{Role: protocol.RoleUser, Content: "hi"}},
		AvailableSkills: []SkillSummary{{Name: "docx", Description: "x"}},
		MCPServers:      []MCPServerInfo{{Name: "fs", Tools: []string{"read"}}},
	})
	if !strings.Contains(got.SystemAddition, "<available_skills>") {
		t.Fatalf("SystemAddition missing skills block:\n%s", got.SystemAddition)
	}
	if !strings.Contains(got.SystemAddition, "- docx: x") {
		t.Fatalf("SystemAddition missing docx entry:\n%s", got.SystemAddition)
	}
	if !strings.Contains(got.SystemAddition, "<available_mcp>") {
		t.Fatalf("SystemAddition missing mcp block:\n%s", got.SystemAddition)
	}
	if !strings.Contains(got.SystemAddition, "- fs (1 tools)") {
		t.Fatalf("SystemAddition missing fs entry:\n%s", got.SystemAddition)
	}
}

func TestIdentitySection(t *testing.T) {
	sec, ok := IdentitySection("x")
	if !ok {
		t.Fatalf("ok = false for non-blank content")
	}
	if sec.Content != "<IDENTITY>\nx\n</IDENTITY>" {
		t.Fatalf("Content = %q", sec.Content)
	}
	if sec.Priority != 31 {
		t.Fatalf("Priority = %d, want 31", sec.Priority)
	}
}

func TestIdentitySectionBlank(t *testing.T) {
	if _, ok := IdentitySection(""); ok {
		t.Fatalf("empty content ok = true, want false")
	}
	if _, ok := IdentitySection("   "); ok {
		t.Fatalf("whitespace content ok = true, want false")
	}
}
