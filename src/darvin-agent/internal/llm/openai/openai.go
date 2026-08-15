// OpenAI-compatible Chat Completions provider. A single implementation
// serves every OpenAI-compatible gateway (OpenAI / DeepSeek / Qwen /
// Zhipu / Moonshot / MiniMax / Volcengine / OpenRouter / Ollama / Gemini);
// the difference between them is only base URL, API key, and model, so the
// shared preset catalog in src/shared/providers.ts maps each preset onto
// this wire format via config.Providers[].api_format.

package openai

import (
	"context"
	"net/http"
	"strings"

	"darvin-cowork/backend/internal/llm"
)

// defaultBaseURL is the OpenAI production endpoint. Tests and gateways can
// override via ProviderConfig.BaseURL.
const defaultBaseURL = "https://api.openai.com/v1"

// Provider implements llm.ModelProvider against the OpenAI chat.completions
// API. One Provider instance is safe for concurrent use; each call builds
// its own HTTP request through the shared httpClient which handles retries.
type Provider struct {
	name    string
	apiKey  string
	baseURL string
	hc      *httpClient
}

// httpClient is a local type alias so we can wire the package-internal
// helpers without exporting them to llm.
type httpClient = llm.HTTPClient

// New constructs an OpenAI-compatible Provider from a ProviderConfig.
// An empty apiKey is allowed (local Ollama and some gateways need no key);
// the settings layer enforces per-preset key requirements.
func New(cfg llm.ProviderConfig) *Provider {
	base := cfg.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	base = strings.TrimRight(base, "/")
	return &Provider{
		name:    "openai",
		apiKey:  cfg.APIKey,
		baseURL: base,
		hc:      llm.NewHTTPClient(&http.Client{}, cfg.Logger),
	}
}

// init registers the OpenAI-compatible provider with the llm registry under
// the "openai" wire format. Every OpenAI-compatible preset (deepseek / qwen /
// ollama / custom ...) resolves to this factory via api_format. It also
// populates the default ModelRegistry with the official OpenAI model family.
func init() {
	llm.RegisterProvider("openai", func(cfg llm.ProviderConfig) (llm.ModelProvider, error) {
		return New(cfg), nil
	})
	for _, m := range openAIModels() {
		llm.DefaultModelRegistry.MustRegisterModel(m)
	}
}

// Name returns the wire format identifier.
func (p *Provider) Name() string { return p.name }

// Complete performs a non-streaming call to the chat completions endpoint.
func (p *Provider) Complete(ctx context.Context, req *llm.CompletionRequest) (*llm.CompletionResponse, error) {
	payload, err := buildRequest(req, false)
	if err != nil {
		return nil, err
	}
	headers := p.headers()
	body, err := p.hc.Do(ctx, p.Name(), p.chatURL(), headers, payload)
	if err != nil {
		return nil, err
	}
	return parseResponse(body)
}

// Stream opens a streaming call to the chat completions endpoint. See
// stream.go for the per-chunk event translation.
func (p *Provider) Stream(ctx context.Context, req *llm.CompletionRequest) (*llm.StreamingResponse, error) {
	return openStream(ctx, p.hc, p.chatURL(), p.headers(), req)
}

// chatURL returns the full chat completions URL. Some OpenAI-compatible
// gateways (Youdao / Xiaomi) ship a full URL as the base; the path suffix
// is only appended when absent.
func (p *Provider) chatURL() string {
	if strings.Contains(p.baseURL, "/chat/completions") {
		return p.baseURL
	}
	return p.baseURL + chatPath
}

// headers returns the standard bearer-token headers. The Authorization
// header is omitted when no key is configured (keyless local gateways).
func (p *Provider) headers() map[string]string {
	h := map[string]string{}
	if p.apiKey != "" {
		h["Authorization"] = "Bearer " + p.apiKey
	}
	return h
}
