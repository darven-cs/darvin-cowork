package llm

import "darvin-cowork/backend/internal/agents/protocol"

type (
	StreamEvent          = protocol.StreamEvent
	StartEvent           = protocol.StartEvent
	AssistantMessage     = protocol.AssistantMessage
	TextDeltaEvent       = protocol.TextDeltaEvent
	ThinkingDeltaEvent   = protocol.ThinkingDeltaEvent
	ToolCallStartEvent   = protocol.ToolCallStartEvent
	ToolCallDeltaEvent   = protocol.ToolCallDeltaEvent
	ToolCallEndEvent     = protocol.ToolCallEndEvent
	DoneEvent            = protocol.DoneEvent
	ErrorEvent           = protocol.ErrorEvent
)