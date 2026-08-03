// Package llm defines the unified LLM client contract for darvin-agent.
//
// It exposes a single ModelProvider interface that abstracts the differences
// between Anthropic, OpenAI and Gemini. Higher layers (Agent loop, Context
// engine, Skills, MCP) consume this contract only and never touch
// provider-specific HTTP / SSE shapes.
//
// The shared type model lives in internal/agents/protocol; this package
// re-exports it so existing consumers keep compiling unchanged, and adds the
// provider-side pieces (ProviderConfig, the provider factory registry) that
// only wiring layers need.
package llm

import "darvin-cowork/backend/internal/agents/protocol"

// Role identifies the author of a Message in a conversation.
type Role = protocol.Role

const (
	RoleSystem    Role = protocol.RoleSystem
	RoleUser      Role = protocol.RoleUser
	RoleAssistant Role = protocol.RoleAssistant
	RoleTool      Role = protocol.RoleTool
)

// Message is the unified conversation item exchanged with any provider.
type Message = protocol.Message

// ImageBlock is a base64-encoded image sent to the provider as an image
// content block.
type ImageBlock = protocol.ImageBlock

// Tool describes a function the model is allowed to invoke (a protocol.ToolSpec).
type Tool = protocol.ToolSpec

// ParameterSchema is a minimal JSON Schema subset accepted across providers.
type ParameterSchema = protocol.ParameterSchema

// ParameterProperty describes a single property inside ParameterSchema.
type ParameterProperty = protocol.ParameterProperty

// ToolChoice instructs the model on how (or whether) to use tools.
type ToolChoice = protocol.ToolChoice

// CompletionRequest is the provider-agnostic call payload.
type CompletionRequest = protocol.CompletionRequest

// CompletionResponse is the unified non-streaming result.
type CompletionResponse = protocol.CompletionResponse

// ToolCall is a single model-emitted function invocation.
type ToolCall = protocol.ToolCall

// ToolResult is the persisted outcome of one tool call.
type ToolResult = protocol.ToolResult

// FinishReason is the normalised stop reason across providers.
type FinishReason = protocol.FinishReason

const (
	FinishReasonStop          FinishReason = protocol.FinishReasonStop
	FinishReasonLength        FinishReason = protocol.FinishReasonLength
	FinishReasonToolCalls     FinishReason = protocol.FinishReasonToolCalls
	FinishReasonContentFilter FinishReason = protocol.FinishReasonContentFilter
	FinishReasonError         FinishReason = protocol.FinishReasonError
	FinishReasonAborted       FinishReason = protocol.FinishReasonAborted
)

// Usage is the minimal token accounting shared by every provider.
type Usage = protocol.Usage

// APIKind names the wire protocol a ModelDescriptor is bound to.
type APIKind = protocol.APIKind

const (
	APIAnthropicMessages  APIKind = protocol.APIAnthropicMessages
	APIOpenAICompletions  APIKind = protocol.APIOpenAICompletions
	APIGeminiGenerativeAI APIKind = protocol.APIGeminiGenerativeAI
)

// InputModality enumerates the input shapes a model can ingest.
type InputModality = protocol.InputModality

const (
	InputText  InputModality = protocol.InputText
	InputImage InputModality = protocol.InputImage
)

// ThinkingLevel is the unified reasoning-effort level accepted by every
// provider.
type ThinkingLevel = protocol.ThinkingLevel

const (
	ThinkingOff    ThinkingLevel = protocol.ThinkingOff
	ThinkingLow    ThinkingLevel = protocol.ThinkingLow
	ThinkingMedium ThinkingLevel = protocol.ThinkingMedium
	ThinkingHigh   ThinkingLevel = protocol.ThinkingHigh
	ThinkingMax    ThinkingLevel = protocol.ThinkingMax
)

// ModelCost holds per-million-token pricing components in USD.
type ModelCost = protocol.ModelCost

// Compat flags provider-specific capabilities consumed by the higher
// layers (ContextEngine, executor) when deciding what to send.
type Compat = protocol.Compat

// ModelDescriptor is the static metadata for a specific model instance.
type ModelDescriptor = protocol.ModelDescriptor
