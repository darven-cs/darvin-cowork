// Package config loads and validates the agent's runtime configuration
// via viper.
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

// LLMConfig is the model-provider block consumed by internal/llm.ProviderConfig.
type LLMConfig struct {
	Provider string `mapstructure:"provider"`
	APIKey   string `mapstructure:"api_key"`
	BaseURL  string `mapstructure:"base_url"`
}

// AgentConfig is the runtime configuration for agent.Agent. The
// ContextEngine block (ContextWindow … AssemblerEnabled) is forwarded
// to the auto-constructed DefaultAssembler at agent.New time.
type AgentConfig struct {
	MaxTurns       int      `mapstructure:"max_turns"`
	ToolTimeoutMS  int      `mapstructure:"tool_timeout_ms"`
	Workdir        string   `mapstructure:"workdir"`
	ShellAllowlist []string `mapstructure:"shell_allowlist"`
	EventBuffer    int      `mapstructure:"event_buffer"`
	ProviderName   string   `mapstructure:"provider_name"`
	Model          string   `mapstructure:"model"`
	Instructions   string   `mapstructure:"instructions"`

	// ContextWindow is the LLM's hard cap in tokens; 0 disables auto-compact.
	ContextWindow int `mapstructure:"context_window"`
	// Four ratios driving the soft-notice cascade; 0 falls back to defaults.
	SoftCompactRatio    float64 `mapstructure:"soft_compact_ratio"`
	ToolResultSnipRatio float64 `mapstructure:"tool_result_snip_ratio"`
	CompactRatio        float64 `mapstructure:"compact_ratio"`
	CompactForceRatio   float64 `mapstructure:"compact_force_ratio"`
	// CompactTailTokens is the kept-tail budget; 0 falls back to the ctxengine default.
	CompactTailTokens int `mapstructure:"compact_tail_tokens"`
	// RecentKeep is the message-count floor on the kept tail.
	RecentKeep int `mapstructure:"recent_keep"`
	// ArchiveDir persists fold regions as jsonl before the LLM call; empty disables.
	ArchiveDir string `mapstructure:"archive_dir"`
	// ToolResultMaxBytes truncates oversized tool outputs; 0 disables truncation.
	ToolResultMaxBytes int `mapstructure:"tool_result_max_bytes"`
	// SummarizeMaxTokens caps the summariser LLM call.
	SummarizeMaxTokens int `mapstructure:"summarize_max_tokens"`
	// SystemPromptAddition is appended to the system prompt assembled by DefaultAssembler.
	SystemPromptAddition string `mapstructure:"system_prompt_addition"`
	// AssemblerEnabled flips between the assembler-driven and the legacy prompt path.
	AssemblerEnabled bool `mapstructure:"assembler_enabled"`
	// MemoryFactsLimit clamps the <MEMORY> block FTS top-N; <= 0 disables.
	MemoryFactsLimit int `mapstructure:"memory_facts_limit"`
	// MemoryFactsCacheTTL bounds the per-(sessionID, query) FTS cache; <= 0 disables.
	MemoryFactsCacheTTL string `mapstructure:"memory_facts_cache_ttl"`
	// WebFetchEnabled registers the web_fetch tool; default true.
	WebFetchEnabled bool `mapstructure:"web_fetch_enabled"`
}

var globalConfig *Config

// UserConfigPath returns the OS-aware user-level config path (the
// directory may not exist yet — Load creates it lazily).
func UserConfigPath() (string, error) {
	dir, err := UserDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// UserDataDir returns the OS-aware user-level data directory shared
// with the Electron main process (app.getPath('userData') + "darvin-agent").
func UserDataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "darvin-cowork", "darvin-agent"), nil
}

// ResolveSessionsDSN maps the raw sessions_dsn value to an absolute path:
// empty -> <UserDataDir>/sessions.db; absolute -> verbatim;
// relative -> joined to the process cwd.
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
// overlay. LLM_API_KEY / DARVIN_SESSIONS_DSN env vars win last.
func Load(configPath string) (*Config, error) {
	viper.SetConfigFile(configPath)
	viper.SetConfigType("yaml")
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	if err := viper.BindEnv("llm.api_key", "LLM_API_KEY"); err != nil {
		return nil, fmt.Errorf("bind LLM_API_KEY: %w", err)
	}
	if err := viper.BindEnv("database.sessions_dsn", "DARVIN_SESSIONS_DSN"); err != nil {
		return nil, fmt.Errorf("bind DARVIN_SESSIONS_DSN: %w", err)
	}
	// web_fetch is enabled unless the operator opts out.
	viper.SetDefault("agent.web_fetch_enabled", true)

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
		// os.UserConfigDir failure is non-fatal; fall back to bundled config.
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
// to the user-level config path, creating the parent dir if missing.
// The renderer / Electron side writes the file directly through its own
// yaml helper; this Go helper exists so the round-trip is exercised
// by Go-level tests.
func WriteUserConfig(apiKey, provider, baseURL string) error {
	path, err := UserConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Hand-rolled YAML keeps the helper independent of viper (viper is
	// already loaded with the bundled config; reuse would clobber globals).
	body := fmt.Sprintf(
		"llm:\n  provider: %q\n  api_key: %q\n  base_url: %q\n",
		provider, apiKey, baseURL,
	)
	return os.WriteFile(path, []byte(body), 0o600)
}
