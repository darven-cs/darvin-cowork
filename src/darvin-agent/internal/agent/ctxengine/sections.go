package ctxengine

// SystemSection is a named block appended to the system prompt at assemble
// time. Sections are sorted by Priority ascending and concatenated.
// v0 has no caller-supplied sections; SystemPromptAddition (Config) is the
// only fixed fragment that lands on the system prompt.
type SystemSection struct {
	Name     string
	Content  string
	Priority int
}

// SkillSummary is the metadata the context engine injects into the
// <available_skills> system section. v0 leaves AvailableSkills empty;
// the Skills spec will populate it.
type SkillSummary struct {
	Name        string
	Description string
}

// Fact is a memory-side fact injected into the system prompt. v0 leaves
// AvailableFacts empty; the Memory spec will populate it.
type Fact struct {
	Content string
	Source  string
}

// MCPServerInfo describes an external MCP server. v0 leaves MCPServers
// empty; the MCP spec will populate it.
type MCPServerInfo struct {
	Name  string
	Tools []string
}
