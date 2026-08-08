// Constructs the LLM provider used by the runtime.

package runtime

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/config"
	"darvin-cowork/backend/internal/llm"

	// Blank import triggers anthropic.init() which registers the
	// provider with llm.NewProvider's name-based factory registry.
	_ "darvin-cowork/backend/internal/llm/anthropic"
)

// loadProvider constructs the LLM provider the runtime talks to. The
// provider is exposed on *Runtime for hot-swap or shutdown paths.
func loadProvider(ctx context.Context, cfg *config.Config, log *zap.Logger) (llm.ModelProvider, error) {
	p, err := llm.NewProvider(ctx, cfg.LLM.Provider, llm.ProviderConfig{
		APIKey:  cfg.LLM.APIKey,
		BaseURL: cfg.LLM.BaseURL,
	})
	if err != nil {
		log.Error("failed to construct LLM provider",
			zap.String("provider", cfg.LLM.Provider), zap.Error(err))
		return nil, fmt.Errorf("construct LLM provider %q: %w", cfg.LLM.Provider, err)
	}
	return p, nil
}
