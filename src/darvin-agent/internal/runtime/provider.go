// Constructs the LLM providers used by the runtime.

package runtime

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/config"
	"darvin-cowork/backend/internal/llm"

	// Blank imports trigger the providers' init() which registers them with
	// llm.NewProvider's name-based factory registry.
	_ "darvin-cowork/backend/internal/llm/anthropic"
	_ "darvin-cowork/backend/internal/llm/gemini"
	_ "darvin-cowork/backend/internal/llm/openai"
)

// defaultProvider is the fallback active provider key when the config has
// no llm.provider set.
const defaultProvider = "anthropic"

// activeProviderKey returns the configured active preset key, defaulting to
// anthropic when unset.
func activeProviderKey(cfg *config.Config) string {
	if cfg.LLM.Provider != "" {
		return cfg.LLM.Provider
	}
	return defaultProvider
}

// resolveWire maps a preset key + config entry onto the wire format name.
// An empty api_format falls back to "anthropic" for the anthropic key and
// "openai" for every other key (backward compatible with old configs).
func resolveWire(key string, entry config.LLMProviderConfig) string {
	if entry.APIFmt != "" {
		return entry.APIFmt
	}
	if key == "anthropic" {
		return "anthropic"
	}
	return "openai"
}

// loadAllProviders resolves every configured preset to a provider instance.
// It always includes the active key (falling back to the legacy top-level
// credentials) so the runtime has a default even when cfg.LLM.Providers is
// empty. Broken presets are logged and skipped; construction failures for
// the active provider are fatal.
func loadAllProviders(ctx context.Context, cfg *config.Config, log *zap.Logger) (map[string]llm.ModelProvider, error) {
	out := map[string]llm.ModelProvider{}
	active := activeProviderKey(cfg)

	if _, ok := cfg.Providers[active]; !ok {
		// Legacy config without a providers.<key> entry: derive the wire
		// from the key name (deepseek → openai, gemini → gemini) instead of
		// assuming anthropic, and use the top-level credentials.
		wire := resolveWire(active, config.LLMProviderConfig{})
		p, err := llm.NewProvider(ctx, wire, llm.ProviderConfig{
			APIKey:  cfg.LLM.APIKey,
			BaseURL: cfg.LLM.BaseURL,
			Logger:  log.Sugar(),
		})
		if err != nil {
			return nil, fmt.Errorf("construct active LLM provider %q: %w", active, err)
		}
		out[active] = p
	}

	for key, entry := range cfg.Providers {
		wire := resolveWire(key, entry)
		p, err := llm.NewProvider(ctx, wire, llm.ProviderConfig{
			APIKey:  entry.APIKey,
			BaseURL: entry.BaseURL,
			Logger:  log.Sugar(),
		})
		if err != nil {
			log.Warn("skip LLM provider preset", zap.String("preset", key), zap.Error(err))
			continue
		}
		out[key] = p
	}

	if _, ok := out[active]; !ok {
		return nil, fmt.Errorf("active LLM provider %q not resolvable", active)
	}
	return out, nil
}

// loadProvider returns the active provider instance (used as the agent's
// default). It derives the full preset map so per-run overrides can switch
// to any configured provider.
func loadProvider(ctx context.Context, cfg *config.Config, log *zap.Logger) (llm.ModelProvider, map[string]llm.ModelProvider, error) {
	providers, err := loadAllProviders(ctx, cfg, log)
	if err != nil {
		return nil, nil, err
	}
	return providers[activeProviderKey(cfg)], providers, nil
}
