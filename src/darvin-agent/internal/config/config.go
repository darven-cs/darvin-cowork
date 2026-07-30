package config

import (
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
// internal/agent/llm.ProviderConfig. The provider name is matched against
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
// The ContextEngine block (TokenBudget … AssemblerEnabled) is forwarded to
// the auto-constructed DefaultAssembler at agent.New time (see
// specs/features/agent-context-engine §FR-12). AssemblerEnabled defaults
// to true at the YAML front-end so end users get the assembler pipeline;
// Go callers using agent.Config directly get false (the bool zero value)
// and must opt in explicitly. TokenBudget=0 disables the budget check
// (executor takes the legacy d.Session().Messages() path).
type AgentConfig struct {
	MaxTurns       int      `mapstructure:"max_turns"`
	ToolTimeoutMS  int      `mapstructure:"tool_timeout_ms"`
	Workdir        string   `mapstructure:"workdir"`
	ShellAllowlist []string `mapstructure:"shell_allowlist"`
	EventBuffer    int      `mapstructure:"event_buffer"`
	ProviderName   string   `mapstructure:"provider_name"`
	Model          string   `mapstructure:"model"`
	Instructions   string   `mapstructure:"instructions"`

	// --- ContextEngine knobs (ctxengine.Config subset; FR-12) ---

	// TokenBudget is the soft cap for prompt assembly. 0 disables the
	// budget check (assembler still runs, but never triggers Compact).
	TokenBudget int `mapstructure:"token_budget"`

	// CompactTailKeep is the number of trailing messages Compact must
	// preserve verbatim when summarising. Defaults to 6 in
	// ctxengine.NewDefaultAssembler when <=0.
	CompactTailKeep int `mapstructure:"compact_tail_keep"`

	// ToolResultMaxBytes truncates individual tool outputs that exceed
	// this size during assembly. 0 disables truncation.
	ToolResultMaxBytes int `mapstructure:"tool_result_max_bytes"`

	// CompactMaxRetries is how many times Compact may retry the
	// summariser with progressively smaller inputs. 0 = no retries.
	CompactMaxRetries int `mapstructure:"compact_max_retries"`

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
}

var globalConfig *Config

func Load(configPath string) (*Config, error) {
	viper.SetConfigFile(configPath)
	viper.SetConfigType("yaml")

	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		return nil, err
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
