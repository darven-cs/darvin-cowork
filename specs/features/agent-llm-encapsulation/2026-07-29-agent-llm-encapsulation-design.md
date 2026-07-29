# Agent LLM 底层封装 — v1 设计文档

> **本文件是 v1 迭代**。v0（`2026-07-28-agent-llm-encapsulation-design.md`）已交付并落地：
> `ModelProvider` 接口（Name/Complete/Stream）、统一数据类型（Role/Message/Tool/ToolChoice/CompletionRequest/CompletionResponse/FinishReason/Usage）、统一错误类型（ProviderError + 4 个错误码）、7 个 StreamEvent 实现、Anthropic provider（含 HTTP 客户端 / 2 次重试 / SSE 解析 / tool_use 增量聚合）、Provider 工厂 + 注册表。
> 本 spec **不复述** v0 已完成项，仅记录本轮要追加的能力与对上游包（executor / event）的连带改动。

---

## 1. 概述

### 1.1 问题 / 背景

v0 把 Anthropic 跑通后，剩余 gap 主要来自两份参考：

1. **`docs/agent/01_LLM_INTERFACE.md`**（OpenClaw 全量抽象）
2. **`docs/agent/api/m1-model-provider.md`**（Go 端 M1 spec）

当前 `internal/agent/llm/` 与两份参考的对照：

| 维度 | v0 已交付 | OpenClaw/M1 还要求 | 本 spec 覆盖 |
|------|----------|-------------------|-------------|
| ModelProvider 方法 | Name / Complete / Stream | + `ListModels` | ✅ FR-1 |
| Model 描述符 | 无 | 完整元数据（contextWindow / maxTokens / cost / reasoning / thinkingLevelMap / compat） | ✅ FR-2 |
| 流事件 | 7 类 | + Thinking 事件（start / delta / end） | ✅ FR-3 |
| Usage 字段 | Prompt / Completion / Total | + CacheRead / CacheWrite / CacheWrite1h / Cost | ✅ FR-4 |
| 请求侧 | 单一 `CompletionRequest` | ThinkingLevel / CacheRetention / SessionID / PromptCacheKey | ✅ FR-5 |
| provider 注册 | anthropic 1 个 | + openai / gemini | ✅ FR-6 |
| 跨模型兼容 | `defaultProviderErrorParser` 已覆盖 3 家 envelope | 跨 provider 消息转换（tool call id 规范化等） | ❌ 留给后续 |

**为什么 v1 必须做**：上层（ContextEngine.Compact / executor 累加 / Failover 链）开始需要 **API 报告的真实 token 拆细**（cache 命中能减预算）与 **成本**（预算告警、provider 选型）；用户切模型时不再丢 thinking 块是 v0 没解决的体验问题；Provider 选型（OpenAI/Gemini）需要 `ListModels` 接口 + Model 描述符，否则 Agent 拿不到 contextWindow 算 token 预算。

### 1.2 目标

v1 在 **不破坏 v0 调用面** 的前提下，向 `internal/agent/llm/` 追加：

1. **Model 描述符 + Registry**：`Model{ID, Name, Provider, API, ContextWindow, MaxTokens, Cost, Reasoning, ThinkingLevelMap, Compat}`，`ModelRegistry` 提供按名查询与 `List()` 遍历，`ModelProvider.ListModels` 返回当前 provider 注册的所有 Model
2. **Thinking 流事件**：3 个新 `StreamEvent` 实现（ThinkingStart / ThinkingDelta / ThinkingEnd），Anthropic provider 实测产出
3. **Cache + Cost 字段**：`Usage` 加 4 个字段（CacheRead/CacheWrite/CacheWrite1h/Cost），Anthropic cache 统计写入 + cost 计算按 Model 描述符
4. **StreamOptions**：`CompletionRequest` 加 `Thinking / CacheRetention / SessionID / PromptCacheKey / Signal` 5 个可选字段，provider 选择性读取
5. **OpenAI Completions provider**：第 2 个 provider，`chat.completions` 协议 + tool_calls 解析
6. **Gemini provider**：第 3 个 provider，`generateContent` 协议 + functionDeclarations 转换
7. **executor / event 配套**：executor 累加 cache + cost 字段；event 包新增 `ThinkingDeltaEvent` 透传类型

### 1.3 非目标

- **不**实现 OpenAI Responses API（与 Completions 协议差异较大，单独 spec）
- **不**实现 Bedrock / Vertex / Azure / Mistral（长尾，留到 v2+）
- **不**实现 WebSocket 传输（仍走 SSE）
- **不**实现 WIF / OAuth / Bearer 短期令牌（Anthropic API Key 仍走 `x-api-key` header；其他 provider 也只走 API Key）
- **不**实现跨模型消息转换（`transformMessages`：图像降级 / 思考签名丢弃 / 工具调用 ID 规范化 / 孤儿工具结果补齐）。这条留在 `messages-transform` 后续 spec。本 spec 的 provider 间 ID 长度限制由各 provider 内部截断
- **不**改 v0 的 ModelProvider 三方法签名（保持兼容）；新方法 `ListModels` 加在 interface 上是允许的（Agent 循环不依赖它，可设为可选）
- **不**改 `cmd/app/main.go` 的装配流程（v0 已经定：本 spec 不动 main.go）
- **不**实现 Failover 链（属于 v0 roadmap 中 P6 的另一桶，单独 spec）

---

## 2. 用户场景

### 场景 1：用户切换 OpenAI 模型

**Given** `config.yaml` 把 `llm.provider` 改为 `openai`，`agent.model` 改为 `gpt-4o-2024-08-06`
**When** Agent 主循环启动
**Then** `llm.NewProvider("openai", cfg)` 返回 `openai.Provider`；`ListModels()` 返回该 provider 已注册的全部 OpenAI Model 描述符；首次 `Complete` / `Stream` 走 OpenAI HTTP 接口

### 场景 2：模型支持 extended thinking

**Given** `req.Thinking = &ThinkingLevel{Level: "high"}`，provider 是 Anthropic Claude Opus 4
**When** `Stream(ctx, req)` 执行
**Then** 流式事件按 `ThinkingStart → ThinkingDelta* → ThinkingEnd → TextDelta* → Done` 顺序产出，`event.ThinkingDeltaEvent` 在 event bus 上被 EventLedger 持久化

