// Native Google Generative AI (Gemini) provider via the generateContent /
// streamGenerateContent REST endpoints.

package gemini

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"darvin-cowork/backend/internal/llm"
)

// defaultBaseURL is the Gemini Developer API root. Tests and proxies can
// override via ProviderConfig.BaseURL.
const defaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// Endpoint suffixes appended to {base}/models/{model}.
const (
	generateSuffix       = ":generateContent"
	streamGenerateSuffix = ":streamGenerateContent?alt=sse"
)

// Provider implements llm.ModelProvider against the Gemini API. One
// Provider instance is safe for concurrent use.
type Provider struct {
	apiKey  string
	baseURL string
	hc      *httpClient
}

type httpClient = llm.HTTPClient

// New constructs a Gemini Provider from a ProviderConfig.
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

// init registers the Gemini provider with the llm registry and populates
// the default ModelRegistry with the Gemini model family.
func init() {
	llm.RegisterProvider("gemini", func(cfg llm.ProviderConfig) (llm.ModelProvider, error) {
		if cfg.APIKey == "" {
			return nil, llm.ErrMissingAPIKey
		}
		return New(cfg), nil
	})
	for _, m := range geminiModels() {
		llm.DefaultModelRegistry.MustRegisterModel(m)
	}
}

// Name returns the wire format identifier.
func (p *Provider) Name() string { return "gemini" }

// Complete performs a non-streaming generateContent call.
func (p *Provider) Complete(ctx context.Context, req *llm.CompletionRequest) (*llm.CompletionResponse, error) {
	payload, err := buildRequest(req, false)
	if err != nil {
		return nil, err
	}
	body, err := p.hc.Do(ctx, p.Name(), p.endpoint(req.Model, false), p.headers(), payload)
	if err != nil {
		return nil, err
	}
	return parseResponse(body)
}

// Stream opens a streaming streamGenerateContent call. See stream.go for
// the per-chunk event translation.
func (p *Provider) Stream(ctx context.Context, req *llm.CompletionRequest) (*llm.StreamingResponse, error) {
	payload, err := buildRequest(req, true)
	if err != nil {
		return nil, err
	}
	body, err := p.hc.DoStream(ctx, p.Name(), p.endpoint(req.Model, true), p.headers(), payload)
	if err != nil {
		return nil, err
	}

	events := make(chan llm.StreamEvent, 16)
	sr := llm.NewStreamingResponse(events, body)
	go func() {
		defer close(events)
		defer body.Close()
		if err := runStream(ctx, body, events, req.Model); err != nil {
			sr.SetErr(err)
			select {
			case events <- llm.ErrorEvent{Err: err}:
			case <-ctx.Done():
			}
		}
	}()
	return sr, nil
}

// endpoint builds the per-model generateContent / streamGenerateContent URL.
func (p *Provider) endpoint(model string, stream bool) string {
	modelPath := "/models/" + urlEscapeModel(model)
	if stream {
		return p.baseURL + modelPath + streamGenerateSuffix
	}
	return p.baseURL + modelPath + generateSuffix
}

// headers returns the Gemini API key header.
func (p *Provider) headers() map[string]string {
	return map[string]string{"x-goog-api-key": p.apiKey}
}

// urlEscapeModel encodes the model id for the URL path segment.
func urlEscapeModel(model string) string {
	return url.PathEscape(model)
}
