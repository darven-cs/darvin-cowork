# 统一模型接口抽象设计

## 目标

定义一套统一的接口抽象，使上层 Agent 运行时无需感知底层是 Claude、Gemini 还是 OpenAI。

---

## 核心接口

### ModelProvider 接口

```go
type ModelProvider interface {
    // Name returns the provider name
    Name() string

    // CreateCompletion creates a chat completion
    CreateCompletion(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error)

    // CreateStreamingCompletion creates a streaming chat completion
    CreateStreamingCompletion(ctx context.Context, req *CompletionRequest) (*StreamingResponse, error)

    // ListModels lists available models
    ListModels(ctx context.Context) ([]Model, error)
}
```

---

## 数据结构

### CompletionRequest

```go
type CompletionRequest struct {
    Model    string
    Messages []Message

    // Generation parameters
    Temperature     float32
    MaxTokens       int
    TopP            float32
    TopK            int
    StopSequences  []string

    // Tool support
    Tools    []Tool
    ToolChoice ToolChoice

    // System instruction
    System string

    // Streaming
    Stream bool

    // Provider-specific (if needed)
    Extra map[string]any
}
```

### Message

```go
type Message struct {
    Role    Role
    Content string

    // Tool calls (for assistant messages)
    ToolCalls []ToolCall

    // Tool result (for tool messages)
    ToolCallID string
}

// Role represents message role
type Role string

const (
    RoleSystem    Role = "system"
    RoleUser      Role = "user"
    RoleAssistant Role = "assistant"
    RoleTool      Role = "tool"
)
```

### Tool

```go
type Tool struct {
    Type        string  // "function"
    Name        string
    Description string
    Parameters  ParameterSchema
}

type ParameterSchema struct {
    Type       string
    Properties map[string]ParameterProperty
    Required   []string
}

type ParameterProperty struct {
    Type        string
    Description string
    Enum        []string
}
```

### ToolCall

```go
type ToolCall struct {
    ID       string
    Name     string
    Arguments map[string]any
}
```

### ToolChoice

```go
type ToolChoice struct {
    Type string // "auto", "any", "none", "tool"
    Name string // tool name if Type is "tool"
}
```

### CompletionResponse

```go
type CompletionResponse struct {
    Model      string
    Content    string
    ToolCalls  []ToolCall
    FinishReason FinishReason
    Usage      Usage
}

type FinishReason string

const (
    FinishReasonStop       FinishReason = "stop"
    FinishReasonLength     FinishReason = "length"
    FinishReasonToolCalls  FinishReason = "tool_calls"
    FinishReasonContentFilter FinishReason = "content_filter"
    FinishReasonError      FinishReason = "error"
)

type Usage struct {
    PromptTokens     int
    CompletionTokens int
    TotalTokens      int
}
```

### StreamingResponse

```go
type StreamingResponse struct {
    Events chan StreamEvent
}

type StreamEvent interface {
    isStreamEvent()
}

// TextEvent represents streamed text
type TextEvent struct {
    Delta string
}

func (TextEvent) isStreamEvent() {}

// ToolCallEvent represents a tool call
type ToolCallEvent struct {
    ID        string
    Name      string
    Arguments string // JSON string
}

func (ToolCallEvent) isStreamEvent() {}

// ToolCallArgumentsDeltaEvent represents streaming arguments
type ToolCallArgumentsDeltaEvent struct {
    ID        string
    Delta     string
}

func (ToolCallArgumentsDeltaEvent) isStreamEvent() {}

// DoneEvent signals completion
type DoneEvent struct {
    Response CompletionResponse
}

func (DoneEvent) isStreamEvent() {}

// ErrorEvent signals an error
type ErrorEvent struct {
    Err error
}

func (ErrorEvent) isStreamEvent() {}
```

### Model

```go
type Model struct {
    ID         string
    Name       string
    Provider   string
    ContextWindow int
}
```

---

## Provider 实现映射

| Provider | API Endpoint | Auth Header |
|----------|--------------|-------------|
| Anthropic | `https://api.anthropic.com/v1/messages` | `x-api-key` |
| Gemini | `https://generativelanguage.googleapis.com/v1beta` | `x-goog-api-key` |
| OpenAI | `https://api.openai.com/v1/chat/completions` | `Authorization: Bearer` |

---

## 请求转换规则

### Messages 转换

| 原生格式 | 统一格式 |
|----------|----------|
| OpenAI: `{"role": "user", "content": "..."}` | `Message{Role: "user", Content: "..."}` |
| Claude: `{"role": "user", "content": "..."}` | `Message{Role: "user", Content: "..."}` |
| Gemini: `{"role": "user", "parts": [{"text": "..."}]}` | `Message{Role: "user", Content: "..."}` |

### 工具定义转换