### 场景 3：跨 turn 提示缓存命中

**Given** 同 session 第二次发同样 messages（除 user 内容外不变），`req.CacheRetention = "short"`
**When** Anthropic provider 发送请求并接收 `usage.cache_read_input_tokens > 0`
**Then** `Usage.CacheRead` 字段非零；executor 把 CacheRead 累加到 totalUsage；ContextEngine.Compact 在下一轮 token 估算时优先用 `Usage.PromptTokens`（v0 已支持）+ 减去 `CacheRead`（v1 新增）

### 场景 4：成本展示

**Given** `Model.Cost.Input = 3.0`（$/M tokens），`Usage.PromptTokens = 10000`，`Usage.CacheRead = 5000`，`CacheWrite1h = 0`
**When** Provider 在 `DoneEvent.Response.Usage.Cost` 上调用 `calculateCost(model, usage)`（FR-4）
**Then** `Usage.Cost.Input = 0.015`（= 10000 × 3 / 1e6），`Cost.CacheRead = 0.001875`（按 cache 折扣价 = Input 价 × 0.1，可配置），`Cost.Total = 0.016875`

### 场景 5：Gemini 工具调用

**Given** `req.Tools` 包含 1 个 `Tool{Name:"get_weather"}`
**When** Gemini provider 把 Tool 转成 `tools[].functionDeclarations[].name` 上行，并收到 `candidates[].content.parts[].functionCall{name, args}`
**Then** 流事件产出 `ToolCallStart{Name:"get_weather"} → ToolCallDelta{Delta:"{\"locat"} → ToolCallDelta{...} → ToolCallEnd{Arguments: parsed map} → Done`

---

## 3. 功能需求

### FR-1：ModelProvider 加 `ListModels(ctx) ([]ModelDescriptor, error)`

```go
type ModelProvider interface {
    Name() string
    Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error)
    Stream(ctx context.Context, req *CompletionRequest) (*StreamingResponse, error)
    // v1 新增：列出当前 provider 注册的全部 Model 描述符（静态，不发起网络）
    ListModels(ctx context.Context) ([]ModelDescriptor, error)
}
```

`ModelDescriptor`（=OpenClaw 的 `Model` 对象，去 API 化命名）：见 FR-2。

**实现**：anthropic / openai / gemini 各 provider 在构造时挂一份 hard-coded Model 列表（Claude 4 系列 / GPT-4o 系列 / Gemini 2.5 系列等），`ListModels` 返回该列表的副本。**不**调 `models.list` 远程接口（v1 阶段不引入鉴权复杂度）。

**零行为变化**：现有调用方（`executor`）不需要 `ListModels`；如需，未来在 Failover 链 spec 里启用。

### FR-2：`ModelDescriptor` 结构

```go
// ModelDescriptor 描述一个具体模型实例的元数据。
// 来源：docs/agent/01_LLM_INTERFACE.md §Model 对象；docs/agent/api/m1-model-provider.md §Model。
type ModelDescriptor struct {
    ID            string                       // 模型版本标识，如 "claude-sonnet-4-5"
    Name          string                       // 人类可读名，如 "Claude Sonnet 4.5"
    Provider      string                       // "anthropic" / "openai" / "gemini"
    APIVersion    APIKind                      // 见下
    ContextWindow int                          // 上下文窗口 token 数
    MaxTokens     int                          // 最大输出 token 数
    Reasoning     bool                         // 是否支持 extended thinking
    ThinkingMap   map[ThinkingLevel]string     // 思考级别 → 提供商预算值；Reasoning=false 时为空
    Input         []InputModality              // 支持的输入形态：Text / Image
    Cost          ModelCost                    // 输入/输出/缓存读写 $/M tokens
    Compat        Compat                       // 提供商特定兼容性标记（v1 仅 OpenAI 用到）
}

type APIKind string
const (
    APIAnthropicMessages APIKind = "anthropic-messages"   // 对应 m1 spec 的 anthropic
    APIOpenAICompletions APIKind = "openai-completions"   // 对应 m1 spec 的 openai
    APIGeminiGenerativeAI APIKind = "google-generative-ai"
)

type InputModality string
const (
    InputText  InputModality = "text"
    InputImage InputModality = "image"
)

type ThinkingLevel string
const (
    ThinkingOff     ThinkingLevel = "off"
    ThinkingLow     ThinkingLevel = "low"
    ThinkingMedium  ThinkingLevel = "medium"
    ThinkingHigh    ThinkingLevel = "high"
    ThinkingMax     ThinkingLevel = "max"
)

type ModelCost struct {
    Input      float64 // $/M tokens
    Output     float64
    CacheRead  float64 // 通常 = Input * 0.1（Anthropic / OpenAI 折扣）
    CacheWrite float64 // 通常 = Input * 1.25（Anthropic 5min 缓存写入）
}

// Compat 是 OpenClaw 风格 per-API 兼容性标记的 Go 简化版。
// v1 仅 OpenAI 用到（thinkingFormat 等）；Anthropic / Gemini 全 true。
type Compat struct {
    SupportsToolCalls       bool
    SupportsImageInput      bool
    SupportsUsageInStream   bool   // 流式 usage 是否在最后一个 chunk 携带
    SupportsStrictToolMode  bool   // 是否支持 strict JSON Schema
}
```

**ModelRegistry**（独立文件 `internal/agent/llm/model_registry.go`）：

```go
// ModelRegistry 提供 ModelDescriptor 的集中查询。
// 各 provider 在构造时通过 RegisterModel 注册自身支持的 Model；
// Provider.ListModels() 内部委托给 Registry.List(providerName)。
type ModelRegistry struct { /* ... */ }

func NewModelRegistry() *ModelRegistry
func (r *ModelRegistry) RegisterModel(m ModelDescriptor)
func (r *ModelRegistry) Get(id string) (ModelDescriptor, bool)
func (r *ModelRegistry) ListByProvider(name string) []ModelDescriptor
func (r *ModelRegistry) All() []ModelDescriptor
```

进程全局表（`var defaultRegistry = NewModelRegistry()`），provider 的 `init()` 注册默认 Model 集合。

### FR-3：Thinking 流事件

在 `internal/agent/llm/events.go` 加 3 个 StreamEvent 实现 + 在 `isStreamEvent` 上补方法：

