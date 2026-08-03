package llm

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestDefaultProviderErrorParser_AnthropicShape(t *testing.T) {
	body := []byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`)
	code, msg, ok := defaultProviderErrorParser(http.StatusTooManyRequests, body)
	if !ok {
		t.Fatal("expected ok=true for anthropic error envelope")
	}
	if code != ErrCodeRateLimit {
		t.Errorf("code = %q, want %q", code, ErrCodeRateLimit)
	}
	if msg != "slow down" {
		t.Errorf("msg = %q, want %q", msg, "slow down")
	}
}

func TestDefaultProviderErrorParser_OpenAIShape(t *testing.T) {
	body := []byte(`{"error":{"type":"invalid_request_error","message":"bad model"}}`)
	code, msg, ok := defaultProviderErrorParser(http.StatusBadRequest, body)
	if !ok {
		t.Fatal("expected ok=true for openai error envelope")
	}
	if code != ErrCodeInvalidRequest {
		t.Errorf("code = %q, want %q", code, ErrCodeInvalidRequest)
	}
	if msg != "bad model" {
		t.Errorf("msg = %q, want %q", msg, "bad model")
	}
}

func TestDefaultProviderErrorParser_GenericShape(t *testing.T) {
	body := []byte(`{"message":"something broke"}`)
	code, _, ok := defaultProviderErrorParser(http.StatusInternalServerError, body)
	if !ok {
		t.Fatal("expected ok=true for generic envelope")
	}
	if code != ErrCodeInternal {
		t.Errorf("code = %q, want %q", code, ErrCodeInternal)
	}
}

func TestDefaultProviderErrorParser_UnknownShape(t *testing.T) {
	body := []byte(`not json`)
	if _, _, ok := defaultProviderErrorParser(http.StatusBadRequest, body); ok {
		t.Errorf("expected ok=false for non-JSON body")
	}
}

func TestMapAnthropicCode(t *testing.T) {
	cases := map[string]string{
		"authentication_error":  ErrCodeAuth,
		"rate_limit_error":      ErrCodeRateLimit,
		"invalid_request_error": ErrCodeInvalidRequest,
		"some_other_error":      ErrCodeInternal,
		"":                      ErrCodeInternal,
	}
	for in, want := range cases {
		if got := mapAnthropicCode(in); got != want {
			t.Errorf("mapAnthropicCode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMapHTTPStatus(t *testing.T) {
	cases := map[int]string{
		http.StatusUnauthorized:        ErrCodeAuth,
		http.StatusForbidden:           ErrCodeAuth,
		http.StatusTooManyRequests:     ErrCodeRateLimit,
		http.StatusBadRequest:          ErrCodeInvalidRequest,
		http.StatusUnprocessableEntity: ErrCodeInvalidRequest,
		http.StatusInternalServerError: ErrCodeInternal,
		http.StatusBadGateway:          ErrCodeInternal,
	}
	for status, want := range cases {
		if got := mapHTTPStatus(status); got != want {
			t.Errorf("mapHTTPStatus(%d) = %q, want %q", status, got, want)
		}
	}
}

func TestIsRetryableTransport(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"context.Canceled", context.Canceled, false},
		{"context.DeadlineExceeded", context.DeadlineExceeded, false},
		{"plain", errors.New("connection refused"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isRetryableTransport(c.err); got != c.want {
				t.Errorf("isRetryableTransport(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hi", 10); got != "hi" {
		t.Errorf("truncate short string = %q, want %q", got, "hi")
	}
	if got := truncate("hello world", 5); !strings.HasSuffix(got, "...") {
		t.Errorf("truncate long string should end with ellipsis, got %q", got)
	}
}

func TestRedactSecrets(t *testing.T) {
	in := map[string]string{
		"x-api-key":         "sk-ant-secret",
		"Authorization":     "Bearer xyz",
		"content-type":      "application/json",
		"anthropic-version": "2023-06-01",
	}
	out := redactSecrets(in)
	if out["x-api-key"] != "[REDACTED]" {
		t.Errorf("x-api-key not redacted: %q", out["x-api-key"])
	}
	if out["Authorization"] != "[REDACTED]" {
		t.Errorf("Authorization not redacted: %q", out["Authorization"])
	}
	if out["content-type"] != "application/json" {
		t.Errorf("non-secret header mutated: %q", out["content-type"])
	}
}

func TestSleepWithCtx_Cancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepWithCtx(ctx, 5*1000*1000); err == nil {
		t.Errorf("expected error after ctx cancel")
	}
}
