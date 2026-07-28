package llm

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestProviderError_Error_WithoutStatus(t *testing.T) {
	e := NewProviderError("anthropic", ErrCodeAuth, "bad key", 0, nil)
	got := e.Error()
	if !strings.Contains(got, "[anthropic]") {
		t.Errorf("Error() missing provider tag: %q", got)
	}
	if !strings.Contains(got, ErrCodeAuth) {
		t.Errorf("Error() missing code: %q", got)
	}
	if !strings.Contains(got, "bad key") {
		t.Errorf("Error() missing message: %q", got)
	}
	if strings.Contains(got, "status=") {
		t.Errorf("Error() should not include status when StatusCode=0: %q", got)
	}
}

func TestProviderError_Error_WithStatus(t *testing.T) {
	e := NewProviderError("openai", ErrCodeRateLimit, "quota exceeded", 429, nil)
	got := e.Error()
	if !strings.Contains(got, "status=429") {
		t.Errorf("Error() missing status=429: %q", got)
	}
}

func TestProviderError_Unwrap(t *testing.T) {
	cause := errors.New("boom")
	e := NewProviderError("anthropic", ErrCodeInternal, "wrapped", 500, cause)
	if !errors.Is(e, cause) {
		t.Errorf("errors.Is should match the underlying cause")
	}
	if e.Unwrap() != cause {
		t.Errorf("Unwrap() = %v, want %v", e.Unwrap(), cause)
	}
}

func TestIsCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code string
		want bool
	}{
		{"nil", nil, ErrCodeAuth, false},
		{"plain error", errors.New("nope"), ErrCodeAuth, false},
		{"matching code", NewProviderError("anthropic", ErrCodeRateLimit, "x", 429, nil), ErrCodeRateLimit, true},
		{"different code", NewProviderError("anthropic", ErrCodeAuth, "x", 401, nil), ErrCodeRateLimit, false},
		{"wrapped", fmt.Errorf("layer: %w", NewProviderError("anthropic", ErrCodeInvalidRequest, "x", 400, nil)), ErrCodeInvalidRequest, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsCode(c.err, c.code); got != c.want {
				t.Errorf("IsCode(%v, %q) = %v, want %v", c.err, c.code, got, c.want)
			}
		})
	}
}