```go
// ThinkingStartEvent 标记 extended thinking 块开始；ContentIndex 是 block index。
type ThinkingStartEvent struct {
    ContentIndex int
    Signature    string // 可选：Anthropic 加密思考签名（v0 不存，留 hook）
}

// ThinkingDeltaEvent 携带 thinking 增量；多次累积得完整思考文本。
type ThinkingDeltaEvent struct {
    ContentIndex int
    Delta        string
}

// ThinkingEndEvent 标记 thinking 块结束；携带完整文本 + 可选签名。
type ThinkingEndEvent struct {
    ContentIndex int
    Content      string
    Signature    string // 跨轮次保留
}
```

Anthropic SSE 映射（参考 `m1-anthropic-api.md` + OpenClaw 01_LLM_INTERFACE §1.3）：

| Anthropic SSE event | 统一 StreamEvent |
|---------------------|------------------|
| `content_block_start` (type=thinking) | `ThinkingStartEvent{ContentIndex, Signature}` |
| `content_block_delta` (delta.type=thinking_delta) | `ThinkingDeltaEvent{ContentIndex, Delta}` |
| `content_block_stop` (thinking 块) | `ThinkingEndEvent{ContentIndex, Content, Signature}` |

OpenAI 不原生支持 extended thinking，Completions 模式下 Thinking* 事件不发（`req.Thinking` 被忽略，provider 在 `Extra["reasoning_effort"]` 取值）。Gemini `thinkingConfig` 走 `Extra` 同理。

### FR-4：Usage 加 Cache + Cost 字段

```go
type Usage struct {
    PromptTokens     int
    CompletionTokens int
    TotalTokens      int

    // v1 新增
    CacheReadTokens     int    // 缓存命中读取
    CacheWriteTokens    int    // 5min 缓存写入
    CacheWrite1hTokens  int    // 1h 缓存写入（Anthropic 才有；其他 provider 留 0）
    Cost                UsageCost
}

type UsageCost struct {
    Input      float64 // USD
    Output     float64
    CacheRead  float64
    CacheWrite float64
    Total      float64
}
```

Anthropic provider 在收到 `message_delta.usage` 时填：

```
cache_read_input_tokens  → Usage.CacheReadTokens
cache_creation_input_tokens → Usage.CacheWriteTokens（按 5min / 1h 拆分；
                                   v1 简化：5min 全部走 CacheWriteTokens，1h 单独累加；
                                   Anthropic 不细分 1h 时则 CacheWrite1hTokens = 0）
```

**Cost 计算**（独立函数 `func CalculateCost(m ModelDescriptor, u *Usage) UsageCost`）：

```go
// 与 docs/agent/01_LLM_INTERFACE.md §成本计算 一致：
//   cost.input      = (model.cost.input / 1e6) * usage.prompt_tokens
//   cost.output     = (model.cost.output / 1e6) * usage.completion_tokens
//   cost.cacheRead  = (model.cost.cacheRead / 1e6) * usage.cacheReadTokens
//   cost.cacheWrite = (model.cost.cacheWrite * (cacheWrite - cacheWrite1h)
//                      + model.cost.input * 2 * cacheWrite1h) / 1e6
//   cost.total      = sum(above)
//
// 各 provider 在 DoneEvent 装配阶段调用，Usage 已挂在 CompletionResponse 上。
```

### FR-5：CompletionRequest 加 StreamOptions

```go
type CompletionRequest struct {
    Model    string
    Messages []Message
    Temperature float32
    MaxTokens   int
    TopP        float32
    TopK        int
    StopSequences []string
    Tools      []Tool
    ToolChoice ToolChoice
    System     string
    Stream     bool
    Extra      map[string]any

    // v1 新增（5 个字段均为可选，零值表示"不指定"）
    Thinking        *ThinkingConfig      // nil = 不开启
    CacheRetention  CacheRetention       // "" / "short" / "long"
    SessionID       string               // 缓存亲和性 session 标识
    PromptCacheKey  string               // 显式缓存键
    Signal          context.CancelCauseFunc // nil = 不在请求内额外挂取消；ctx 仍生效
}

type ThinkingConfig struct {
    Level       ThinkingLevel // low/medium/high/max；Level=="off" 等价于 nil
    Budget      int           // 可选：覆盖 ThinkingMap 解析值（按 token 数）
}

type CacheRetention string
const (
    CacheRetentionNone  CacheRetention = ""
    CacheRetentionShort CacheRetention = "short"  // ~5min
    CacheRetentionLong  CacheRetention = "long"   // ~1h
)
```

`Signal` 字段仅在调用方已有 ctx 之外的取消场景使用；一般保持 nil，ctx 已经覆盖大部分场景。该字段存在是为 OpenClaw 风格 `StreamOptions.signal` 的 Go 化对应（v0 没有 cancel cause func 概念，v1 补齐）。

各 provider 读取策略：

| 字段 | Anthropic | OpenAI | Gemini |
|------|-----------|--------|--------|
| Thinking | `thinking{type:"enabled", budget_tokens}`（取 ThinkingMap 或 Budget） | `reasoning_effort` 写入 Extra / 直传 | `thinkingConfig{thinkingBudget}` |
| CacheRetention | `cache_control.type` = "ephemeral"（= short）/ 暂不写 long | 不支持，忽略 | `cachedContent`（v1 不实现） |
| SessionID | `metadata.user_id` 字段 | `user` 字段（OpenAI 安全追踪） | 不支持，忽略 |
| PromptCacheKey | `prompt_cache_key` header | 不支持，忽略 | 不支持，忽略 |

### FR-6：OpenAI Completions provider

目录：`internal/agent/llm/openai/`。`init()` 注册到 default registry。

```go
// provider.go
type Provider struct { /* ... */ }
func New(apiKey string, opts ...Option) *Provider
func (p *Provider) Name() string { return "openai" }
func (p *Provider) Complete(ctx, req) (*CompletionResponse, error)
func (p *Provider) Stream(ctx, req) (*StreamingResponse, error)
func (p *Provider) ListModels(ctx) ([]ModelDescriptor, error)
```

