package protocol

// StreamEvent is the unified streaming event emitted by every provider.
type StreamEvent interface {
	isStreamEvent()
}

// StartEvent marks the start of a streaming response.
type StartEvent struct {
	Partial AssistantMessage
}

// AssistantMessage is the minimal snapshot carried in StartEvent.
type AssistantMessage struct {
	Model string
}

// TextDeltaEvent carries an incremental chunk of assistant text.
type TextDeltaEvent struct {
	Delta string
}

// ThinkingDeltaEvent carries an incremental chunk of extended-thinking output.
type ThinkingDeltaEvent struct {
	Delta string
}

// ToolCallStartEvent signals the start of a tool invocation.
type ToolCallStartEvent struct {
	ID   string
	Name string
}

// ToolCallDeltaEvent carries a fragment of tool-call argument JSON.
type ToolCallDeltaEvent struct {
	ID    string
	Delta string
}

// ToolCallEndEvent signals completion of a tool invocation.
type ToolCallEndEvent struct {
	ID        string
	Name      string
	Arguments map[string]any
}

// DoneEvent marks the successful end of the stream.
type DoneEvent struct {
	Response CompletionResponse
}

// ErrorEvent signals an unrecoverable failure; the channel closes after.
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