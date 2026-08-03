package llm_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	// Side-effect import: registers the "anthropic" provider in init().
	"darvin-cowork/backend/internal/llm"
	_ "darvin-cowork/backend/internal/llm/anthropic"
)

func TestNewProvider_Unknown(t *testing.T) {
	_, err := llm.NewProvider(context.Background(), "no-such-provider", llm.ProviderConfig{APIKey: "k"})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if !strings.Contains(err.Error(), "no-such-provider") {
		t.Errorf("error should mention provider name: %v", err)
	}
	if !errors.Is(err, llm.ErrUnknownProvider) {
		t.Errorf("error should wrap ErrUnknownProvider: %v", err)
	}
}

func TestNewProvider_MissingAPIKey(t *testing.T) {
	// anthropic is registered by init() in the anthropic package which we
	// import here for the side-effect.
	_, err := llm.NewProvider(context.Background(), "anthropic", llm.ProviderConfig{})
	if err == nil {
		t.Fatal("expected ErrMissingAPIKey")
	}
	if !errors.Is(err, llm.ErrMissingAPIKey) {
		t.Errorf("expected ErrMissingAPIKey, got %v", err)
	}
}

func TestNewProvider_HappyPath(t *testing.T) {
	// This depends on the anthropic init() having registered. Importing
	// the anthropic package via a blank reference guarantees the init()
	// fires in this test binary.
	p, err := llm.NewProvider(context.Background(), "anthropic", llm.ProviderConfig{APIKey: "sk-test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "anthropic" {
		t.Errorf("Name() = %q, want %q", p.Name(), "anthropic")
	}
}

func TestRegisterProvider_DuplicatePanics(t *testing.T) {
	// The anthropic provider is registered via init(). Re-registering
	// the same name should panic.
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on duplicate registration")
		}
	}()
	llm.RegisterProvider("anthropic", func(cfg llm.ProviderConfig) (llm.ModelProvider, error) {
		return nil, nil
	})
}

func TestRegisterProvider_NilFactoryPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on nil factory")
		}
	}()
	llm.RegisterProvider("test-nil-factory", nil)
}

func TestRegisteredProviders_IncludesAnthropic(t *testing.T) {
	got := llm.RegisteredProviders()
	found := false
	for _, n := range got {
		if n == "anthropic" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected anthropic in RegisteredProviders(), got %v", got)
	}
}
