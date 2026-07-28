package llm

import (
	"errors"
	"fmt"
)

// Provider error codes. Every provider maps its native errors onto this
// small set so the Agent loop can switch on Code without learning each
// provider's vocabulary.
const (
	ErrCodeRateLimit      = "rate_limit_error"
	ErrCodeAuth           = "authentication_error"
	ErrCodeInvalidRequest = "invalid_request_error"
	ErrCodeInternal       = "internal_error"
)

// ProviderError is the unified error returned by every ModelProvider.
//
// Providers are expected to wrap their native failures into a *ProviderError
// before returning. Callers use errors.As to extract Code / StatusCode for
// retry or user-facing reporting. Cause is the original error for log
// diagnostics only; do not pattern-match on it across providers.
type ProviderError struct {
	Provider   string
	Code       string
	Message    string
	StatusCode int
	Cause      error
}

// Error implements the error interface.
func (e *ProviderError) Error() string {
	if e.StatusCode > 0 {
		return fmt.Sprintf("[%s] %s (status=%d): %s", e.Provider, e.Code, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("[%s] %s: %s", e.Provider, e.Code, e.Message)
}

// Unwrap exposes Cause for errors.Is / errors.As chains.
func (e *ProviderError) Unwrap() error { return e.Cause }

// IsCode reports whether err is a *ProviderError with the given code.
// It returns false for nil or non-provider errors.
func IsCode(err error, code string) bool {
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.Code == code
	}
	return false
}

// NewProviderError constructs a *ProviderError with the given fields.
// Cause may be nil.
func NewProviderError(provider, code, message string, status int, cause error) *ProviderError {
	return &ProviderError{
		Provider:   provider,
		Code:       code,
		Message:    message,
		StatusCode: status,
		Cause:      cause,
	}
}