package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoad_AgentContextEngineFields decodes a YAML fixture that populates
// every ctxengine knob and checks the mapstructure mapping is correct.
func TestLoad_AgentContextEngineFields(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")

	yaml := `
app:
  name: test
  env: test
database:
  sessions_dsn: ":memory:"
log:
  level: info
llm:
  provider: anthropic
agent:
  max_turns: 11
  tool_timeout_ms: 12345
  workdir: "."
  shell_allowlist: [ls, cat]
  event_buffer: 32
  provider_name: anthropic
  model: claude-sonnet-4-5
  instructions: "be terse"
  token_budget: 4096
  compact_tail_keep: 9
  tool_result_max_bytes: 2048
  compact_max_retries: 2
  summarize_max_tokens: 512
  system_prompt_addition: "extra block"
  assembler_enabled: true
`
	if err := writeFile(yamlPath, yaml); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Database.SessionsDSN != ":memory:" {
		t.Errorf("SessionsDSN = %q, want :memory:", cfg.Database.SessionsDSN)
	}
	a := cfg.Agent
	if a.TokenBudget != 4096 {
		t.Errorf("TokenBudget = %d, want 4096", a.TokenBudget)
	}
	if a.CompactTailKeep != 9 {
		t.Errorf("CompactTailKeep = %d, want 9", a.CompactTailKeep)
	}
	if a.ToolResultMaxBytes != 2048 {
		t.Errorf("ToolResultMaxBytes = %d, want 2048", a.ToolResultMaxBytes)
	}
	if a.CompactMaxRetries != 2 {
		t.Errorf("CompactMaxRetries = %d, want 2", a.CompactMaxRetries)
	}
	if a.SummarizeMaxTokens != 512 {
		t.Errorf("SummarizeMaxTokens = %d, want 512", a.SummarizeMaxTokens)
	}
	if a.SystemPromptAddition != "extra block" {
		t.Errorf("SystemPromptAddition = %q, want %q", a.SystemPromptAddition, "extra block")
	}
	if !a.AssemblerEnabled {
		t.Errorf("AssemblerEnabled = false, want true")
	}
	// sanity: pre-existing fields still mapped.
	if a.MaxTurns != 11 {
		t.Errorf("MaxTurns = %d, want 11", a.MaxTurns)
	}
	if a.ToolTimeoutMS != 12345 {
		t.Errorf("ToolTimeoutMS = %d, want 12345", a.ToolTimeoutMS)
	}
}

// TestLoad_AgentContextEngineDefaults confirms that when the YAML omits
// the ctxengine block, every knob falls back to its Go zero value —
// crucially AssemblerEnabled=false (the documented "Go bool zero means
// disabled" semantic). cfg.yaml front-end compensates by emitting
// `assembler_enabled: true` explicitly (see config.yaml).
func TestLoad_AgentContextEngineDefaults(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")

	yaml := `
app: {name: test, env: test}
database: {sessions_dsn: ":memory:"}
log: {level: info}
llm: {provider: anthropic}
agent:
  max_turns: 25
  tool_timeout_ms: 30000
  workdir: "."
  shell_allowlist: [ls]
  event_buffer: 64
  provider_name: anthropic
  model: claude-sonnet-4-5
  instructions: ""
`
	if err := writeFile(yamlPath, yaml); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	a := cfg.Agent
	if a.TokenBudget != 0 || a.CompactTailKeep != 0 || a.ToolResultMaxBytes != 0 ||
		a.CompactMaxRetries != 0 || a.SummarizeMaxTokens != 0 ||
		a.SystemPromptAddition != "" || a.AssemblerEnabled {
		t.Errorf("expected all-zero defaults when ctxengine block omitted, got %+v", a)
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}
