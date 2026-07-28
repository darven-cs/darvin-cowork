package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// maxRetries is the upper bound on retries for 429 / 5xx responses.
// We keep it conservative: 2 retries, exponential backoff 1s / 2s.
const (
	maxRetries     = 2
	initialBackoff = 1 * time.Second
	maxBackoff     = 2 * time.Second
)

// HTTPClient is a thin wrapper around net/http with provider-aware retry
// behaviour. It centralises:
//
//   - JSON request body marshalling
//   - 429 / 5xx retries with exponential backoff
//   - ProviderError wrapping on terminal failure
//   - Logger access (when configured)
//
// All methods respect ctx cancellation / deadlines.
//
// It is exported because each provider package needs its own instance to
// inject retry / logging behaviour; consumers of the llm package do not
// touch this type directly.
type HTTPClient struct {
	client  *http.Client
	logger  Logger
	backoff []time.Duration
}

// Logger is the minimal logger surface this package needs. It is satisfied
// by *zap.SugaredLogger, *slog.Logger, or a no-op. The agent's own
// internal/logger.Logger satisfies it.
type Logger interface {
	Debugw(msg string, keysAndValues ...any)
	Infow(msg string, keysAndValues ...any)
	Warnw(msg string, keysAndValues ...any)
	Errorw(msg string, keysAndValues ...any)
}

// NewHTTPClient constructs an HTTPClient with sensible defaults.
// Callers may pass a nil logger to silence logs. Passing a nil *http.Client
// installs a default with no built-in timeout (the caller is expected to
// govern request lifetime via context).
func NewHTTPClient(client *http.Client, logger Logger) *HTTPClient {
	if client == nil {
		client = &http.Client{Timeout: 0}
	}
	return &HTTPClient{
		client:  client,
		logger:  logger,
		backoff: []time.Duration{initialBackoff, maxBackoff},
	}
}

// Do performs a POST request with a JSON body and returns the response body.
//
// On 429 / 5xx responses it retries up to maxRetries times with exponential
// backoff (1s, 2s) honouring ctx cancellation. Other 4xx responses are
// returned as *ProviderError{Code: ErrCodeInvalidRequest} without retry.
// Network failures (DNS, connection refused, EOF) are wrapped as
// *ProviderError{Code: ErrCodeInternal} and retried.
//
// The body is fully buffered in memory. Streaming is handled by DoStream.
func (c *HTTPClient) Do(
	ctx context.Context,
	provider string,
	url string,
	headers map[string]string,
	payload any,
) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, NewProviderError(provider, ErrCodeInvalidRequest,
			fmt.Sprintf("marshal payload: %s", err.Error()), 0, err)
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			if err := sleepWithCtx(ctx, c.backoff[attempt-1]); err != nil {
				return nil, err
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, NewProviderError(provider, ErrCodeInternal,
				fmt.Sprintf("build request: %s", err.Error()), 0, err)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		req.Header.Set("content-type", "application/json")

		c.logDebug(provider, "http request",
			"url", url,
			"attempt", attempt+1,
			"body_bytes", len(body),
		)

		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = NewProviderError(provider, ErrCodeInternal,
				fmt.Sprintf("http do: %s", err.Error()), 0, err)
			if !isRetryableTransport(err) || ctx.Err() != nil {
				return nil, lastErr
			}
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = NewProviderError(provider, ErrCodeInternal,
				fmt.Sprintf("read body: %s", readErr.Error()), resp.StatusCode, readErr)
			if ctx.Err() != nil {
				return nil, lastErr
			}
			continue
		}

		// Non-retryable client errors.
		if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
			return nil, parseProviderError(provider, resp.StatusCode, respBody)
		}

		// Retryable status codes.
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = parseProviderError(provider, resp.StatusCode, respBody)
			continue
		}

		// Success.
		c.logDebug(provider, "http response",
			"url", url,
			"status", resp.StatusCode,
			"body_bytes", len(respBody),
		)
		return respBody, nil
	}

	if lastErr == nil {
		lastErr = errors.New("retries exhausted with no recorded error")
	}
	return nil, lastErr
}

