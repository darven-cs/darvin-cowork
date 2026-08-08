package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
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
  context_window: 32000
  recent_keep: 3
  tool_result_max_bytes: 2048
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
	if a.ContextWindow != 32000 {
		t.Errorf("ContextWindow = %d, want 32000", a.ContextWindow)
	}
	if a.RecentKeep != 3 {
		t.Errorf("RecentKeep = %d, want 3", a.RecentKeep)
	}
	if a.ToolResultMaxBytes != 2048 {
		t.Errorf("ToolResultMaxBytes = %d, want 2048", a.ToolResultMaxBytes)
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
	if a.ContextWindow != 0 || a.RecentKeep != 0 || a.ToolResultMaxBytes != 0 ||
		a.SummarizeMaxTokens != 0 ||
		a.SystemPromptAddition != "" || a.AssemblerEnabled {
		t.Errorf("expected all-zero defaults when ctxengine block omitted, got %+v", a)
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

// resetViper clears viper's globals between tests so each Load call
// starts from a clean slate. Without this, the second test in a
// sequence inherits keys from the first.
func resetViper() {
	viper.Reset()
	globalConfig = nil
}

// TestLoadReadsBundled is the baseline: Load(configPath) unmarshals the
// bundled yaml and the empty `llm.api_key` flows through. HOME /
// XDG_CONFIG_HOME are redirected to a tmp dir with no overlay, so this
// matches a fresh install.
func TestLoadReadsBundled(t *testing.T) {
	defer resetViper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := writeFile(cfgPath, `
app:
  name: darvin-cowork
  env: test
database:
  sessions_dsn: ./sessions.db
log:
  level: info
  encoding: console
  output: stderr
llm:
  provider: anthropic
  api_key: ""
  base_url: ""
agent:
  max_turns: 5
  event_buffer: 8
  provider_name: anthropic
  model: claude-sonnet-4-5
  instructions: ""
`); err != nil {
		t.Fatalf("write bundled config: %v", err)
	}
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	t.Setenv("APPDATA", "")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LLM.APIKey != "" {
		t.Errorf("LLM.APIKey = %q, want empty (bundled is empty)", cfg.LLM.APIKey)
	}
	if cfg.Agent.MaxTurns != 5 {
		t.Errorf("Agent.MaxTurns = %d, want 5", cfg.Agent.MaxTurns)
	}
}

// TestLoadMergesUserOverlay is FR-1.2: an existing user-level
// darvin-cowork/config.yaml wins over the bundled file. The bundled
// api_key is empty; the overlay sets it to "user-key-123".
func TestLoadMergesUserOverlay(t *testing.T) {
	defer resetViper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := writeFile(cfgPath, `
llm:
  provider: anthropic
  api_key: ""
  base_url: ""
`); err != nil {
		t.Fatalf("write bundled: %v", err)
	}

	// Place user-level config at the location os.UserConfigDir returns.
	home := filepath.Join(dir, "home")
	xdg := filepath.Join(home, ".config")
	userCfgDir := filepath.Join(xdg, "darvin-cowork", "darvin-agent")
	if err := os.MkdirAll(userCfgDir, 0o755); err != nil {
		t.Fatalf("MkdirAll user cfg dir: %v", err)
	}
	userCfgPath := filepath.Join(userCfgDir, "config.yaml")
	if err := writeFile(userCfgPath, `
llm:
  api_key: "user-key-123"
`); err != nil {
		t.Fatalf("write user overlay: %v", err)
	}

	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("APPDATA", "")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LLM.APIKey != "user-key-123" {
		t.Errorf("LLM.APIKey = %q, want user-key-123", cfg.LLM.APIKey)
	}
	if cfg.LLM.Provider != "anthropic" {
		t.Errorf("LLM.Provider = %q, want anthropic (from bundled)", cfg.LLM.Provider)
	}
}

// TestLoadEnvVarOverrides confirms AutomaticEnv binds LLM_API_KEY to
// llm.api_key — when the env var is set, it wins over both bundled
// and user-overlay values.
func TestLoadEnvVarOverrides(t *testing.T) {
	defer resetViper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := writeFile(cfgPath, `
llm:
  provider: anthropic
  api_key: "bundled"
  base_url: ""
`); err != nil {
		t.Fatalf("write bundled: %v", err)
	}
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	t.Setenv("LLM_API_KEY", "env-key-xyz")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LLM.APIKey != "env-key-xyz" {
		t.Errorf("LLM.APIKey = %q, want env-key-xyz", cfg.LLM.APIKey)
	}
}

// TestUserConfigPathIsOSAware makes sure the helper returns a path
// under os.UserConfigDir() — the exact platform location is exercised
// by the os package itself. The leaf path mirrors the Electron side:
// .../darvin-cowork/darvin-agent/config.yaml.
func TestUserConfigPathIsOSAware(t *testing.T) {
	path, err := UserConfigPath()
	if err != nil {
		t.Fatalf("UserConfigPath: %v", err)
	}
	if filepath.Base(path) != "config.yaml" {
		t.Errorf("file name = %q, want config.yaml", filepath.Base(path))
	}
	if filepath.Base(filepath.Dir(path)) != "darvin-agent" {
		t.Errorf("dir name = %q, want darvin-agent", filepath.Base(filepath.Dir(path)))
	}
	if filepath.Base(filepath.Dir(filepath.Dir(path))) != "darvin-cowork" {
		t.Errorf("parent dir = %q, want darvin-cowork", filepath.Base(filepath.Dir(filepath.Dir(path))))
	}
}

// TestWriteUserConfigRoundTrip writes via WriteUserConfig and reads
// back via Load — verifies the two helpers agree on the file
// location / shape.
func TestWriteUserConfigRoundTrip(t *testing.T) {
	defer resetViper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	t.Setenv("APPDATA", "")

	if err := WriteUserConfig("roundtrip-key", "anthropic", ""); err != nil {
		t.Fatalf("WriteUserConfig: %v", err)
	}

	// Bundled stub is empty api_key.
	bundledPath := filepath.Join(dir, "bundled.yaml")
	if err := writeFile(bundledPath, `
llm:
  provider: anthropic
  api_key: ""
  base_url: ""
`); err != nil {
		t.Fatalf("write bundled: %v", err)
	}

	cfg, err := Load(bundledPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LLM.APIKey != "roundtrip-key" {
		t.Errorf("LLM.APIKey = %q, want roundtrip-key", cfg.LLM.APIKey)
	}
}

// TestLoadMissingUserOverlayIsGraceful covers the fresh-install path:
// the user-level config doesn't exist yet, so the bundled file is
// the only source. Load must not return an error.
func TestLoadMissingUserOverlayIsGraceful(t *testing.T) {
	defer resetViper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := writeFile(cfgPath, `
llm:
  provider: anthropic
  api_key: ""
  base_url: ""
`); err != nil {
		t.Fatalf("write bundled: %v", err)
	}
	t.Setenv("HOME", filepath.Join(dir, "missing-home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "missing-xdg"))
	t.Setenv("APPDATA", "")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load with no overlay: %v", err)
	}
	if cfg.LLM.Provider != "anthropic" {
		t.Errorf("LLM.Provider = %q, want anthropic", cfg.LLM.Provider)
	}
}

// TestResolveSessionsDSN_EmptyDefaultsToUserDataDir covers the
// empty-value branch: ResolveSessionsDSN should return
// <UserConfigDir>/darvin-cowork/darvin-agent/sessions.db regardless of
// cwd so Electron + Go land on the same absolute path.
func TestResolveSessionsDSN_EmptyDefaultsToUserDataDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	t.Setenv("APPDATA", "")

	got, err := ResolveSessionsDSN("")
	if err != nil {
		t.Fatalf("ResolveSessionsDSN empty: %v", err)
	}
	want := filepath.Join(dir, ".config", "darvin-cowork", "darvin-agent", "sessions.db")
	if got != want {
		t.Errorf("ResolveSessionsDSN(\"\") = %q, want %q", got, want)
	}
}

// TestResolveSessionsDSN_AbsolutePassthrough covers the absolute
// branch: Electron injects DARVIN_SESSIONS_DSN, which viper's
// BindEnv surface eventually hands to ResolveSessionsDSN already as
// an absolute path; it must be returned verbatim.
func TestResolveSessionsDSN_AbsolutePassthrough(t *testing.T) {
	abs := "/var/lib/darvin-cowork/sessions.db"
	got, err := ResolveSessionsDSN(abs)
	if err != nil {
		t.Fatalf("ResolveSessionsDSN abs: %v", err)
	}
	if got != abs {
		t.Errorf("ResolveSessionsDSN(%q) = %q, want verbatim", abs, got)
	}
}

// TestResolveSessionsDSN_RelativeExpandsFromCwd covers the
// relative-path branch preserved for `go run` and test fixtures.
// The result must be <cwd>/<dsn> and must change with cwd.
func TestResolveSessionsDSN_RelativeExpandsFromCwd(t *testing.T) {
	dir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	got, err := ResolveSessionsDSN("./sessions.db")
	if err != nil {
		t.Fatalf("ResolveSessionsDSN rel: %v", err)
	}
	want := filepath.Join(dir, "sessions.db")
	if got != want {
		t.Errorf("ResolveSessionsDSN(./sessions.db) = %q, want %q", got, want)
	}
}
