package protocol

import (
	"context"
	"io"
)

// ModelProvider is the unified LLM client contract.
//
// Implementations translate the provider-agnostic CompletionRequest /
// CompletionResponse / StreamEvent into provider-native HTTP and SSE shapes,
// hiding transport details from the Agent loop.
type ModelProvider interface {
	// Name returns the stable identifier of the provider
	// (e.g. "anthropic", "openai"). It is used in logs and error wrapping.
	Name() string

	// Complete performs a single non-streaming call and returns the full
	// CompletionResponse. The context governs the request lifetime and
	// can be cancelled to abort the HTTP call.
	Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error)

	// Stream opens a streaming call. The returned StreamingResponse exposes
	// a channel of unified StreamEvent values; the caller drains it with
	// `for ev := range sr.Events()` and inspects sr.Err() afterwards.
	//
	// The returned error is for setup-time failures (auth, payload
	// construction, immediate HTTP failure). Once the channel is opened,
	// transient failures surface as ErrorEvent values inside the stream.
	Stream(ctx context.Context, req *CompletionRequest) (*StreamingResponse, error)
}

// StreamingResponse is the handle to an in-flight provider stream.
//
// Events is the channel of StreamEvent values; it is closed exactly once,
// either after a DoneEvent or after an ErrorEvent. Err returns the terminal
// error (nil if the stream ended with DoneEvent). Body is the underlying
// HTTP response body so providers can attach cleanup hooks; consumers
// should never read it directly.
type StreamingResponse struct {
	Events <-chan StreamEvent

	// err is set before Events is closed and is returned by Err().
	err error

	// body is retained so the provider goroutine can close it on exit.
	body io.ReadCloser
}

// NewStreamingResponse is the constructor used by provider packages.
// It wires the events channel and the underlying HTTP body so the
// provider goroutine can clean up on exit. It is exported because the
// protocol package deliberately hides StreamingResponse internals from
// consumers; only providers are expected to call this.
func NewStreamingResponse(events chan StreamEvent, body io.ReadCloser) *StreamingResponse {
	return &StreamingResponse{Events: events, body: body}
}

// Err returns the terminal error for the stream. It is safe to call after
// the Events channel has been closed. Returns nil on a clean DoneEvent.
func (s *StreamingResponse) Err() error { return s.err }

// SetErr records the terminal error before the Events channel is closed.
// Provider implementations call this from their producer goroutine when
// they hit a non-recoverable failure. Idempotent: only the first non-nil
// error is kept.
func (s *StreamingResponse) SetErr(err error) {
	if s.err == nil && err != nil {
		s.err = err
	}
}

// Close releases any resources held by the stream. It is safe to call
// multiple times. Implementations must drain Events before returning from
// the goroutine that produces them, so consumers do not need to call Close
// during normal use; Close exists for early cancellation paths.
func (s *StreamingResponse) Close() error {
	if s.body != nil {
		return s.body.Close()
	}
	return nil
}
