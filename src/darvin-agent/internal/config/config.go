package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	App      AppConfig      `mapstructure:"app"`
	Database DatabaseConfig `mapstructure:"database"`
	Log      LogConfig      `mapstructure:"log"`
	LLM      LLMConfig      `mapstructure:"llm"`
	Agent    AgentConfig    `mapstructure:"agent"`
}

type AppConfig struct {
	Name string `mapstructure:"name"`
	Env  string `mapstructure:"env"`
}

type DatabaseConfig struct {
	SessionsDSN string `mapstructure:"sessions_dsn"`
}

type LogConfig struct {
	Level      string `mapstructure:"level"`
	Encoding   string `mapstructure:"encoding"`
	Output     string `mapstructure:"output"`
	Filename   string `mapstructure:"filename"`
	MaxSize    int    `mapstructure:"max_size"`
	MaxBackups int    `mapstructure:"max_backups"`
	MaxAge     int    `mapstructure:"max_age"`
}

// LLMConfig is the model-provider configuration block consumed by
// internal/llm.ProviderConfig. The provider name is matched against
// the registered factories in the llm package.
type LLMConfig struct {
	Provider string `mapstructure:"provider"`
	APIKey   string `mapstructure:"api_key"`
	BaseURL  string `mapstructure:"base_url"`
}

// AgentConfig is the runtime configuration for the agent.Agent itself.
// MaxTurns caps how many LLM/tool iterations one user prompt may produce
// before the executor gives up. ToolTimeoutMS bounds each tool invocation
// (converted to time.Duration when handed to agent.New); Workdir anchors
// the file/shell tool sandbox; ShellAllowlist restricts shell commands;
// EventBuffer is the per-subscriber channel size on the event bus.
//
// The ContextEngine block (ContextWindow … AssemblerEnabled) is forwarded to
// the auto-constructed DefaultAssembler at agent.New time. AssemblerEnabled
// defaults to true at the YAML front-end so end users get the assembler
// pipeline; Go callers using agent.Config directly get false (the bool zero
// value) and must opt in explicitly. ContextWindow=0 disables the entire
// auto-compact pipeline (the FR-1 closed semantic that mirrors Reasonix
// maybeCompact:86).
type AgentConfig struct {
	MaxTurns       int      `mapstructure:"max_turns"`
	ToolTimeoutMS  int      `mapstructure:"tool_timeout_ms"`
	Workdir        string   `mapstructure:"workdir"`
	ShellAllowlist []string `mapstructure:"shell_allowlist"`
	EventBuffer    int      `mapstructure:"event_buffer"`
	ProviderName   string   `mapstructure:"provider_name"`
	Model          string   `mapstructure:"model"`
	Instructions   string   `mapstructure:"instructions"`

	// ContextWindow is the LLM's hard context cap in tokens. 0
	// disables the entire auto-compact pipeline (the assembler still
	// runs the system-section / tool-truncation passes, but never
	// triggers Compact).
	ContextWindow int `mapstructure:"context_window"`

	// Four threshold ratios driving the FR-2 cascade:
	//   SoftCompactRatio    — 50% soft notice
	//   ToolResultSnipRatio — 60% stale tool result snip
	//   CompactRatio        — 80% trigger LLM summarise
	//   CompactForceRatio   — 90% force summarise (bypass foldEconomics)
	// 0 falls back to the Reasonix default (0.5/0.6/0.8/0.9).
	SoftCompactRatio    float64 `mapstructure:"soft_compact_ratio"`
	ToolResultSnipRatio float64 `mapstructure:"tool_result_snip_ratio"`
	CompactRatio        float64 `mapstructure:"compact_ratio"`
	CompactForceRatio   float64 `mapstructure:"compact_force_ratio"`

	// CompactTailTokens is the token budget the kept tail fits under.
	// Mirrors Reasonix defaultTailTokens (16384). 0 falls back to the
	// ctxengine default.
	CompactTailTokens int `mapstructure:"compact_tail_tokens"`

	// RecentKeep is the message-count floor on the kept tail —
	// compaction never keeps fewer than this many recent messages even
	// if the token budget allows more. Mirrors Reasonix minRecentKeep (2).
	RecentKeep int `mapstructure:"recent_keep"`

	// ArchiveDir, when non-empty, causes Compact to persist the fold
	// region as a timestamped jsonl before the LLM call. Empty disables
	// archive (the most common configuration in fresh installs).
	// Best-effort: write failures emit a Notice but do not block
	// compaction.
	ArchiveDir string `mapstructure:"archive_dir"`

	// ToolResultMaxBytes truncates individual tool outputs that exceed
	// this size during assembly. 0 disables truncation.
	ToolResultMaxBytes int `mapstructure:"tool_result_max_bytes"`

	// SummarizeMaxTokens is the cap passed to the summariser LLM call.
	// 0 lets the provider pick its own default.
	SummarizeMaxTokens int `mapstructure:"summarize_max_tokens"`

	// SystemPromptAddition is an extra string appended to the system
	// prompt assembled by DefaultAssembler. Empty = no addition.
	SystemPromptAddition string `mapstructure:"system_prompt_addition"`

	// AssemblerEnabled flips the executor between the assembler-driven
	// prompt construction path and the legacy
	// session.Messages() fallback. The cfg.yaml default is true.
	AssemblerEnabled bool `mapstructure:"assembler_enabled"`

	// MemoryFactsLimit clamps the <MEMORY> system-block FTS top-N. <= 0
	// disables the MEMORY block. Wired through to
	// ctxengine.Config.MemoryFactsLimit and agent.Config.MemoryFactsLimit.
	MemoryFactsLimit int `mapstructure:"memory_facts_limit"`

	// MemoryFactsCacheTTL bounds the per-(sessionID, query) FTS cache.
	// <= 0 disables caching — every Assemble re-queries FTS. Parsed
	// into time.Duration via time.ParseDuration in agent.New.
	MemoryFactsCacheTTL string `mapstructure:"memory_facts_cache_ttl"`
}