```go
// OpenAI/Gemini format → Unified
func ParseTool(tool map[string]any) Tool {
    if tool["type"] == "function" {
        fn := tool["function"].(map[string]any)
        return Tool{
            Type:        "function",
            Name:        fn["name"].(string),
            Description: fn["description"].(string),
            Parameters:  parseParameters(fn["parameters"].(map[string]any)),
        }
    }
    // Handle other types...
}
```

### 工具调用转换

```go
// OpenAI tool_call → Unified ToolCall
func ParseToolCall(tc map[string]any) ToolCall {
    fn := tc["function"].(map[string]any)
    return ToolCall{
        ID:        tc["id"].(string),
        Name:      fn["name"].(string),
        Arguments: json.RawMessage(fn["arguments"].(string)),
    }
}

// Claude tool_use → Unified ToolCall
func ParseClaudeToolUse(toolUse map[string]any) ToolCall {
    return ToolCall{
        ID:        toolUse["id"].(string),
        Name:      toolUse["name"].(string),
        Arguments: json.RawMessage(toolUse["input"]),
    }
}
```

---

## 实现示例

### Anthropic Provider

```go
type AnthropicProvider struct {
    apiKey string
    client *http.Client
}

func (p *AnthropicProvider) Name() string {
    return "anthropic"
}

func (p *AnthropicProvider) CreateCompletion(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
    payload := map[string]any{
        "model":      req.Model,
        "max_tokens": req.MaxTokens,
        "messages":   convertMessages(req.Messages),
    }

    if req.System != "" {
        payload["system"] = req.System
    }
    if req.Temperature > 0 {
        payload["temperature"] = req.Temperature
    }
    if len(req.Tools) > 0 {
        payload["tools"] = convertTools(req.Tools)
    }

    // ... HTTP request handling
}

func convertMessages(msgs []Message) []map[string]any {
    result := make([]map[string]any, len(msgs))
    for i, m := range msgs {
        result[i] = map[string]any{
            "role":    string(m.Role),
            "content": m.Content,
        }
    }
    return result
}
```

### OpenAI Provider

```go
type OpenAIProvider struct {
    apiKey string
    client *http.Client
}

func (p *OpenAIProvider) Name() string {
    return "openai"
}

func (p *OpenAIProvider) CreateCompletion(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
    payload := map[string]any{
        "model":    req.Model,
        "messages": convertMessages(req.Messages),
    }

    if req.Temperature > 0 {
        payload["temperature"] = req.Temperature
    }
    if req.MaxTokens > 0 {
        payload["max_tokens"] = req.MaxTokens
    }
    if len(req.Tools) > 0 {
        payload["tools"] = convertTools(req.Tools)
    }

    // ... HTTP request handling
}
```

---

## 使用示例

### 创建 Provider

```go
func NewProvider(providerName, apiKey string) (ModelProvider, error) {
    switch providerName {
    case "anthropic":
        return NewAnthropicProvider(apiKey), nil
    case "openai":
        return NewOpenAIProvider(apiKey), nil
    case "gemini":
        return NewGeminiProvider(apiKey), nil
    default:
        return nil, fmt.Errorf("unknown provider: %s", providerName)
    }
}
```

### 调用示例

```go
provider, _ := NewProvider("openai", apiKey)

resp, err := provider.CreateCompletion(ctx, &CompletionRequest{
    Model:    "gpt-4o-mini",
    Messages: []Message{
        {Role: RoleUser, Content: "Hello"},
    },
    MaxTokens:   1024,
    Temperature: 0.7,
})
```

### 流式调用示例

```go
stream, err := provider.CreateStreamingCompletion(ctx, &CompletionRequest{
    Model:    "gpt-4o-mini",
    Messages: []Message{
        {Role: RoleUser, Content: "Hello"},
    },
    Stream: true,
})

for event := range stream.Events {
    switch e := event.(type) {
    case TextEvent:
        fmt.Print(e.Delta)
    case ToolCallEvent:
        fmt.Printf("Tool call: %s(%s)\n", e.Name, e.Arguments)
    case DoneEvent:
        fmt.Printf("\nUsage: %v\n", e.Response.Usage)
    }
}
```

---

## 错误处理

### 统一错误类型

```go
type ProviderError struct {
    Provider   string
    Code       string
    Message    string
    StatusCode int
}

func (e *ProviderError) Error() string {
    return fmt.Sprintf("[%s] %s: %s", e.Provider, e.Code, e.Message)
}

// Error codes
const (
    ErrCodeRateLimit     = "rate_limit_error"
    ErrCodeAuth          = "authentication_error"
    ErrCodeInvalidRequest = "invalid_request_error"
    ErrCodeInternal       = "internal_error"
)
```

---

## 配置

### Provider 配置结构

```go
type ProviderConfig struct {
    Anthropic APIKeyConfig
    OpenAI    APIKeyConfig
    Gemini    APIKeyConfig
}

type APIKeyConfig struct {
    APIKey string
    BaseURL string  // optional, for proxies
}
```
