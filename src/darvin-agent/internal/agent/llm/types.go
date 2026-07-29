// Package llm defines the unified LLM client contract for darvin-agent.
//
// It exposes a single ModelProvider interface that abstracts the differences
// between Anthropic, OpenAI and Gemini. Higher layers (Agent loop, Context
// engine, Skills, MCP) consume this contract only and never touch
// provider-specific HTTP / SSE shapes.
package llm

// Role identifies the author of a Message in a conversation.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is the unified conversation item exchanged with any provider.
//
// Content is a plain string for system / user / assistant messages.
// For tool messages (role=tool) the ToolCallID links back to the
// assistant's ToolCall.ID and Arguments carries the JSON-encoded payload.
type Message struct {
	Role    Role
	Content string

	// ToolCalls is populated on assistant messages that requested tools.
	ToolCalls []ToolCall

	// ToolCallID is populated on tool messages to reference the
	// originating ToolCall.ID.
	ToolCallID string
}

// Tool describes a function the model is allowed to invoke.
//
// Parameters follows JSON Schema (Draft 2020-12 subset) and is passed
// through to each provider with provider-specific field renames.
type Tool struct {
	Type        string          // always "function" for now
	Name        string          // unique within a request
	Description string          // shown to the model for selection
	Parameters  ParameterSchema // JSON Schema for the function arguments
}

// ParameterSchema is a minimal JSON Schema subset accepted across providers.
type ParameterSchema struct {
	Type       string                       `json:"type"`
	Properties map[string]ParameterProperty `json:"properties,omitempty"`
	Required   []string                     `json:"required,omitempty"`
}

// ParameterProperty describes a single property inside ParameterSchema.
type ParameterProperty struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Enum        []any  `json:"enum,omitempty"`
	Default     any    `json:"default,omitempty"`
}

// ToolChoice instructs the model on how (or whether) to use tools.
type ToolChoice struct {
	// Type is one of "auto", "any", "none", "tool".
	Type string
	// Name is required when Type == "tool".
	Name string
}

// CompletionRequest is the provider-agnostic call payload.
type CompletionRequest struct {
	Model    string
	Messages []Message

	// Generation parameters.
	Temperature   float32
	MaxTokens     int
	TopP          float32
	TopK          int
	StopSequences []string

	// Tool support.
	Tools      []Tool
	ToolChoice ToolChoice

	// System instruction. Empty string means "no system prompt".
	System string

	// Stream is a hint for providers that distinguish streaming vs. unary
	// endpoints (Anthropic uses it to negotiate SSE).
	Stream bool

	// Extra is an opaque passthrough for provider-specific knobs.
	// Each provider implementation decides which keys it understands.
	Extra map[string]any
}

// CompletionResponse is the unified non-streaming result.
//
// When the model decides to call tools, Content is empty and ToolCalls
// carries one entry per invocation. FinishReason signals the terminating
// condition (normal / length / tool_use / content_filter / error).
type CompletionResponse struct {
	Model        string
	Content      string
	ToolCalls    []ToolCall
	FinishReason FinishReason
	Usage        Usage
}

// ToolCall is a single model-emitted function invocation.
type ToolCall struct {
	ID        string         // provider-issued, unique within a request
	Name      string         // matches Tool.Name
	Arguments map[string]any // parsed JSON object
}

// FinishReason is the normalised stop reason across providers.
type FinishReason string

const (
	FinishReasonStop          FinishReason = "stop"
	FinishReasonLength        FinishReason = "length"
	FinishReasonToolCalls     FinishReason = "tool_calls"
	FinishReasonContentFilter FinishReason = "content_filter"
	FinishReasonError         FinishReason = "error"
	// FinishReasonAborted signals a turn cut short by ctx cancellation
	// (e.g. Agent.Abort or queue.Steer). Synthesised by the executor layer,
	// never returned by providers directly.
	FinishReasonAborted FinishReason = "aborted"
)

// Usage is the minimal token accounting shared by every provider.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int

	CacheReadTokens    int
	CacheWriteTokens   int
	CacheWrite1hTokens int
}

// APIKind names the wire protocol a ModelDescriptor is bound to. Used to
// pick buildRequest / parseResponse / stream parsing at provider boundary.
type APIKind string

const (
	APIAnthropicMessages  APIKind = "anthropic-messages"
	APIOpenAICompletions  APIKind = "openai-completions"
	APIGeminiGenerativeAI APIKind = "google-generative-ai"
)

// InputModality enumerates the input shapes a model can ingest.
type InputModality string

const (
	InputText  InputModality = "text"
	InputImage InputModality = "image"
)

// ThinkingLevel is the unified reasoning-effort level accepted by every
// provider. Providers map it to their native field (budget_tokens,
// reasoning_effort, thinkingBudget) under the hood.
type ThinkingLevel string

const (
	ThinkingOff    ThinkingLevel = "off"
	ThinkingLow    ThinkingLevel = "low"
	ThinkingMedium ThinkingLevel = "medium"
	ThinkingHigh   ThinkingLevel = "high"
	ThinkingMax    ThinkingLevel = "max"
)

// ModelCost holds per-million-token pricing components in USD. CacheRead
// is typically the Input rate discounted; CacheWrite is typically 1.25x
// Input for short-TTL caches.
type ModelCost struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
}

// Compat flags provider-specific capabilities consumed by the higher
// layers (ContextEngine, executor) when deciding what to send.
type Compat struct {
	SupportsToolCalls      bool
	SupportsImageInput     bool
	SupportsUsageInStream  bool
	SupportsStrictToolMode bool
}

// ModelDescriptor is the static metadata for a specific model instance.
// It is registered at provider init() and consumed by the ContextEngine
// (contextWindow / MaxTokens), the budget tracker (Cost) and the executor
// (Compat) without any network call.
type ModelDescriptor struct {
	ID            string
	Name          string
	Provider      string
	APIVersion    APIKind
	ContextWindow int
	MaxTokens     int
	Reasoning     bool
	ThinkingMap   map[ThinkingLevel]string
	Input         []InputModality
	Cost          ModelCost
	Compat        Compat
}