- 端点：`POST {baseURL}/chat/completions`
- 默认 `baseURL = "https://api.openai.com"`
- 请求头：`Authorization: Bearer $API_KEY` + `Content-Type: application/json`
- 请求体：`{model, messages, max_tokens, temperature, top_p, tools, tool_choice, stream, stream_options{include_usage:true}}` （`stream_options.include_usage=true` 才能在最后一个 chunk 拿到 usage）
- 流式事件映射（OpenAI SSE ↔ 统一 StreamEvent）：

| OpenAI chunk | 统一事件 |
|--------------|----------|
| `choices[0].finish_reason` 前的 `delta.content` | `TextDeltaEvent{Delta}` |
| `choices[0].delta.tool_calls[i].function.name`（首帧） | `ToolCallStartEvent{ID, Name}` |
| `choices[0].delta.tool_calls[i].function.arguments` | `ToolCallDeltaEvent{ID, Delta}` |
| `choices[0].finish_reason="stop"` + 末尾 `usage` chunk | `DoneEvent{Response}` |
| `choices[0].finish_reason="tool_calls"` | `DoneEvent{Response}` （FinishReason=tool_calls） |
| HTTP error | `ErrorEvent{Err}` |

OpenAI 不原生支持 thinking：FR-3 的 ThinkingStart/Delta/End 事件不发；如果 req.Thinking 非 nil，provider 把 Level 写到 `reasoning_effort`（按 Compat.SupportsUsageInStream 选）。

注册 Model（hard-coded，开局即用）：

| ID | ContextWindow | MaxTokens | Cost ($/M) | Reasoning |
|----|---------------|-----------|------------|-----------|
| `gpt-4o-2024-08-06` | 128000 | 16384 | in:2.5/out:10 | false |
| `gpt-4o-mini-2024-07-18` | 128000 | 16384 | in:0.15/out:0.6 | false |
| `o3-mini` | 200000 | 100000 | in:1.1/out:4.4 | true（reasoning_effort） |
| `gpt-4-turbo-2024-04-09` | 128000 | 4096 | in:10/out:30 | false |

### FR-7：Gemini provider

目录：`internal/agent/llm/gemini/`。`init()` 注册到 default registry。

```go
type Provider struct { /* ... */ }
func New(apiKey string, opts ...Option) *Provider
func (p *Provider) Name() string { return "gemini" }
func (p *Provider) Complete(ctx, req) (*CompletionResponse, error)
func (p *Provider) Stream(ctx, req) (*StreamingResponse, error)
func (p *Provider) ListModels(ctx) ([]ModelDescriptor, error)
```

- 端点：`POST {baseURL}/v1beta/models/{model}:streamGenerateContent?alt=sse&key={API_KEY}`（流式）；非流式 `:generateContent`
- 默认 `baseURL = "https://generativelanguage.googleapis.com"`
- 鉴权：query string `key=$API_KEY`（也支持 header `x-goog-api-key`，两者选一；v1 选 query string）
- 请求体：`{contents, systemInstruction{parts}, tools[{functionDeclarations}], generationConfig{temperature, maxOutputTokens, topP, topK, stopSequences}}`
- 消息转换：`Message{Role:user} → Content{role:"user", parts:[{text}]}`；`assistant → role:"model"`；`tool → role:"function", parts:[{functionResponse{name, response}}]`
- Tool 转换：`Tool → functionDeclarations[]`，参数直接走 `parametersJsonSchema`（Gemini 原生 JSON Schema）
- 流式事件映射：

| Gemini chunk | 统一事件 |
|--------------|----------|
| `candidates[0].content.parts[].text` | `TextDeltaEvent{Delta}` |
| `candidates[0].content.parts[].functionCall{name}`（首帧） | `ToolCallStartEvent{Name}`（Gemini functionCall 没有 ID，v1 用 `Name + index` 派生 ID：`fmt.Sprintf("gemini-%s-%d", name, index)`，避免 ID 为空） |
| `candidates[0].content.parts[].functionCall.args`（一次性给完整对象） | `ToolCallEndEvent{ID, Name, Arguments}` （不依赖 delta；如有多 functionCall 走多轮 ToolCallStart→ToolCallEnd） |
| `candidates[0].finishReason` | DoneEvent 装配 |
| `usageMetadata` | DoneEvent.Response.Usage 字段 |

注册 Model：

| ID | ContextWindow | MaxTokens | Cost | Reasoning |
|----|---------------|-----------|------|-----------|
| `gemini-2.5-pro` | 1000000 | 64000 | in:1.25/out:5.0 | true（thinkingBudget） |
| `gemini-2.5-flash` | 1000000 | 64000 | in:0.075/out:0.3 | true |
| `gemini-2.0-flash` | 1000000 | 64000 | in:0.1/out:0.4 | false |

### FR-8：executor / event 配套改动

**`internal/agent/executor/executor.go`**（累加循环，约 5 行改动）：

```go
// drainStream 返回值不变，仍为 llm.Usage；
// 但累加循环加 4 行：
totalUsage.CacheReadTokens += turnUsage.CacheReadTokens
totalUsage.CacheWriteTokens += turnUsage.CacheWriteTokens
totalUsage.CacheWrite1hTokens += turnUsage.CacheWrite1hTokens
totalUsage.Cost.Total += turnUsage.Cost.Total
```

**`internal/agent/event/event.go`**（新增透传类型）：

```go
type ThinkingDeltaEvent struct {
    Delta string
}
func (ThinkingDeltaEvent) isAgentEvent()     {}
func (ThinkingDeltaEvent) EventName() string { return "thinking_delta" }
```

executor 的 switch 里加 case `llm.ThinkingDeltaEvent` → emit `event.ThinkingDeltaEvent{Delta:e.Delta}`。ThinkingStart/End 在 v1 不单独上 event bus（它们更适合作为内部累积锚点；UI 后续从 Message 渲染时再读 Session 历史里的 thinking 块即可）。

### FR-9：HTTP 客户端补 Retry-After 解析

v0 重试只重试 429 / 5xx 各 2 次。v1 扩展：

- 解析 `Retry-After` header（Anthropic / OpenAI 都返回），有值时 `sleep(min(Retry-After, maxBackoff))`，默认 `maxBackoff = 30s`
- 4xx 客户端错误（除 429）继续不重试
- 不引入 token bucket；保留 v0 的固定 2 次重试上限

---

## 4. 实现方案

### 4.1 目录结构（v1 增量）

