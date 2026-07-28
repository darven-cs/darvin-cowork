package anthropic

import (
	"context"
	"net/http"
	"strings"

	"darvin-cowork/backend/internal/agent/llm"
)

// defaultBaseURL is the Anthropic production endpoint. Tests and proxies
// can override via ProviderConfig.BaseURL.
const defaultBaseURL = "https://api.anthropic.com"

// Provider implements llm.ModelProvider against the Anthropic Messages API.
//
// One Provider instance is safe for concurrent use; each call constructs
// its own HTTP request through the shared httpClient which handles retries.
type Provider struct {
	apiKey  string
	baseURL string
	hc      *httpClient
}

// httpClient is a local type alias so we can wire the package-internal
// helpers (do, doStream) without exporting them to llm. The llm package
// owns the production implementation; this file just wraps it.
type httpClient = llm.HTTPClient

// New constructs an Anthropic Provider from a ProviderConfig. The HTTP
// client is created lazily with sensible defaults.
func New(cfg llm.ProviderConfig) *Provider {
	base := cfg.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	base = strings.TrimRight(base, "/")
	return &Provider{
		apiKey:  cfg.APIKey,
		baseURL: base,
		hc:      llm.NewHTTPClient(&http.Client{}, cfg.Logger),
	}
}

// init registers the Anthropic provider with the llm registry so callers
// can obtain it via llm.NewProvider(ctx, "anthropic", cfg). The blank
// import in main.go is what pulls this init() into the binary.
func init() {
	llm.RegisterProvider("anthropic", func(cfg llm.ProviderConfig) (llm.ModelProvider, error) {
		if cfg.APIKey == "" {
			return nil, llm.ErrMissingAPIKey
		}
		return New(cfg), nil
	})
}

// Name returns the stable provider identifier.
func (p *Provider) Name() string { return "anthropic" }

// Complete performs a non-streaming call to /v1/messages.
func (p *Provider) Complete(ctx context.Context, req *llm.CompletionRequest) (*llm.CompletionResponse, error) {
	payload, err := buildRequest(req, false)
	if err != nil {
		return nil, err
	}
	headers := p.headers()
	body, err := p.hc.Do(ctx, p.Name(), p.baseURL+messagesPath, headers, payload)
	if err != nil {
		return nil, err
	}
	return parseResponse(body)
}

// Stream opens a streaming call to /v1/messages. See stream.go for the
// per-frame event translation.
func (p *Provider) Stream(ctx context.Context, req *llm.CompletionRequest) (*llm.StreamingResponse, error) {
	return openStream(ctx, p.hc, p.baseURL+messagesPath, p.headers(), req)
}

// headers returns the standard Anthropic request headers.
func (p *Provider) headers() map[string]string {
	return map[string]string{
		"x-api-key":         p.apiKey,
		"anthropic-version": anthropicVersion,
	}
}
