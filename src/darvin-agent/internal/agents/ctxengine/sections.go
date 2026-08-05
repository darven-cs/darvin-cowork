package ctxengine

import (
	"fmt"
	"strings"
)

// SystemSection is a named block appended to the system prompt at assemble
// time. Sections are sorted by Priority ascending and concatenated.
// No caller-supplied sections today; SystemPromptAddition (Config) is the
// only fixed fragment that lands on the system prompt.
type SystemSection struct {
	Name     string
	Content  string
	Priority int
}

// SkillSummary is the metadata the context engine injects into the
// <available_skills> system section.
type SkillSummary struct {
	Name        string
	Description string
}

// Fact is a memory-side fact injected into the system prompt.
// AvailableFacts is empty until the memory subsystem is wired in.
type Fact struct {
	Content string
	Source  string
}

// MCPServerInfo describes an external MCP server.
type MCPServerInfo struct {
	Name  string
	Tools []string
}

// renderAvailableSkillsSection formats the <available_skills> block the LLM
// sees. Empty input yields a "(none registered)" stub so the LLM does not
// mistake an absent block for a permission grant.
func renderAvailableSkillsSection(skills []SkillSummary) string {
	const max = 20
	if len(skills) == 0 {
		return "<available_skills>\n(none registered)\n</available_skills>"
	}
	var b strings.Builder
	b.WriteString("<available_skills>\n")
	limit := len(skills)
	if limit > max {
		limit = max
	}
	for i := 0; i < limit; i++ {
		s := skills[i]
		name := strings.TrimSpace(s.Name)
		if name == "" {
			continue
		}
		desc := strings.TrimSpace(s.Description)
		if desc == "" {
			fmt.Fprintf(&b, "  - %s\n", name)
		} else {
			fmt.Fprintf(&b, "  - %s: %s\n", name, desc)
		}
	}
	if len(skills) > max {
		fmt.Fprintf(&b, "  (+%d more, use list_skills to see all)\n", len(skills)-max)
	}
	b.WriteString("</available_skills>")
	return b.String()
}

// renderAvailableFactsSection formats the <available_facts> block. Same
// stub policy as skills.
func renderAvailableFactsSection(facts []Fact) string {
	const max = 20
	if len(facts) == 0 {
		return "<available_facts>\n(none registered)\n</available_facts>"
	}
	var b strings.Builder
	b.WriteString("<available_facts>\n")
	limit := len(facts)
	if limit > max {
		limit = max
	}
	for i := 0; i < limit; i++ {
		f := facts[i]
		content := strings.TrimSpace(f.Content)
		if content == "" {
			continue
		}
		if f.Source != "" {
			fmt.Fprintf(&b, "  - (%s) %s\n", f.Source, content)
		} else {
			fmt.Fprintf(&b, "  - %s\n", content)
		}
	}
	if len(facts) > max {
		fmt.Fprintf(&b, "  (+%d more)\n", len(facts)-max)
	}
	b.WriteString("</available_facts>")
	return b.String()
}

// renderAvailableMCPSection formats the <available_mcp> block.
func renderAvailableMCPSection(servers []MCPServerInfo) string {
	const max = 20
	if len(servers) == 0 {
		return "<available_mcp>\n(none registered)\n</available_mcp>"
	}
	var b strings.Builder
	b.WriteString("<available_mcp>\n")
	limit := len(servers)
	if limit > max {
		limit = max
	}
	for i := 0; i < limit; i++ {
		s := servers[i]
		name := strings.TrimSpace(s.Name)
		if name == "" {
			continue
		}
		fmt.Fprintf(&b, "  - %s (%d tools)\n", name, len(s.Tools))
	}
	if len(servers) > max {
		fmt.Fprintf(&b, "  (+%d more)\n", len(servers)-max)
	}
	b.WriteString("</available_mcp>")
	return b.String()
}

// BuiltInSections returns the standard set of sections the assembler always
// attaches to the system prompt. Empty registries are skipped so callers
// that have not wired in skills / memory / MCP see no change to the
// system addition. Callers can append their own SystemSections on top;
// priorities keep the order stable.
func BuiltInSections(skills []SkillSummary, facts []Fact, servers []MCPServerInfo) []SystemSection {
	var out []SystemSection
	if len(skills) > 0 {
		out = append(out, SystemSection{Name: "available_skills", Content: renderAvailableSkillsSection(skills), Priority: 100})
	}
	if len(facts) > 0 {
		out = append(out, SystemSection{Name: "available_facts", Content: renderAvailableFactsSection(facts), Priority: 110})
	}
	if len(servers) > 0 {
		out = append(out, SystemSection{Name: "available_mcp", Content: renderAvailableMCPSection(servers), Priority: 120})
	}
	return out
}
