package anthropic

import (
	"testing"

	"darvin-cowork/backend/internal/agent/llm"
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