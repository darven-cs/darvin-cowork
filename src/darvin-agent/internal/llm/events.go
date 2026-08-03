package llm

import "darvin-cowork/backend/internal/agents/protocol"

// StreamEvent is the unified streaming event emitted by every provider.
type StreamEvent = protocol.StreamEvent

// StartEvent marks the beginning of a streaming response.
type StartEvent = protocol.StartEvent

// AssistantMessage is the minimal assistant snapshot carried inside
// StartEvent / partial event payloads.
type AssistantMessage = protocol.AssistantMessage

// TextDeltaEvent carries an incremental chunk of assistant text.
type TextDeltaEvent = protocol.TextDeltaEvent

// ThinkingDeltaEvent carries an incremental chunk of the model's
// extended-thinking output.
type ThinkingDeltaEvent = protocol.ThinkingDeltaEvent

// ToolCallStartEvent signals the beginning of a tool invocation.
type ToolCallStartEvent = protocol.ToolCallStartEvent

// ToolCallDeltaEvent carries a fragment of the tool call's argument JSON.
type ToolCallDeltaEvent = protocol.ToolCallDeltaEvent

// ToolCallEndEvent signals the completion of a tool invocation.
type ToolCallEndEvent = protocol.ToolCallEndEvent

// DoneEvent marks the successful end of the stream.
type DoneEvent = protocol.DoneEvent

// ErrorEvent signals an unrecoverable failure during streaming.
type ErrorEvent = protocol.ErrorEvent
