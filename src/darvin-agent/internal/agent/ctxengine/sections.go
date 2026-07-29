package ctxengine

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
// <available_skills> system section. AvailableSkills is empty in the
// current build (no skills system wired in yet).
type SkillSummary struct {
	Name        string
	Description string
}

// Fact is a memory-side fact injected into the system prompt.
// AvailableFacts is empty in the current build (no memory system wired
// in yet).
type Fact struct {
	Content string
	Source  string
}

// MCPServerInfo describes an external MCP server. MCPServers is empty
// in the current build (no MCP client wired in yet).
type MCPServerInfo struct {
	Name  string
	Tools []string
}
