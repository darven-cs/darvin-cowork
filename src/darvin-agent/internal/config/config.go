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
	DSN string `mapstructure:"dsn"`
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
// before the executor gives up. ToolTimeout bounds each tool invocation;
// Workdir anchors the file/shell tool sandbox; ShellAllowlist restricts
// shell commands; EventBuffer is the per-subscriber channel size on the
// event bus.
type AgentConfig struct {
	MaxTurns       int      `mapstructure:"max_turns"`
	ToolTimeoutMS  int      `mapstructure:"tool_timeout_ms"`
	Workdir        string   `mapstructure:"workdir"`
	ShellAllowlist []string `mapstructure:"shell_allowlist"`
	EventBuffer    int      `mapstructure:"event_buffer"`
	ProviderName   string   `mapstructure:"provider_name"`
	Model          string   `mapstructure:"model"`
	Instructions   string   `mapstructure:"instructions"`
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
