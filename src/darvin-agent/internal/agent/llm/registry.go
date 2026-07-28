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
// dispatch by name without importing each provider (which would create an
// import cycle).
type ProviderFactory func(cfg ProviderConfig) (ModelProvider, error)

// ProviderConfig carries the credentials and endpoints needed to construct
// a provider instance. Each field is optional except APIKey (which the
// Anthropic / OpenAI / Gemini providers all require).
type ProviderConfig struct {
	// APIKey is the provider-issued key. For Anthropic it goes in the
	// x-api-key header; for OpenAI / Gemini it goes in Authorization /
	// x-goog-api-key respectively.
	APIKey string

	// BaseURL overrides the provider's default endpoint. Useful for
	// proxies and self-hosted gateways. Leave empty for production.
	BaseURL string

	// Extra is an opaque bag of provider-specific tuning knobs.
	Extra map[string]any

	// Logger is the optional logger to use for debug / warn lines. Nil is
	// allowed and disables logging.
	Logger Logger
}

// Sentinel errors returned by NewProvider.
var (
	// ErrUnknownProvider is returned when the requested provider name has
	// no registered implementation in this build.
	ErrUnknownProvider = errors.New("llm: unknown provider")

	// ErrMissingAPIKey is returned when the constructed config has no
	// APIKey. Providers never issue requests with an empty key.
	ErrMissingAPIKey = errors.New("llm: missing API key")
)

var (
	registryMu sync.RWMutex
	registry   = map[string]ProviderFactory{}
)

// RegisterProvider makes a provider available under the given name. It is
// intended to be called from provider packages' init() functions so the
// set of supported providers expands by simply importing them.
//
// Registering the same name twice is a programmer error and panics —
// duplicate names typically indicate a copy/paste bug in the provider
// package.
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
// Useful for diagnostics and /config dump endpoints.
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

// NewProvider constructs a ModelProvider by name.
//
// The set of recognised names is determined by which provider packages
// have been imported (and therefore called RegisterProvider from init()).
// NewProvider does not perform network I/O; it only validates the
// configuration and dispatches to the registered factory.
func NewProvider(ctx context.Context, name string, cfg ProviderConfig) (ModelProvider, error) {
	registryMu.RLock()
	factory, ok := registry[name]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q (registered: %v)", ErrUnknownProvider, name, RegisteredProviders())
	}
	return factory(cfg)
}