// DoStream performs a POST and returns the raw response body for the caller
// to parse as SSE / chunked. Status code errors are returned as
// *ProviderError before any body is exposed. Network errors follow the same
// retry policy as Do.
func (c *HTTPClient) DoStream(
	ctx context.Context,
	provider string,
	url string,
	headers map[string]string,
	payload any,
) (io.ReadCloser, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, NewProviderError(provider, ErrCodeInvalidRequest,
			fmt.Sprintf("marshal payload: %s", err.Error()), 0, err)
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			if err := sleepWithCtx(ctx, c.backoff[attempt-1]); err != nil {
				return nil, err
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, NewProviderError(provider, ErrCodeInternal,
				fmt.Sprintf("build request: %s", err.Error()), 0, err)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("accept", "text/event-stream")

		c.logDebug(provider, "http stream request",
			"url", url,
			"attempt", attempt+1,
			"body_bytes", len(body),
		)

		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = NewProviderError(provider, ErrCodeInternal,
				fmt.Sprintf("http do: %s", err.Error()), 0, err)
			if !isRetryableTransport(err) || ctx.Err() != nil {
				return nil, lastErr
			}
			continue
		}

		if resp.StatusCode >= 400 {
			// Drain the body to allow connection reuse and to surface
			// the provider's error payload via parseProviderError.
			respBody, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
				lastErr = parseProviderError(provider, resp.StatusCode, respBody)
				continue
			}
			return nil, parseProviderError(provider, resp.StatusCode, respBody)
		}

		c.logDebug(provider, "http stream open",
			"url", url,
			"status", resp.StatusCode,
		)
		return resp.Body, nil
	}

	if lastErr == nil {
		lastErr = errors.New("retries exhausted with no recorded error")
	}
	return nil, lastErr
}

// parseProviderError converts a raw error response body into a ProviderError.
//
// Each provider supplies its own decoder via ProviderErrorParser to extract
// the canonical error type / code. If parsing fails we fall back to a
// generic ErrCodeInternal with the raw body in Message.
type ProviderErrorParser func(statusCode int, body []byte) (code, message string, ok bool)

// parseProviderError delegates to the per-provider parser when set; the
// default fallback uses a tolerant shape that covers Anthropic / OpenAI /
// Gemini error envelopes.
func parseProviderError(provider string, status int, body []byte) error {
	code, message, ok := defaultProviderErrorParser(status, body)
	if !ok {
		code = ErrCodeInternal
		message = truncate(string(body), 512)
	}
	return NewProviderError(provider, code, message, status, nil)
}

// defaultProviderErrorParser recognises a handful of common envelopes:
//   - {"type":"error","error":{"type":"<code>","message":"..."}}   (Anthropic)
//   - {"error":{"type":"<code>","message":"..."}}                 (OpenAI)
//   - {"error":{"code":<int>,"message":"...","status":"..."}}     (Google)
//   - {"message":"..."}                                            (Generic)
func defaultProviderErrorParser(status int, body []byte) (string, string, bool) {
	var envelope struct {
		Type  string `json:"type"`
		Error *struct {
			Type    string `json:"type"`
			Code    any    `json:"code"` // string or int
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", "", false
	}

	if envelope.Error != nil {
		if envelope.Error.Type != "" {
			return mapAnthropicCode(envelope.Error.Type), envelope.Error.Message, true
		}
		if envelope.Error.Message != "" {
			return mapHTTPStatus(status), envelope.Error.Message, true
		}
	}
	if envelope.Type != "" && envelope.Type == "error" {
		return mapHTTPStatus(status), envelope.Message, true
	}
	if envelope.Message != "" {
		return mapHTTPStatus(status), envelope.Message, true
	}
	return "", "", false
}

// mapAnthropicCode maps Anthropic's error.type to the unified code.
func mapAnthropicCode(t string) string {
	switch t {
	case "authentication_error":
		return ErrCodeAuth
	case "rate_limit_error":
		return ErrCodeRateLimit
	case "invalid_request_error":
		return ErrCodeInvalidRequest
	default:
		return ErrCodeInternal
	}
}

// mapHTTPStatus maps a 4xx / 5xx HTTP status into a unified code when the
// provider didn't include a more specific type.
func mapHTTPStatus(status int) string {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrCodeAuth
	case http.StatusTooManyRequests:
		return ErrCodeRateLimit
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return ErrCodeInvalidRequest
	default:
		return ErrCodeInternal
	}
}

// isRetryableTransport returns true for transport errors worth retrying.
func isRetryableTransport(err error) bool {
	if err == nil {
		return false
	}
	// context errors propagate up; we don't retry those.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	// Everything else (DNS, connection reset, EOF, timeouts other than ctx)
	// is treated as transient.
	return true
}

// sleepWithCtx blocks for d or until ctx is cancelled.
func sleepWithCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// truncate limits a string to max bytes for safe logging.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// logDebug / logWarn etc. tolerate a nil logger so tests don't need a stub.
func (c *HTTPClient) logDebug(provider, msg string, kv ...any) {
	if c.logger == nil {
		return
	}
	kv = append([]any{"provider", provider}, kv...)
	c.logger.Debugw(msg, kv...)
}

// redactSecrets returns a redacted copy of the headers map suitable for
// logging. It is exposed (capitalised form omitted to keep the surface
// small) for callers who want to log outbound headers.
func redactSecrets(h map[string]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		kl := strings.ToLower(k)
		if kl == "x-api-key" || kl == "authorization" {
			out[k] = "[REDACTED]"
			continue
		}
		out[k] = v
	}
	return out
}