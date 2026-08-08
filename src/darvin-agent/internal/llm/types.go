// Package llm defines the unified LLM client contract for darvin-agent.
// The shared type model lives in internal/agents/protocol; this package
// re-exports it so existing consumers keep compiling unchanged and adds
// the provider-side pieces (ProviderConfig, the provider factory registry)
// only wiring layers need.
package llm

import "darvin-cowork/backend/internal/agents/protocol"

type Role = protocol.Role

const (
	RoleSystem    Role = protocol.RoleSystem
	RoleUser      Role = protocol.RoleUser
	RoleAssistant Role = protocol.RoleAssistant
	RoleTool      Role = protocol.RoleTool
)

type (
	Message          = protocol.Message
	ImageBlock       = protocol.ImageBlock
	Tool             = protocol.ToolSpec
	ParameterSchema  = protocol.ParameterSchema
	ParameterProperty = protocol.ParameterProperty
	ToolChoice       = protocol.ToolChoice
	CompletionRequest  = protocol.CompletionRequest
	CompletionResponse = protocol.CompletionResponse
	ToolCall         = protocol.ToolCall
	ToolResult       = protocol.ToolResult
	FinishReason     = protocol.FinishReason
	Usage            = protocol.Usage
	APIKind          = protocol.APIKind
	InputModality    = protocol.InputModality
	ThinkingLevel    = protocol.ThinkingLevel
	ModelCost        = protocol.ModelCost
	Compat           = protocol.Compat
	ModelDescriptor  = protocol.ModelDescriptor
)

const (
	FinishReasonStop          FinishReason = protocol.FinishReasonStop
	FinishReasonLength        FinishReason = protocol.FinishReasonLength
	FinishReasonToolCalls     FinishReason = protocol.FinishReasonToolCalls
	FinishReasonContentFilter FinishReason = protocol.FinishReasonContentFilter
	FinishReasonError         FinishReason = protocol.FinishReasonError
	FinishReasonAborted       FinishReason = protocol.FinishReasonAborted

	APIAnthropicMessages  APIKind = protocol.APIAnthropicMessages
	APIOpenAICompletions  APIKind = protocol.APIOpenAICompletions
	APIGeminiGenerativeAI APIKind = protocol.APIGeminiGenerativeAI

	InputText  InputModality = protocol.InputText
	InputImage InputModality = protocol.InputImage

	ThinkingOff    ThinkingLevel = protocol.ThinkingOff
	ThinkingLow    ThinkingLevel = protocol.ThinkingLow
	ThinkingMedium ThinkingLevel = protocol.ThinkingMedium
	ThinkingHigh   ThinkingLevel = protocol.ThinkingHigh
	ThinkingMax    ThinkingLevel = protocol.ThinkingMax
)