```
src/darvin-agent/internal/agent/llm/
├── provider.go            # v0 不动；新增 ListModels 在 ModelProvider interface
├── types.go               # v0 + Usage 4 字段 + CompletionRequest 5 字段 + ModelDescriptor/Compat 等
├── events.go              # v0 + ThinkingStart/Delta/End 三个事件
├── errors.go              # v0 不动
├── httpclient.go          # v0 + Retry-After 解析
├── registry.go            # v0 不动
├── model_registry.go      # 🆕 ModelRegistry + 全局 defaultRegistry
├── cost.go                # 🆕 CalculateCost + UsageCost
├── compat.go              # 🆕 Compat struct + 默认值
├── anthropic/             # v0 + CacheRead/Write 解析 + Thinking 事件 + ListModels + Model 注册
│   ├── provider.go
│   ├── convert.go
│   └── stream.go
├── openai/                # 🆕 完整 provider
│   ├── provider.go
│   ├── convert.go
│   └── stream.go
└── gemini/                # 🆕 完整 provider
    ├── provider.go
    ├── convert.go
    └── stream.go
```

### 4.2 关键设计决策

#### 4.2.1 兼容性策略：additive only

- `ModelProvider` interface 加方法**会破坏 v0 的 `*AnthropicProvider` 已经实现该接口的事实**：v0 的 anthropic.Provider 只有 3 个方法，v1 加 `ListModels` 后它就不再 satisfy interface。**应对**：本 spec 提交时同时给 anthropic.Provider 补 `ListModels` 方法（FR-1 + FR-6 + FR-7 一并交付，不存在中间态）。
- `Usage` / `CompletionRequest` 加字段是纯 additive（Go 零值兼容），不动既有调用方。
- `StreamEvent` 接口加新事件实现是 additive（既有事件不变），上层 type switch 加 case 即可。

#### 4.2.2 ModelDescriptor 注册时点

硬编码 Model 表放在各 provider 包的 `init()` 里：

```go
// internal/agent/llm/anthropic/provider.go
func init() {
    llm.RegisterProvider("anthropic", New)
    llm.MustRegisterModel(ModelDescriptor{
        ID: "claude-sonnet-4-5", Name: "Claude Sonnet 4.5", Provider: "anthropic",
        APIVersion: llm.APIAnthropicMessages, ContextWindow: 200000, MaxTokens: 8192,
        Reasoning: true,
        ThinkingMap: map[llm.ThinkingLevel]string{
            llm.ThinkingLow: "1024", llm.ThinkingMedium: "4096",
            llm.ThinkingHigh: "8192", llm.ThinkingMax: "16384",
        },
        Input: []llm.InputModality{llm.InputText, llm.InputImage},
        Cost: llm.ModelCost{Input: 3.0, Output: 15.0, CacheRead: 0.3, CacheWrite: 3.75},
        Compat: llm.Compat{SupportsToolCalls: true, SupportsImageInput: true, SupportsUsageInStream: true},
    })
    // ... 其他 Claude 模型
}
```

`_ import` provider 包即触发注册。`cmd/app/main.go` 仍按 v0 风格只 `_ import` anthropic；v1 加 `_ import "darvin-agent/internal/agent/llm/openai"` / `_ import ".../gemini"` 仍不动 main.go（与 v0 §FR-7 一致：本 spec 不修改 main.go）。

#### 4.2.3 Cache token 简化

OpenClaw 区分 `cacheWrite` 5min vs `cacheWrite1h`（1h 价 = input × 2）。v1 简化：

- Anthropic provider 拿到 `cache_creation_input_tokens` 后，按 `Extra["anthropic.cache_ttl"]`（字符串 `"5m"` / `"1h"`）分流：
  - `"5m"` → `Usage.CacheWriteTokens +=`（5min）
  - `"1h"` → `Usage.CacheWrite1hTokens +=`（1h）
  - 未指定 → 默认 5min
- 其他 provider 不区分，写入 `CacheWriteTokens`，`CacheWrite1hTokens = 0`
- `CalculateCost` 按 FR-4 公式严格区分

#### 4.2.4 Gemini functionCall ID 派生

Gemini `functionCall` 没有 ID 字段（与 Anthropic `tool_use.id` / OpenAI `tool_calls[i].id` 不同）。v1 派生规则：

```go
func deriveGeminiToolCallID(name string, index int) string {
    h := sha1.Sum([]byte(fmt.Sprintf("%s|%d", name, index)))
    return fmt.Sprintf("gemini-%s-%x", name, h[:4])
}
```

8 字符前缀 + 8 字符 hash 后缀，足够唯一且满足 Anthropic / OpenAI 的 ≤64 字符限制。

#### 4.2.5 计算 Cost 的位置

每个 provider 在组装 `DoneEvent.Response` 之前调用：

```go
// anthropic/stream.go DoneEvent 装配前
m, _ := llm.GetModel(req.Model) // ModelRegistry 查询
if m != nil {
    llm.CalculateCost(m, &resp.Usage)
}
events <- llm.DoneEvent{Response: resp}
```

`llm.GetModel(id)` 查不到时（用户传了未知 model 字符串），不计算 Cost，`Usage.Cost` 留零（不报错）。

#### 4.2.6 context.CancelCauseFunc 用法

`CompletionRequest.Signal` 字段为 `context.CancelCauseFunc`（即 `context.WithCancelCause` 的回调），调用方需要时可以注册跨协程取消原因。本 spec **不**强制要求使用，保持 nil 与 v0 行为等价。Agent 循环目前只用 `ctx`，不读 `req.Signal`；该字段留 hook 给后续 Failover 链 spec（failover 重试场景需要"为什么切 provider"的 cause）。

### 4.3 关键代码骨架

