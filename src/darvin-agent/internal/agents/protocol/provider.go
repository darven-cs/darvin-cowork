package protocol

import (
	"context"
	"io"
)

// ModelProvider is the unified LLM client contract.
type ModelProvider interface {
	Name() string
	Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error)
	Stream(ctx context.Context, req *CompletionRequest) (*StreamingResponse, error)
}

// StreamingResponse is the handle to an in-flight provider stream.
// Events closes after DoneEvent or ErrorEvent; Err returns the terminal error.
type StreamingResponse struct {
	Events <-chan StreamEvent

	err  error
	body io.ReadCloser
}

// NewStreamingResponse wires events and HTTP body; provider packages only.
func NewStreamingResponse(events chan StreamEvent, body io.ReadCloser) *StreamingResponse {
	return &StreamingResponse{Events: events, body: body}
}

// Err returns the terminal stream error; safe to call after Events closes.
func (s *StreamingResponse) Err() error { return s.err }

// SetErr records the terminal error before Events closes; idempotent.
func (s *StreamingResponse) SetErr(err error) {
	if s.err == nil && err != nil {
		s.err = err
	}
}

// Close releases stream resources; safe to call multiple times.
func (s *StreamingResponse) Close() error {
	if s.body != nil {
		return s.body.Close()
	}
	return nil
}