var globalConfig *Config

// UserConfigPath returns the OS-aware user-level config path. The
// directory may not exist yet — Load creates it lazily so a fresh
// install without an `api_key` written by the Settings UI still works.
//
// Linux:   $XDG_CONFIG_HOME/darvin-cowork/darvin-agent/config.yaml
//
//	or ~/.config/darvin-cowork/darvin-agent/config.yaml
//
// macOS:   ~/Library/Application Support/darvin-cowork/darvin-agent/config.yaml
// Windows: %APPDATA%\darvin-cowork\darvin-agent\config.yaml
func UserConfigPath() (string, error) {
	dir, err := UserDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// UserDataDir returns the OS-aware user-level data directory shared
// with the Electron main process (matches app.getPath('userData') +
// "darvin-agent"). Both sides land on the same absolute path so the
// SQLite files and config.yaml stay together, while Electron's own
// Chromium cache lives one level up in the parent darvin-cowork/.
func UserDataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "darvin-cowork", "darvin-agent"), nil
}

// ResolveSessionsDSN maps the raw `database.sessions_dsn` value to an
// absolute path the SQLite driver can open:
//
//   - empty   -> <UserDataDir>/sessions.db (default; same dir as config.yaml)
//   - absolute -> returned verbatim (Electron injects this form via
//     DARVIN_SESSIONS_DSN so the Electron side decides the location)
//   - relative -> resolved against the process cwd (preserved for tests
//     and explicit overrides; `go run` and the test suite both use this)
//
// Returning an absolute path keeps database.Init free of path logic.
func ResolveSessionsDSN(dsn string) (string, error) {
	if dsn == "" {
		dir, err := UserDataDir()
		if err != nil {
			return "", fmt.Errorf("resolve default sessions dir: %w", err)
		}
		return filepath.Join(dir, "sessions.db"), nil
	}
	if filepath.IsAbs(dsn) {
		return dsn, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve cwd for relative dsn: %w", err)
	}
	return filepath.Join(cwd, dsn), nil
}

// Load reads the bundled config first, then merges any user-level
// overlay on top. The order is critical: the
// overlay must win so the Settings UI can override a placeholder
// bundled api_key without editing the source tree.
//
// LLM_API_KEY from the environment always wins last — viper's
// AutomaticEnv binds it to the `llm.api_key` mapstructure key.
func Load(configPath string) (*Config, error) {
	viper.SetConfigFile(configPath)
	viper.SetConfigType("yaml")
	viper.AutomaticEnv()
	// BindEnv is what makes Unmarshal honour env vars: AutomaticEnv alone
	// only intercepts viper.Get calls, not the recursive walk that
	// Unmarshal performs. BindEnv("llm.api_key", "LLM_API_KEY") maps the
	// exact leaf to the env var name (with SetEnvKeyReplacer handling
	// the standard `.`→`_` rule for any future leaf).
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	if err := viper.BindEnv("llm.api_key", "LLM_API_KEY"); err != nil {
		return nil, fmt.Errorf("bind LLM_API_KEY: %w", err)
	}
	if err := viper.BindEnv("database.sessions_dsn", "DARVIN_SESSIONS_DSN"); err != nil {
		return nil, fmt.Errorf("bind DARVIN_SESSIONS_DSN: %w", err)
	}

	if err := viper.ReadInConfig(); err != nil {
		return nil, err
	}

	if userPath, err := UserConfigPath(); err == nil {
		if _, statErr := os.Stat(userPath); statErr == nil {
			viper.SetConfigFile(userPath)
			if err := viper.MergeInConfig(); err != nil {
				return nil, fmt.Errorf("merge user config %s: %w", userPath, err)
			}
		}
	} else {
		// os.UserConfigDir failure is non-fatal; log via stderr so a
		// user without HOME on a minimal Linux still gets the bundled
		// config without a hard failure.
		fmt.Fprintf(os.Stderr, "config: user config dir unavailable: %v\n", err)
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	globalConfig = &cfg
	return &cfg, nil
}

func Get() *Config {
	if globalConfig == nil {
		panic("config not initialized, call Load first")
	}
	return globalConfig
}

// WriteUserConfig serialises llm.api_key + llm.provider + llm.base_url
// to the user-level config path, creating the parent directory if
// missing. Called from the Settings UI flow when the user saves an
// LLM provider config. The renderer / Electron side
// writes the file directly through its own yaml helper; this Go
// helper exists so the round-trip is exercised by Go-level tests.
//
// Errors fall through verbatim; the caller decides whether to surface
// them to the user.
func WriteUserConfig(apiKey, provider, baseURL string) error {
	path, err := UserConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Hand-rolled YAML keeps the helper independent of viper (viper is
	// already loaded with the bundled config; reuse would clobber
	// globals). The schema is the only shape WriteUserConfig writes —
	// anything else stays in the bundled config.
	body := fmt.Sprintf(
		"llm:\n  provider: %q\n  api_key: %q\n  base_url: %q\n",
		provider, apiKey, baseURL,
	)
	return os.WriteFile(path, []byte(body), 0o600)
}
