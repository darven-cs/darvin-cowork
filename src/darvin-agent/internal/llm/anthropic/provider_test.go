package anthropic

import (
	"testing"

	"darvin-cowork/backend/internal/llm"
)

func TestProvider_Name(t *testing.T) {
	p := New(llm.ProviderConfig{APIKey: "sk-test"})
	if got := p.Name(); got != "anthropic" {
		t.Errorf("Name() = %q, want %q", got, "anthropic")
	}
}

func TestProvider_Headers(t *testing.T) {
	p := New(llm.ProviderConfig{APIKey: "sk-test-abc"})
	h := p.headers()
	if h["x-api-key"] != "sk-test-abc" {
		t.Errorf("x-api-key = %q, want %q", h["x-api-key"], "sk-test-abc")
	}
	if h["anthropic-version"] != anthropicVersion {
		t.Errorf("anthropic-version = %q, want %q", h["anthropic-version"], anthropicVersion)
	}
}

func TestProvider_BaseURL_Default(t *testing.T) {
	p := New(llm.ProviderConfig{APIKey: "k"})
	want := "https://api.anthropic.com"
	if p.baseURL != want {
		t.Errorf("baseURL = %q, want %q", p.baseURL, want)
	}
}

func TestProvider_BaseURL_TrailingSlashTrimmed(t *testing.T) {
	p := New(llm.ProviderConfig{APIKey: "k", BaseURL: "https://proxy.example.com/"})
	want := "https://proxy.example.com"
	if p.baseURL != want {
		t.Errorf("baseURL = %q, want %q", p.baseURL, want)
	}
}

func TestProvider_BaseURL_CustomPath(t *testing.T) {
	p := New(llm.ProviderConfig{APIKey: "k", BaseURL: "http://localhost:8080/gateway/anthropic"})
	if p.baseURL+messagesPath != "http://localhost:8080/gateway/anthropic/v1/messages" {
		t.Errorf("full endpoint = %q", p.baseURL+messagesPath)
	}
}

func TestInit_RegistersAnthropicModels(t *testing.T) {
	// init() runs once at package import; this test confirms the side
	// effect happened by querying the default registry.
	models := llm.DefaultModelRegistry.ListByProvider("anthropic")
	if len(models) < 4 {
		t.Fatalf("expected ≥4 anthropic models, got %d", len(models))
	}
	seen := map[string]bool{}
	for _, m := range models {
		seen[m.ID] = true
		if m.Provider != "anthropic" {
			t.Errorf("model %s has Provider=%q, want anthropic", m.ID, m.Provider)
		}
		if m.APIVersion != llm.APIAnthropicMessages {
			t.Errorf("model %s APIVersion=%q, want %q", m.ID, m.APIVersion, llm.APIAnthropicMessages)
		}
	}
	for _, want := range []string{"claude-sonnet-4-5", "claude-opus-4-1", "claude-3-5-sonnet-latest", "claude-3-5-haiku-latest"} {
		if !seen[want] {
			t.Errorf("expected model %q in registry", want)
		}
	}

	m, ok := llm.DefaultModelRegistry.Get("claude-sonnet-4-5")
	if !ok {
		t.Fatal("Get(claude-sonnet-4-5) not found")
	}
	if m.ContextWindow != 200000 {
		t.Errorf("ContextWindow = %d, want 200000", m.ContextWindow)
	}
	if !m.Reasoning {
		t.Errorf("claude-sonnet-4-5 should be Reasoning=true")
	}
	if m.ThinkingMap[llm.ThinkingHigh] != "8192" {
		t.Errorf("ThinkingMap[High] = %q, want %q", m.ThinkingMap[llm.ThinkingHigh], "8192")
	}
}

func TestInit_RegistersCompatFlags(t *testing.T) {
	m, ok := llm.DefaultModelRegistry.Get("claude-3-5-haiku-latest")
	if !ok {
		t.Fatal("Get(claude-3-5-haiku-latest) not found")
	}
	if !m.Compat.SupportsToolCalls {
		t.Errorf("SupportsToolCalls should be true")
	}
	if !m.Compat.SupportsImageInput {
		t.Errorf("SupportsImageInput should be true")
	}
	if !m.Compat.SupportsUsageInStream {
		t.Errorf("SupportsUsageInStream should be true")
	}
}