```go
// internal/agent/llm/openai/stream.go 核心循环（与 anthropic 类似，省略）
func newStream(ctx, ...) (*StreamingResponse, error) {
    body, err := hc.DoStream(ctx, url, headers, payload)
    if err != nil { return nil, err }
    events := make(chan llm.StreamEvent, 16)
    go func() {
        defer close(events)
        defer body.Close()
        toolBuffers := map[int]*toolAccum{}
        var finalUsage llm.Usage
        var respModel string

        for scanner.Scan() {
            // SSE: "data: {...}\n\n" (OpenAI 不发 event: 行)
            // 解析 chunk.choices[0]：
            //   - delta.content           → TextDeltaEvent{Delta}
            //   - delta.tool_calls[i]     → ToolCallStart / ToolCallDelta（按 index 聚合）
            //   - finish_reason           → 触发 DoneEvent 装配
            // 末尾 chunk 含 usage（因 stream_options.include_usage=true）→ finalUsage
        }
        if scanner.Err() != nil {
            events <- llm.ErrorEvent{Err: scanner.Err()}
            return
        }
        events <- llm.DoneEvent{Response: llm.CompletionResponse{
            Model: respModel, ToolCalls: ..., FinishReason: ..., Usage: finalUsage,
        }}
    }()
    return llm.NewStreamingResponse(events, body), nil
}
```

```go
// internal/agent/llm/cost.go
func CalculateCost(m ModelDescriptor, u *Usage) UsageCost {
    var c UsageCost
    c.Input      = m.Cost.Input      / 1e6 * float64(u.PromptTokens)
    c.Output     = m.Cost.Output     / 1e6 * float64(u.CompletionTokens)
    c.CacheRead  = m.Cost.CacheRead  / 1e6 * float64(u.CacheReadTokens)
    cacheWrite5m := u.CacheWriteTokens - u.CacheWrite1hTokens
    c.CacheWrite = (m.Cost.CacheWrite*float64(cacheWrite5m) +
                    m.Cost.Input*2*float64(u.CacheWrite1hTokens)) / 1e6
    c.Total = c.Input + c.Output + c.CacheRead + c.CacheWrite
    u.Cost = c
    return c
}
```

### 4.4 测试策略

延续 v0 风格：

- `internal/agent/llm/types_test.go`：Usage / CompletionRequest 字段 round-trip
- `internal/agent/llm/cost_test.go`：CalculateCost 各场景（5min/1h cache 拆分、cache 命中折扣、零 cache 边界）
- `internal/agent/llm/model_registry_test.go`：Register / Get / ListByProvider / All 行为；重复 ID panic
- `internal/agent/llm/openai/convert_test.go`：消息 / tool 转换；functionCall 派生 ID 稳定
- `internal/agent/llm/openai/stream_test.go`：chunk fixture 解析；tool_calls 多 index 聚合
- `internal/agent/llm/gemini/convert_test.go`：messages → contents；tool → functionDeclarations
- `internal/agent/llm/gemini/stream_test.go`：chunk fixture 解析；functionCall ID 派生
- `internal/agent/llm/anthropic/stream_test.go`：**扩展** fixture：thinking 块、cache token 解析
- `internal/agent/executor/executor_test.go`：**扩展** fixture：Usage 含 cache 字段时累加正确；LLMEndEvent.Usage 字段透传

仓库当前没有 `go test` runner（`AGENTS.md` §测试），本 spec 落地测试代码但不要求 CI 跑通。后续 spec 启用 go test runner 时直接使用。

### 4.5 依赖

v0 已用：`net/http` / `encoding/json` / `bufio` / `strings` / `strconv` / `errors` / `context` / `time` / `crypto/sha1`（v1 新增，仅 gemini 用）/ `internal/logger`。

**不**新增第三方依赖。Anthropic SDK / OpenAI SDK 暂不引入，避免与 v0 自实现 HTTP 客户端冲突；后续若要迁 SDK 单独 spec。

---

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| `req.Model` 字符串在 ModelRegistry 查不到 | provider 不报 error，继续发请求；CalculateCost 跳过（Cost 留零），DoneEvent 仍正常产出 |
| Anthropic 流含 thinking 但 `req.Thinking == nil` | provider 不读 thinking 块（按 v0 行为忽略 content_block_start type=thinking），仍正常产出文本 / tool |
| `req.Thinking` 非 nil 但 provider 不支持（OpenAI Completions） | provider 写 `reasoning_effort` 进 request body；不产 ThinkingStart/Delta/End 事件（OpenAI 不流式 thinking） |
| OpenAI `stream_options.include_usage=false`（用户自定义） | 末 chunk 无 usage，Usage.PromptTokens 等留零；CalculateCost 因 prompt=0 结果 0 |
| Gemini functionCall 同一 turn 多次出现 | 按 `parts[]` 顺序逐个发 ToolCallStart → ToolCallEnd（OpenClaw 风格） |
| OpenAI 4xx 不在 `defaultProviderErrorParser` 覆盖 | HTTP client 返回 raw body 给 provider；provider 按 OpenAI `{error:{message}}` envelope 自己解析，仍 wrap 进 `*ProviderError{Code: ErrCodeInvalidRequest}` |
| Cache token 5min 与 1h 同时为 0（绝大多数 provider） | CalculateCost.CacheWrite = 0；Cost.Total 仅含 Input + Output + CacheRead |
| Model 描述符重复 ID 注册 | `MustRegisterModel` panic（与 `RegisterProvider` 的语义一致） |
| 用户取消 ctx | 流式 channel 立刻关闭；`Err()` 返回 `context.Canceled`；v0 行为不变 |
| 工具调用 ID 在多 provider 切换时长度 > 64 | v1 不做转换（FR-1.3 非目标）；切换 provider 时调用方需自行处理。本 spec 在文档中加 NOTE 提醒 |

