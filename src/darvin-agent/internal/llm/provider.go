package llm

import (
	"io"

	"darvin-cowork/backend/internal/agents/protocol"
)

// ModelProvider is the unified LLM client contract.
type ModelProvider = protocol.ModelProvider

// StreamingResponse is the handle to an in-flight provider stream.
type StreamingResponse = protocol.StreamingResponse

// NewStreamingResponse is the constructor used by provider packages; it
// delegates to the protocol package's constructor.
func NewStreamingResponse(events chan StreamEvent, body io.ReadCloser) *StreamingResponse {
	return protocol.NewStreamingResponse(events, body)
}
