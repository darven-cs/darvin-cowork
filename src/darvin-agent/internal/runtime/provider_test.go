// Tests for multi-provider resolution and wire dispatch.

package runtime

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/config"
)

func TestActiveProviderKey_Default(t *testing.T) {
	if got := activeProviderKey(&config.Config{}); got != "anthropic" {
		t.Errorf("default = %q, want anthropic", got)
	}
	if got := activeProviderKey(&config.Config{LLM: config.LLMConfig{Provider: "deepseek"}}); got != "deepseek" {
		t.Errorf("configured = %q, want deepseek", got)
	}
}

func TestResolveWire(t *testing.T) {
	cases := []struct {
		key   string
		entry config.LLMProviderConfig
		want  string
	}{
		{"anthropic", config.LLMProviderConfig{}, "anthropic"},
		{"anthropic", config.LLMProviderConfig{APIFmt: "openai"}, "openai"},
		{"deepseek", config.LLMProviderConfig{}, "openai"},
		{"custom", config.LLMProviderConfig{}, "openai"},
		{"qwen", config.LLMProviderConfig{APIFmt: "openai"}, "openai"},
	}
	for _, c := range cases {
		if got := resolveWire(c.key, c.entry); got != c.want {
			t.Errorf("resolveWire(%q, %+v) = %q, want %q", c.key, c.entry, got, c.want)
		}
	}
}

func TestLoadAllProviders_ActiveFallbackToTopLevel(t *testing.T) {
	// No providers map: the active key must resolve from the legacy
	// top-level credentials block.
	cfg := &config.Config{
		LLM: config.LLMConfig{
			Provider: "anthropic",
			APIKey:   "sk-test",
		},
	}
	providers, err := loadAllProviders(context.Background(), cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("loadAllProviders: %v", err)
	}
	p, ok := providers["anthropic"]
	if !ok || p == nil {
		t.Fatal("active provider anthropic missing")
	}
	if p.Name() != "anthropic" {
		t.Errorf("active provider Name = %q, want anthropic", p.Name())
	}
}

func TestLoadAllProviders_DispatchByAPIFmt(t *testing.T) {
	cfg := &config.Config{
		LLM: config.LLMConfig{
			Provider: "deepseek",
		},
		Providers: map[string]config.LLMProviderConfig{
			"deepseek": {APIFmt: "openai", BaseURL: "http://localhost:1234/v1", DefaultModel: "deepseek-chat"},
			"ollama":   {APIFmt: "openai", BaseURL: "http://localhost:11434/v1"},
		},
	}
	providers, err := loadAllProviders(context.Background(), cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("loadAllProviders: %v", err)
	}
	for _, key := range []string{"deepseek", "ollama"} {
		p, ok := providers[key]
		if !ok {
			t.Errorf("provider %q missing", key)
			continue
		}
		if p.Name() != "openai" {
			t.Errorf("provider %q Name = %q, want openai", key, p.Name())
		}
	}
	if _, ok := providers["anthropic"]; ok {
		t.Error("anthropic should not be resolved when not configured")
	}
}

// TestLoadAllProviders_LegacyFallbackDerivesWire reproduces the 404 bug: an
// active key like deepseek with no providers.<key> entry must resolve to the
// OPENAI wire (derived from the key name), not hardcoded anthropic.
func TestLoadAllProviders_LegacyFallbackDerivesWire(t *testing.T) {
	cfg := &config.Config{
		LLM: config.LLMConfig{
			Provider: "deepseek",
			APIKey:   "sk-test",
			BaseURL:  "https://api.deepseek.com",
			Model:    "deepseek-v4-flash",
		},
	}
	providers, err := loadAllProviders(context.Background(), cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("loadAllProviders: %v", err)
	}
	p, ok := providers["deepseek"]
	if !ok {
		t.Fatal("active provider deepseek missing")
	}
	if p.Name() != "openai" {
		t.Errorf("active deepseek Name = %q, want openai wire (not anthropic → 404)", p.Name())
	}
}
