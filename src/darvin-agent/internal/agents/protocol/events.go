package protocol

// StreamEvent is the unified streaming event emitted by every provider.
//
// The set of concrete event types is intentionally small. Providers translate
// their native SSE / WebSocket frames into these shapes; the Agent loop
// consumes them with a type switch and never touches provider vocabulary.
type StreamEvent interface {
	// isStreamEvent keeps the type closed to this package.
	isStreamEvent()
}

// StartEvent marks the beginning of a streaming response.
// Partial carries the initial AssistantMessage scaffold (model id, empty
// content blocks) so the UI can render placeholders immediately.
type StartEvent struct {
	Partial AssistantMessage
}

// AssistantMessage is the minimal assistant snapshot carried inside
// StartEvent / partial event payloads. It is provider-agnostic and only
// carries what the UI / Agent loop needs to render incrementally.
type AssistantMessage struct {
	Model string
}

// TextDeltaEvent carries an incremental chunk of assistant text.
// Multiple TextDeltaEvents concatenate into the final assistant message.
type TextDeltaEvent struct {
	Delta string
}

// ThinkingDeltaEvent carries an incremental chunk of the model's
// extended-thinking output, kept separate from TextDeltaEvent so the UI
// can render it in a collapsed panel. Providers do not emit it yet.
type ThinkingDeltaEvent struct {
	Delta string
}

// ToolCallStartEvent signals the beginning of a tool invocation.
// The provider guarantees that a matching ToolCallEndEvent with the same
// ID will follow (or an ErrorEvent terminating the stream).
type ToolCallStartEvent struct {
	ID   string
	Name string
}

// ToolCallDeltaEvent carries a fragment of the tool call's argument JSON.
// Concatenated in arrival order, these reproduce the full argument payload.
type ToolCallDeltaEvent struct {
	ID    string
	Delta string
}

// ToolCallEndEvent signals the completion of a tool invocation.
// Arguments is already parsed from the concatenated delta JSON.
type ToolCallEndEvent struct {
	ID        string
	Name      string
	Arguments map[string]any
}

// DoneEvent marks the successful end of the stream. The embedded
// CompletionResponse contains the cumulative usage and final finish reason.
//
// DoneEvent is always the last event on the channel for a successful run.
type DoneEvent struct {
	Response CompletionResponse
}

// ErrorEvent signals an unrecoverable failure during streaming.
// The channel is closed immediately after ErrorEvent; callers should check
// StreamingResponse.Err() to retrieve the error after the channel drains.
type ErrorEvent struct {
	Err error
}

func (StartEvent) isStreamEvent()         {}
func (TextDeltaEvent) isStreamEvent()     {}
func (ThinkingDeltaEvent) isStreamEvent() {}
func (ToolCallStartEvent) isStreamEvent() {}
func (ToolCallDeltaEvent) isStreamEvent() {}
func (ToolCallEndEvent) isStreamEvent()   {}
func (DoneEvent) isStreamEvent()          {}
func (ErrorEvent) isStreamEvent()         {}