---

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/darvin-agent/internal/agent/llm/provider.go` | 修改：ModelProvider interface 加 ListModels 方法 |
| `src/darvin-agent/internal/agent/llm/types.go` | 修改：Usage 加 4 字段；CompletionRequest 加 5 字段；新增 ModelDescriptor / ThinkingConfig / CacheRetention / ThinkingLevel / APIKind / InputModality / ModelCost / Compat / UsageCost |
| `src/darvin-agent/internal/agent/llm/events.go` | 修改：新增 ThinkingStartEvent / ThinkingDeltaEvent / ThinkingEndEvent + isStreamEvent 方法 |
| `src/darvin-agent/internal/agent/llm/httpclient.go` | 修改：429 重试时读 Retry-After header，按其 sleep |
| `src/darvin-agent/internal/agent/llm/model_registry.go` | 🆕 ModelRegistry + defaultRegistry + RegisterModel / MustRegisterModel / Get / ListByProvider / All |
| `src/darvin-agent/internal/agent/llm/cost.go` | 🆕 CalculateCost + UsageCost |
| `src/darvin-agent/internal/agent/llm/compat.go` | 🆕 Compat 默认值（DefaultAnthropicCompat / DefaultOpenAICompat / DefaultGeminiCompat） |
| `src/darvin-agent/internal/agent/llm/anthropic/provider.go` | 修改：实现 ListModels；init() 注册 Model 描述符 |
| `src/darvin-agent/internal/agent/llm/anthropic/stream.go` | 修改：thinking 块识别；cache token 解析；DoneEvent 装配前调用 CalculateCost |
| `src/darvin-agent/internal/agent/llm/anthropic/convert.go` | 修改：payload 支持 thinking 块 / cache_control；ThinkingMap → budget_tokens 转换 |
| `src/darvin-agent/internal/agent/llm/openai/provider.go` | 🆕 Provider + Name/Complete/Stream/ListModels |
| `src/darvin-agent/internal/agent/llm/openai/convert.go` | 🆕 消息 / tool 转换；reasoning_effort 注入 |
| `src/darvin-agent/internal/agent/llm/openai/stream.go` | 🆕 SSE chunk 解析；tool_calls index 聚合 |
| `src/darvin-agent/internal/agent/llm/gemini/provider.go` | 🆕 Provider + Name/Complete/Stream/ListModels |
| `src/darvin-agent/internal/agent/llm/gemini/convert.go` | 🆕 messages → contents；Tool → functionDeclarations |
| `src/darvin-agent/internal/agent/llm/gemini/stream.go` | 🆕 SSE chunk 解析；functionCall ID 派生；usageMetadata 解析 |
| `src/darvin-agent/internal/agent/event/event.go` | 修改：新增 ThinkingDeltaEvent 透传类型 |
| `src/darvin-agent/internal/agent/executor/executor.go` | 修改：累加循环加 4 行（cache + cost）；switch 加 case llm.ThinkingDeltaEvent |
| `src/darvin-agent/internal/agent/executor/executor_test.go` | 修改：新增 fixture 用例覆盖 cache + cost |
| `specs/features/agent-llm-encapsulation/2026-07-29-agent-llm-encapsulation-design.md` | 🆕 本 spec 文件 |

**不修改**：
- `cmd/app/main.go`：v0 §FR-7 已约定不动；新 provider 通过 `_ import` 触发 init
- `internal/agent/llm/errors.go`：v0 已完整
- `internal/agent/llm/registry.go`：v0 已完整
- `internal/agent/agent.go`：Usage / StreamEvent 字段为 additive，不改调用方
- `internal/agent/ctxengine/*`：Compact / Assemble 仍只读 `Usage.PromptTokens`，Cache 字段暂不喂入（留 hook 给后续 spec）

不涉及：renderer、preload、`src/main/runtime/`、`src/main/index.ts`、Tailwind / Vue 栈。

---

## 7. 验收标准

**通用**：
- [ ] `cd src/darvin-agent && go build ./...` 编译通过
- [ ] `go vet ./...` 无警告
- [ ] `gofmt -l .` 干净
- [ ] 既有调用面（`executor.RunConversation`、`agent.Agent` 主循环、`ctxengine` Compact 读 PromptTokens）**不**回归
- [ ] `internal/agent/llm/anthropic/stream.go` 既有 v0 测试不回归

**FR-1 ListModels**：
- [ ] `ModelProvider` interface 加 `ListModels(ctx) ([]ModelDescriptor, error)` 方法
- [ ] anthropic / openai / gemini 三家 provider 全部实现 ListModels
- [ ] `ModelRegistry.Get("claude-sonnet-4-5")` 返回非空 ModelDescriptor
- [ ] 重复 ID 注册 panic

**FR-2 ModelDescriptor**：
- [ ] `ModelDescriptor` 暴露至少 9 个字段（ID/Name/Provider/APIVersion/ContextWindow/MaxTokens/Reasoning/ThinkingMap/Input/Cost/Compat）
- [ ] `ThinkingMap[ThinkingHigh]` 是 string 数值（"8192"），Reasoning=false 时整张表为空
- [ ] `ModelCost` 4 个字段都是 `$/M tokens` 单位
- [ ] `Compat` 4 个 bool 字段均有合理默认值（DefaultAnthropicCompat 全 true，DefaultOpenAICompat 的 SupportsStrictToolMode=false）

**FR-3 Thinking 事件**：
- [ ] `ThinkingStartEvent{ContentIndex, Signature}` / `ThinkingDeltaEvent{ContentIndex, Delta}` / `ThinkingEndEvent{ContentIndex, Content, Signature}` 三类事件均实现 `isStreamEvent()`
- [ ] Anthropic provider fixture（`anthropic/stream_test.go`）：含 thinking 块的 SSE 样本能产出完整 `ThinkingStart → ThinkingDelta* → ThinkingEnd` 序列
- [ ] OpenAI / Gemini provider 不产 Thinking 事件（fixture 验证）

**FR-4 Cache + Cost**：
- [ ] `Usage` 加 4 字段（CacheReadTokens / CacheWriteTokens / CacheWrite1hTokens / Cost）
- [ ] `CalculateCost` 函数：输入 fixture `(model=claude-sonnet-4-5, prompt=10000, completion=1000, cacheRead=5000, cacheWrite=2000, cacheWrite1h=0)` 算出 `cost.input=0.03, cost.output=0.015, cost.cacheRead=0.0015, cost.cacheWrite=0.0075, cost.total=0.0535`
- [ ] Anthropic provider 在 DoneEvent.Response.Usage.Cost 上写入计算结果
- [ ] executor.go 的 totalUsage 累加循环把 cache + cost 4 字段一并累加

**FR-5 StreamOptions**：
- [ ] `CompletionRequest` 加 5 字段（Thinking / CacheRetention / SessionID / PromptCacheKey / Signal）
- [ ] `req.Thinking == nil` 时 Anthropic 不发 thinking 块（v0 行为不变）
- [ ] `req.Thinking.Level == "high"` 时 Anthropic payload 含 `thinking{budget_tokens: "8192"}`
- [ ] `req.CacheRetention == "short"` 时 Anthropic payload 在 system / 末尾 message 上含 `cache_control{type:"ephemeral"}`
- [ ] `req.Signal == nil` 时调用行为与 v0 一致

**FR-6 OpenAI provider**：
- [ ] `init()` 注册到 default registry
- [ ] `Name() == "openai"`
- [ ] `ListModels()` 至少返回 4 个 Model 描述符（gpt-4o / gpt-4o-mini / o3-mini / gpt-4-turbo）
- [ ] 流式 fixture：含 tool_calls 多 index 的 SSE 样本能产出 `ToolCallStart → ToolCallDelta* → ToolCallEnd`（每个 index 一组）
- [ ] 错误响应 fixture：HTTP 401 / 429 / 500 各映射到对应 ProviderError code

**FR-7 Gemini provider**：
- [ ] `init()` 注册到 default registry
- [ ] `Name() == "gemini"`
- [ ] `ListModels()` 至少返回 3 个 Model 描述符（gemini-2.5-pro / gemini-2.5-flash / gemini-2.0-flash）
- [ ] functionCall ID 派生：同一 (name, index) 多次调用产出相同 ID
- [ ] usageMetadata 解析填入 Usage.CacheReadTokens 等（Gemini `cachedContentTokenCount` 对应 CacheReadTokens）

**FR-8 executor / event 配套**：
- [ ] `event.ThinkingDeltaEvent` 实现 `isAgentEvent()`，EventName() == "thinking_delta"
- [ ] executor 的 switch case `llm.ThinkingDeltaEvent` 触发 emit `event.ThinkingDeltaEvent{Delta:e.Delta}`
- [ ] executor 的 totalUsage 在 DoneEvent 含 cache 字段时仍正确累加
- [ ] `executor_test.go` 新增 fixture：assistant turn 含 cache + cost，验证 LLMEndEvent.Usage 字段透传

**FR-9 Retry-After**：
- [ ] `httpclient.go` 解析 Retry-After header（数字秒数）
- [ ] 429 重试间隔 = `min(Retry-After, 30s)`（默认上限）
- [ ] 解析失败回退到 v0 的 1s/2s 指数退避

**集成手测路径**（v0 §4.4 风格的扩展）：
```bash
export OPENAI_API_KEY=sk-...
# 临时改 cmd/app/main.go:
#   llm.DefaultModelRegistry().Get("gpt-4o-2024-08-06")
#   provider, _ := llm.NewProvider(ctx, "openai", llm.ProviderConfig{APIKey: os.Getenv("OPENAI_API_KEY")})
#   stream, _ := provider.Stream(ctx, &llm.CompletionRequest{
#       Model: "gpt-4o-2024-08-06",
#       Messages: []llm.Message{{Role: llm.RoleUser, Content: "ping"}},
#       MaxTokens: 64,
#   })
#   for ev := range stream.Events { fmt.Printf("%T %+v\n", ev, ev) }
go build ./... && ./bin/darvin-agent-$(go env GOOS)-$(go env GOARCH)
```

---

## 8. 后续 spec 候选（不在本 spec 范围内）

| 候选 | 内容 | 优先级 |
|------|------|--------|
| `messages-transform` | 跨 provider 消息转换（图像降级 / 思考签名丢弃 / tool_call id 规范化 / 孤儿工具结果补齐）；OpenClaw `AiTransportHost.transformTransportMessages` 的 Go 化 | 高（Failover 链前置） |
| `openai-responses-provider` | OpenAI 新版 Responses API + tool call ID 规范化（`fc_xxx|rs_xxx` → ≤64 字符） | 中（gpt-5/4o-reasoning 等新模型） |
| `failover-chain` | 主 provider 失败自动切备用 + 熔断器；复用 ModelRegistry 做 provider 选型 | 高（v0 roadmap P6） |
| `thinking-and-cache-quota` | thinking budget 与 cache retention 的全局配额；与 Failover 链联动 | 中 |
| `bedrock-vertex-azure` | 三个长尾 provider | 低 |
| `agent-loop-ws-protocol` | electron 子进程接入 IPC（已与 P8 网关合并，本 spec 仍不动 main.go） | 由 `docs/plan/agent-package-roadmap.md` 主导 |
| `memory-system` | 记忆系统 / Dreaming 三阶段 | 由 `docs/agent/03_MEMORY_SYSTEM.md` 主导 |
| `skills-system` | Skills 四层加载 | 由 `docs/agent/04_SKILLS_SYSTEM.md` 主导 |

每个后续 spec 都按 v0 + v1 的接口约定（`ModelProvider` / `StreamEvent` / `ModelDescriptor` / `Usage`）扩展，不在本 spec 内提前暴露。

---

## 9. 与 v0 spec 的关系

本 spec 是 v0（`2026-07-28-agent-llm-encapsulation-design.md`）的迭代，**不替代** v0：

- **继承**：v0 §FR-1~§FR-7 全部交付内容 + §9 实现偏差中记录的 9 处微调（struct 字段 Events、FinishReasonAborted、Tool.Type、Logger 接口注入、defaultProviderErrorParser、backoff 数组、IsCode / NewProviderError、ToolCall 值类型、cmd/llm-smoke 删除）
- **扩展**：本 spec 加 FR-1~FR-9 九节，对应 ModelDescriptor / Thinking / Cache+Cost / StreamOptions / OpenAI / Gemini / executor-event / Retry-After
- **不修改**：v0 §6 涉及的 main.go / logger / config / database（保持 v0 "本 spec 不动 main.go" 的承诺）
- **回归保护**：v0 §7 验收标准中关于 "anthropic provider 流式产出 Start → TextDelta* → Done" 的部分必须仍然通过（本 spec 只新增事件类型，不修改既有事件类型）

v0 + v1 合并后的 `internal/agent/llm/` 对外契约：

```go
type ModelProvider interface {
    Name() string
    Complete(ctx, *CompletionRequest) (*CompletionResponse, error)
    Stream(ctx, *CompletionRequest) (*StreamingResponse, error)
    ListModels(ctx) ([]ModelDescriptor, error)  // v1
}

// StreamEvent: 7 类（v0）+ 3 类（v1）= 10 类
// Usage: 3 字段（v0）+ 4 字段（v1）= 7 字段
// CompletionRequest: 12 字段（v0）+ 5 字段（v1）= 17 字段
// Provider 注册: 1（v0）+ 2（v1）= 3 个
```

后续 spec 在此基础上继续扩展（Failover 链 / 消息转换 / 长尾 provider），不再回退到 v0 形态。