package protocol

import "encoding/json"

// Role identifies the author of a Message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is the unified conversation item exchanged with any provider.
// For tool messages (role=tool) ToolCallID links back to the
// assistant's ToolCall.ID.
type Message struct {
	Role       Role
	Content    string
	Images     []ImageBlock
	ToolCalls  []ToolCall
	ToolCallID string
	ID         string
	Timestamp  int64
}

// ImageBlock is a base64-encoded image content block. MediaType is the
// image MIME; Data is the raw base64 payload with no `data:` prefix.
type ImageBlock struct {
	MediaType string
	Data      string
}

// ToolSpec describes a function the model is allowed to invoke.
// Parameters is raw JSON Schema, carried end-to-end so no construct
// (anyOf, $ref, nested properties) can be silently truncated.
type ToolSpec struct {
	Type        string
	Name        string
	Description string
	Parameters  json.RawMessage
}

// ParameterSchema is a minimal JSON Schema subset accepted across providers.
type ParameterSchema struct {
	Type                 string                       `json:"type"`
	Properties           map[string]ParameterProperty `json:"properties,omitempty"`
	Required             []string                     `json:"required,omitempty"`
	AdditionalProperties *bool                        `json:"additionalProperties,omitempty"`
}

// ParameterProperty describes one property inside ParameterSchema.
// Minimum / Maximum apply to number / integer, MinLength / MaxLength /
// Pattern to string, Items to array elements.
type ParameterProperty struct {
	Type        string             `json:"type"`
	Description string             `json:"description,omitempty"`
	Enum        []string           `json:"enum,omitempty"`
	Default     any                `json:"default,omitempty"`
	Format      string             `json:"format,omitempty"`
	Minimum     *float64           `json:"minimum,omitempty"`
	Maximum     *float64           `json:"maximum,omitempty"`
	MinLength   *int               `json:"minLength,omitempty"`
	MaxLength   *int               `json:"maxLength,omitempty"`
	Pattern     string             `json:"pattern,omitempty"`
	Items       *ParameterProperty `json:"items,omitempty"`
}

// ToolChoice instructs the model on how (or whether) to use tools.
// Type is one of "auto", "any", "none", "tool"; Name is required when Type=="tool".
type ToolChoice struct {
	Type string
	Name string
}

// CompletionRequest is the provider-agnostic call payload.
type CompletionRequest struct {
	Model    string
	Messages []Message

	Temperature   float32
	MaxTokens     int
	TopP          float32
	TopK          int
	StopSequences []string

	Tools      []ToolSpec
	ToolChoice ToolChoice

	System string

	// Stream hints providers that distinguish streaming vs unary endpoints.
	Stream bool

	// Extra is an opaque passthrough for provider-specific knobs.
	Extra map[string]any
}

// CompletionResponse is the unified non-streaming result. When the model
// decides to call tools, Content is empty and ToolCalls carries one entry
// per invocation.
type CompletionResponse struct {
	Model        string
	Content      string
	ToolCalls    []ToolCall
	FinishReason FinishReason
	Usage        Usage
}

// ToolCall is a single model-emitted function invocation.
type ToolCall struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
	Result    *ToolResult    `json:"result,omitempty"`
}

// ToolResult is the persisted outcome of one tool call.
type ToolResult struct {
	Content string `json:"content"`
	IsError bool   `json:"isError"`
}

// FinishReason is the normalised stop reason across providers.
type FinishReason string

const (
	FinishReasonStop          FinishReason = "stop"
	FinishReasonLength        FinishReason = "length"
	FinishReasonToolCalls     FinishReason = "tool_calls"
	FinishReasonContentFilter FinishReason = "content_filter"
	FinishReasonError         FinishReason = "error"
	// FinishReasonAborted signals a turn cut short by ctx cancellation.
	// Synthesised by the executor layer, never returned by providers directly.
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