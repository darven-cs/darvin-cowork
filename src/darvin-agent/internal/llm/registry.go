package llm

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ProviderFactory builds a ModelProvider from a ProviderConfig. Provider
// packages register a factory in their init() so the llm package can
// dispatch by name without importing each provider.
type ProviderFactory func(cfg ProviderConfig) (ModelProvider, error)

// ProviderConfig carries credentials and endpoints for a provider.
type ProviderConfig struct {
	APIKey  string
	BaseURL string
	Extra   map[string]any
	Logger  Logger
}

// Sentinel errors returned by NewProvider.
var (
	ErrUnknownProvider = errors.New("llm: unknown provider")
	ErrMissingAPIKey   = errors.New("llm: missing API key")
)

var (
	registryMu sync.RWMutex
	registry   = map[string]ProviderFactory{}
)

// RegisterProvider makes a provider available under the given name.
// Intended to be called from provider packages' init(); duplicate names
// panic.
func RegisterProvider(name string, factory ProviderFactory) {
	if name == "" {
		panic("llm: RegisterProvider called with empty name")
	}
	if factory == nil {
		panic("llm: RegisterProvider called with nil factory")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("llm: provider %q already registered", name))
	}
	registry[name] = factory
}

// RegisteredProviders returns the sorted list of registered provider names.
func RegisteredProviders() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// NewProvider constructs a ModelProvider by name (no network I/O).
func NewProvider(ctx context.Context, name string, cfg ProviderConfig) (ModelProvider, error) {
	registryMu.RLock()
	factory, ok := registry[name]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q (registered: %v)", ErrUnknownProvider, name, RegisteredProviders())
	}
	return factory(cfg)
}
