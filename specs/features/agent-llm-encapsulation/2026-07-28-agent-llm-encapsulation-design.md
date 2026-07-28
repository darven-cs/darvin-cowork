# Agent LLM 底层封装设计文档

## 1. 概述

### 1.1 问题 / 背景

darvin-agent（`src/darvin-agent/`）当前仅含 README 占位，Agent 运行时所有业务都需在 Go 侧落地。其中 **LLM 客户端是 Agent 循环的最底层依赖**，上层 Agent 调度、上下文引擎、记忆、技能、MCP 都要通过它驱动模型。

直接面向 OpenAI / Anthropic / Gemini 三家差异巨大的 HTTP API 写代码会带来：

1. **路由膨胀**：每接一个提供商就要复制一遍请求构造、SSE 解析、tool call 解析、错误码映射，后续 provider 越多越乱。
2. **流式协议碎片化**：OpenAI 是 `chat.completions` 增量 delta，Anthropic 是 `content_block_delta`，Gemini 是 `parts[]`，没有统一抽象上层就接不住。
3. **工具调用格式分裂**：OpenAI 的 `function_call.arguments` 是 JSON 字符串增量流式；Anthropic 是 `input` 完整对象；Gemini 是 `functionCall.args`。Agent 循环需要一个统一的 `ToolCall` 流才能落地。
4. **思考能力（reasoning / extended thinking）**：Anthropic、Gemini、OpenAI 各自有 thinking 字段和签名机制，模型切换时不能丢。
5. **跨模型兼容**：同一会话里切换模型时，工具调用 ID 长度、思考签名、图像输入等需要做规范化（见 `docs/agent/01_LLM_INTERFACE.md` 的 transformMessages）。

`docs/plan/m1-model-provider.md` 已经给出第一版统一接口草图，但停留在示例代码层；`docs/agent/01_LLM_INTERFACE.md` 是参考实现的完整设计参考。本 spec 在两者基础上收敛出 darvin-agent **当前阶段**需要落地的最小可用封装。

### 1.2 目标

在 `src/darvin-agent/` 下建立 LLM 客户端统一封装层，提供：

1. **统一的 Go 接口** `ModelProvider`，屏蔽 OpenAI / Anthropic / Gemini 的差异
2. **统一的消息 / 工具 / 流式事件类型**，让上层 Agent 循环不再感知 provider
3. **第一个可用的 provider 实现（Anthropic）**，含非流式与流式两种调用
4. **统一的错误类型 + 限流 / 重试骨架**，后续 provider 共用

后续阶段再补 OpenAI / Gemini 的 provider 实现和思考签名、跨模型消息转换等高级能力。

### 1.3 非目标

- **不**实现 Agent 主循环（`runtime.go` / `agent.go`）、上下文引擎、记忆、技能、MCP —— 那些是后续 spec
- **不**实现 OpenAI / Gemini provider —— 本 spec 只交付 Anthropic provider + 抽象层；后续单独 spec 跟进
- **不**实现成本计算、限流、缓存、prompt 缓存键策略 —— 留到性能优化阶段
- **不**实现工具调用 ID 跨模型规范化 —— 留到接入第二个 provider 之后再做（单 provider 时没必要）
- **不**实现 WebSocket 传输、Azure Bedrock Vertex 等长尾变体

---

## 2. 用户场景

### 场景 1：Agent 主循环调用一次非流式补全

**Given** darvin-agent 启动后已根据配置加载 Anthropic provider
**When** Agent 主循环发起一次 `Complete(ctx, req)` 调用
**Then** provider 返回结构化的 `CompletionResponse`（含 content / tool_calls / usage / finish_reason），Agent 主循环无须关心 SSE 与 HTTP 细节

### 场景 2：Agent 主循环订阅一次流式响应

**Given** 同一个 provider 实例
**When** Agent 主循环调用 `Stream(ctx, req)` 并 `for ev := range events`
**Then** 按顺序收到 `Start → TextDelta* → (ToolCallStart / ToolCallDelta)* → Done`，事件类型与 provider 解耦，UI 层可消费统一的 `TextDelta` 与 `ToolCallDelta`

### 场景 3：模型返回工具调用

**Given** 请求中携带了 tools 定义
**When** 模型决定调用 `get_weather(location)`
**Then** 流式事件里先发 `ToolCallStart{ID,Name}`，随后若干 `ToolCallDelta{Delta}` 增量拼接参数 JSON，最后 `ToolCallEnd{ID,Name,Arguments(map)}`，Agent 主循环用 `tool_use_id` 派发到工具注册表

### 场景 4：遇到 rate_limit / 网络错误

**Given** provider 返回 429 或网络中断
**When** provider 内部执行重试
**Then** 重试耗尽后抛出带错误码（`rate_limit_error` / `authentication_error` / `invalid_request_error` / `internal_error`）的统一 `*ProviderError`，Agent 层按错误码决定是否中断 / 退避 / 上报

---

## 3. 功能需求

### FR-1：统一 ModelProvider 接口

定义 `internal/agent/llm/provider.go`：

```go
type ModelProvider interface {
    Name() string
    Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error)
    Stream(ctx context.Context, req *CompletionRequest) (*StreamingResponse, error)
}
```

`Stream` 返回的 `*StreamingResponse` 暴露 `Events <-chan StreamEvent` 字段（**字段，不是方法**）与 `Err() error` 方法。调用方通过 `for ev := range sr.Events()` 消费事件；`sr.Err()` 在 channel 关闭后返回最后一次错误。

### FR-2：统一数据类型

`internal/agent/llm/types.go` 定义：

- `Role`：常量 `RoleSystem / RoleUser / RoleAssistant / RoleTool`
- `Message`：含 `Role / Content / ToolCalls []*ToolCall / ToolCallID string`
- `Tool`：含 `Name / Description / Parameters ParameterSchema`（JSON Schema）
- `ToolChoice`：含 `Type("auto"|"any"|"none"|"tool") / Name`
- `CompletionRequest`：含 `Model / Messages / Temperature / MaxTokens / TopP / TopK / StopSequences / Tools / ToolChoice / System / Stream / Extra`
- `CompletionResponse`：含 `Model / Content / ToolCalls / FinishReason / Usage`
- `FinishReason`：`stop / length / tool_calls / content_filter / error`，另在 agent-loop 落地后追加 `aborted`（executor 合成，标识被 Agent.Abort 或 queue.Steer 中断的轮次，**不再由 provider 返回**）
- `Usage`：`PromptTokens / CompletionTokens / TotalTokens`
- `ParameterSchema` + `ParameterProperty`：JSON Schema 子集
- `StreamEvent` 接口：`StartEvent{Partial AssistantMessage} / TextDeltaEvent{Delta} / ToolCallStartEvent{ID,Name} / ToolCallDeltaEvent{ID,Delta} / ToolCallEndEvent{ID,Name,Arguments} / DoneEvent{Response CompletionResponse} / ErrorEvent{Err}`

### FR-3：统一错误类型

`internal/agent/llm/errors.go` 定义：

```go
type ProviderError struct {
    Provider   string
    Code       string  // 见下表
    Message    string
    StatusCode int
    Cause      error
}
func (e *ProviderError) Error() string
func (e *ProviderError) Unwrap() error
```

错误码常量：

```go
const (
    ErrCodeRateLimit      = "rate_limit_error"
    ErrCodeAuth           = "authentication_error"
    ErrCodeInvalidRequest = "invalid_request_error"
    ErrCodeInternal       = "internal_error"
)
```

### FR-4：Anthropic provider 实现

`internal/agent/llm/anthropic/provider.go` + `convert.go` + `stream.go`，实现 `ModelProvider`：

- 端点：`POST {baseURL}/v1/messages`
- 默认 `baseURL = "https://api.anthropic.com"`
- 请求头：`x-api-key: $API_KEY` + `anthropic-version: 2023-06-01` + `content-type: application/json`
- 请求体字段映射（统一 ↔ Anthropic）：

| 统一字段 | Anthropic 字段 |
|----------|---------------|
| `req.System` | `system`（顶层） |
| `req.Messages` | `messages` |
| `req.Tools` | `tools[]`（`input_schema` 字段） |
| `req.ToolChoice` | `tool_choice` |
| `req.Temperature / TopP / TopK / StopSequences / MaxTokens` | 同名 |
| `req.Model` | `model` |
| `req.Stream` | `stream: true` |

- 流式事件映射（Anthropic SSE ↔ 统一事件）：

| Anthropic SSE event | 统一 StreamEvent |
|---------------------|------------------|
| `message_start` | `StartEvent` |
| `content_block_start` (type=text) | （后续跟 `text_delta`） |
| `content_block_delta` (delta.type=text_delta) | `TextDeltaEvent{Delta}` |
| `content_block_start` (type=tool_use) | `ToolCallStartEvent{ID,Name}` |
| `content_block_delta` (delta.type=input_json_delta) | `ToolCallDeltaEvent{ID,Delta}` |
| `content_block_stop` (type=tool_use) | `ToolCallEndEvent{ID,Name,Arguments}`（从累积的 JSON 字符串反序列化得到 `Arguments`） |
| `message_delta`（带 stop_reason） | （累积，最后随 DoneEvent 一起给） |
| `message_stop` | `DoneEvent{Response}` |
| `error` event | `ErrorEvent{Err}` |

- 错误响应（Anthropic `{"type":"error","error":{"type":"...","message":"..."}}`）按 `type` 映射到 `ProviderError.Code`：
  - `authentication_error` → `ErrCodeAuth`
  - `rate_limit_error` → `ErrCodeRateLimit`
  - `invalid_request_error` → `ErrCodeInvalidRequest`
  - 其他 → `ErrCodeInternal`

### FR-5：HTTP 客户端与重试

`internal/agent/llm/httpclient.go`：

- 复用 `internal/logger` 的 logger 打印请求/响应 debug 日志（脱敏后）
- 默认 `http.Client`，超时由调用方通过 `context.Context` 控制
- 简单重试：仅对 429 / 5xx 且 `ctx` 未取消时最多重试 2 次，指数退避（1s / 2s，可被 ctx deadline 截断）
- 不重试 4xx（除 429）

### FR-6：Provider 工厂

`internal/agent/llm/registry.go`：

```go
func NewProvider(ctx context.Context, name string, cfg ProviderConfig) (ModelProvider, error)
```

- 当前支持 `name == "anthropic"`
- 未知 name 返回 `ErrUnknownProvider`
- 配置 `ProviderConfig.APIKey` 必填；`BaseURL` 可选（默认 Anthropic 官方）

### FR-7：构造器（不装配到 main.go）

`llm.NewProvider(ctx, name, cfg)` 是 Provider 的工厂入口，**本 spec 不在 `cmd/app/main.go` 装配它**。Provider 实例化与注入到 Agent 主循环的逻辑留到后续 `agent-runtime-core` spec（届时 `main.go` 通过 `_ import "anthropic"` 触发 `init()` 注册，再调用 `llm.NewProvider` 拿到实例并喂给 `runtime.New(agent.WithProvider(provider))`）。本 spec 的交付仅到"调用方拿到 `llm.ModelProvider` 接口"为止。

---

## 4. 实现方案

### 4.1 目录结构

在 `src/darvin-agent/` 下新增 `internal/agent/llm/` 目录（与后续的 `internal/agent/context` / `internal/agent/memory` / `internal/agent/skills` / `internal/agent/mcp` 同级，详见 §8 后续 spec）：

```
src/darvin-agent/
├── internal/
│   ├── agent/
│   │   └── llm/
│   │       ├── provider.go        # ModelProvider 接口 + 公共类型
│   │       ├── types.go           # Request/Response/Message/Tool/Usage
│   │       ├── errors.go          # ProviderError + 错误码常量
│   │       ├── events.go          # StreamEvent 接口 + 各事件实现
│   │       ├── httpclient.go      # 带重试的 HTTP 客户端
│   │       ├── registry.go        # NewProvider 工厂
│   │       └── anthropic/
│   │           ├── provider.go    # AnthropicProvider 实现
│   │           ├── convert.go     # 统一 ↔ Anthropic 请求/响应转换
│   │           └── stream.go      # Anthropic SSE → 统一 StreamEvent 解析
│   └── ...（logger / config / database 沿用 go-base-framework）
└── cmd/app/main.go                # 本 spec 不动：装配 provider 留到 agent-runtime-core spec
```

后续 spec 在 `internal/agent/` 下并列放置：

```
internal/agent/
├── llm/         # 本 spec
├── context/     # 后续 spec：上下文引擎
├── memory/      # 后续 spec：记忆系统
├── skills/      # 后续 spec：技能系统
└── mcp/         # 后续 spec：MCP 客户端
```

依赖：

- `net/http`（标准库）
- `encoding/json`（标准库）
- `bufio`（SSE 行解析）
- `strings` / `strconv` / `errors` / `context` / `time`（标准库）
- `internal/logger`（已有）

**不**引入第三方 HTTP 客户端或 SSE 库，避免与既有 `go.mod` 冲突；后续如要迁 SDK 单独走 spec。

### 4.2 关键设计决策

#### 4.2.1 流式协议：channel-based `Events`（**struct 字段**）

`StreamingResponse` 暴露 `Events <-chan StreamEvent`（**字段，不是方法**）与 `Err() error`。调用方 `for ev := range sr.Events` 消费；`Err()` 在通道关闭后返回最后一次错误。**不**用回调 / `interface{}` / `io.Reader` —— channel + 强类型事件最贴合 Go 习惯，且便于上层 `select` 监听 ctx 取消。

`StartEvent.Partial AssistantMessage{Model}` 携带流开始时的 model id 占位（供 UI 立即渲染 streaming placeholder；后续 TextDeltaEvent / ToolCallStartEvent 继续填充）。

`DoneEvent` 携带完整的 `CompletionResponse`（含累计 Usage、最终 FinishReason），Agent 主循环拿到后即可结算本轮。

#### 4.2.2 ToolCall 流式：增量 delta + 结束整对象

Anthropic 增量给的是 `input_json_delta`（JSON 字符串片段），我们在 provider 内部按 `index` 聚合：遇到 `content_block_start (tool_use)` 起一个 buffer，`input_json_delta` 追加 buffer，最后 `content_block_stop` 时 `json.Unmarshal` 得到 `Arguments`。这样上层只看到 `ToolCallEnd{ID,Name,Arguments(map[string]any)}`，与是否流式无关。

#### 4.2.3 重试策略保守

仅 429 / 5xx 重试 2 次，避免在错误重试上耗费太多复杂度。后续若需要更复杂策略（per-error-code 退避、token bucket）单独 spec。

#### 4.2.4 Extra 字段

`CompletionRequest.Extra map[string]any` 透传 provider 特定参数（例如 Anthropic 的 `metadata`、`stop_sequences` 命名差异等），**不**做兜底解析，由各 provider 自行读取/忽略。保持抽象层最小。

#### 4.2.5 不做跨模型消息转换

`transformMessages`（图像降级、思考签名丢弃、工具调用 ID 规范化、孤儿工具结果补齐）这些高级能力在 OpenClaw 设计里位于 `AiTransportHost.transformTransportMessages`，对应到 Go 侧属于 **更高一层** 的"上下文组装"职责。本 spec 只交付最底层的 provider 调用，转换逻辑留到上层 Agent 主循环或独立的 `messages/transform.go` 模块，**不**塞到 `internal/agent/llm/` 里。

### 4.3 Anthropic provider 关键代码骨架

```go
// internal/agent/llm/anthropic/provider.go
type Provider struct {
    apiKey  string
    baseURL string
    http    *httpclient.Client
}

func New(apiKey string, opts ...Option) *Provider { ... }

func (p *Provider) Name() string { return "anthropic" }

func (p *Provider) Complete(ctx context.Context, req *llm.CompletionRequest) (*llm.CompletionResponse, error) {
    payload, err := buildPayload(req, false)
    if err != nil { return nil, err }
    body, err := p.http.Do(ctx, p.baseURL+"/v1/messages", p.headers(), payload)
    if err != nil { return nil, err }
    return parseResponse(body)
}

func (p *Provider) Stream(ctx context.Context, req *llm.CompletionRequest) (*llm.StreamingResponse, error) {
    payload, err := buildPayload(req, true)
    if err != nil { return nil, err }
    return newStream(ctx, p.http, p.baseURL+"/v1/messages", p.headers(), payload)
}
```

```go
// internal/agent/llm/anthropic/stream.go 核心循环
func newStream(ctx context.Context, hc *httpclient.Client, url string, headers http.Header, payload []byte) (*llm.StreamingResponse, error) {
    body, err := hc.DoStream(ctx, url, headers, payload)
    if err != nil { return nil, err }
    events := make(chan llm.StreamEvent, 16)

    go func() {
        defer close(events)
        defer body.Close()

        // 按 index 聚合 tool_use
        toolBuffers := map[int]*toolAccum{}
        var finalStopReason string
        var usage llm.Usage
        var respModel string

        scanner := bufio.NewScanner(body)
        for scanner.Scan() {
            line := scanner.Bytes()
            // SSE: "event: xxx\n" / "data: {...}\n\n"
            // 解析后 dispatch 到 events：
            //   - message_start        → StartEvent + 初始化 respModel/usage
            //   - content_block_start  → ToolCallStartEvent{ID,Name} (tool_use) / nothing (text)
            //   - content_block_delta   → TextDeltaEvent 或 ToolCallDeltaEvent
            //   - content_block_stop    → ToolCallEndEvent（反序列化累积 JSON）
            //   - message_delta         → 记录 stop_reason / usage 增量
            //   - message_stop          → DoneEvent{Response}
            //   - error                 → ErrorEvent{Err}
            // 任何解析错误 → ErrorEvent{Err}
        }
        if err := scanner.Err(); err != nil { events <- llm.ErrorEvent{Err: err}; return }
    }()

    return &llm.StreamingResponse{Events: events}, nil
}
```

### 4.4 测试策略

本项目当前没有 Go test runner（见 `AGENTS.md` §测试），本 spec **不**强制要求加测试。但内部会落地 `internal/agent/llm/anthropic/convert_test.go` 与 `stream_test.go` 内的最小单测骨架（用 `testing` 标准库），后续接 vitest / go test runner 时可直接启用。

手测路径：

```bash
# 设置环境变量
export ANTHROPIC_API_KEY=sk-ant-...
# 在 cmd/app/main.go 里临时加一段：
#   provider, _ := agentllm.NewProvider(ctx, "anthropic", agentllm.ProviderConfig{APIKey: os.Getenv("ANTHROPIC_API_KEY")})
#   resp, _ := provider.Complete(ctx, &agentllm.CompletionRequest{
#       Model:    "claude-sonnet-4-5",
#       Messages: []agentllm.Message{{Role: agentllm.RoleUser, Content: "ping"}},
#       MaxTokens: 64,
#   })
#   fmt.Println(resp.Content)
go build ./... && ./bin/darvin-agent-$(go env GOOS)-$(go env GOARCH)
```

---

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| ctx 在请求中途被取消 | httpclient.Do 立刻终止请求；流式情况下 scanner 读到 EOF 后 channel 关闭，调用方 select 见 ctx.Err() |
| Anthropic 返回 401 | 不重试，返回 `*ProviderError{Code: ErrCodeAuth}` |
| Anthropic 返回 429 | 重试 2 次（指数退避 1s / 2s），仍失败返回 `*ProviderError{Code: ErrCodeRateLimit}` |
| Anthropic 返回 5xx | 重试 2 次，仍失败返回 `*ProviderError{Code: ErrCodeInternal, StatusCode: 5xx}` |
| Anthropic 返回 4xx（除 429） | 不重试，返回 `*ProviderError{Code: ErrCodeInvalidRequest, StatusCode: 4xx}` |
| SSE 帧解析失败（JSON 损坏） | 立即关闭 channel，发 `ErrorEvent{Err: ...}`，Stream 内部把错误保留供 `Err()` 返回 |
| 流中途断连 | scanner.Err() 非空 → `ErrorEvent{Err}` → channel 关闭 |
| 工具调用参数 JSON 不完整（提前 stream 终止） | `ToolCallEnd` 不发送；改在 `DoneEvent` 之前追加一次 `ErrorEvent{Err}` 标记 partial tool call |
| APIKey 为空 | registry.NewProvider 阶段返回 `ErrMissingAPIKey`，不发起请求 |
| baseURL 自定义为非官方域名 | 直接使用，不做域名校验（避免越权） |

---

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/darvin-agent/internal/agent/llm/provider.go` | 新增：`ModelProvider` 接口 + `StreamingResponse` |
| `src/darvin-agent/internal/agent/llm/types.go` | 新增：`Role / Message / Tool / ToolChoice / CompletionRequest / CompletionResponse / FinishReason / Usage / ParameterSchema / ParameterProperty` |
| `src/darvin-agent/internal/agent/llm/errors.go` | 新增：`ProviderError` + 错误码常量 |
| `src/darvin-agent/internal/agent/llm/events.go` | 新增：`StreamEvent` 接口 + 各事件结构体 + `StartEvent / TextDeltaEvent / ToolCallStartEvent / ToolCallDeltaEvent / ToolCallEndEvent / DoneEvent / ErrorEvent` |
| `src/darvin-agent/internal/agent/llm/httpclient.go` | 新增：`Client` 结构 + `Do / DoStream` 方法 + 429/5xx 重试 + 日志脱敏 |
| `src/darvin-agent/internal/agent/llm/registry.go` | 新增：`NewProvider` 工厂 + `ProviderConfig` + 错误 `ErrUnknownProvider / ErrMissingAPIKey` |
| `src/darvin-agent/internal/agent/llm/anthropic/provider.go` | 新增：`Provider` 实现 `ModelProvider`（Complete / Stream） |
| `src/darvin-agent/internal/agent/llm/anthropic/convert.go` | 新增：`buildPayload` / `parseResponse` + 工具定义 / 消息数组转换 |
| `src/darvin-agent/internal/agent/llm/anthropic/stream.go` | 新增：SSE 扫描 + 事件 dispatch + tool_use 增量聚合 + DoneEvent 装配 |
| `specs/features/agent-llm-encapsulation/2026-07-28-agent-llm-encapsulation-design.md` | 新增：本 spec 文件 |

**本 spec 不修改 `cmd/app/main.go`**——provider 的实例化与注入留到后续 `agent-runtime-core` spec（构造 Agent 主循环时一并接入）。本 spec 的 main.go 仅承担"go-base-framework 已定义"的三件套（config / logger / database）装配，不感知 LLM 层存在。

不涉及：renderer、preload、`src/main/runtime/`、`src/main/index.ts`、Tailwind / Vue 栈。

---

## 7. 验收标准

- [ ] `cd src/darvin-agent && go build ./...` 编译通过，无报错
- [ ] `go vet ./...` 无警告
- [ ] `internal/agent/llm/provider.go` 暴露 `ModelProvider` 接口，含 `Name / Complete / Stream` 三个方法
- [ ] `internal/agent/llm/types.go` 暴露 `Role / Message / Tool / ToolChoice / CompletionRequest / CompletionResponse / FinishReason / Usage`
- [ ] `internal/agent/llm/events.go` 暴露 `StreamEvent` 接口 + 至少 7 个事件类型（Start / TextDelta / ToolCallStart / ToolCallDelta / ToolCallEnd / Done / Error）
- [ ] `internal/agent/llm/errors.go` 暴露 `ProviderError` + 4 个错误码常量
- [ ] `internal/agent/llm/registry.go` 的 `NewProvider(ctx, "anthropic", cfg)` 在 APIKey 缺失时返回错误，不发起网络请求
- [ ] Anthropic provider 对应官方文档示例请求（curl 例子）能成功完成 `Complete` 调用（手测：在 main.go 临时加一段调通，stdout 打印 `resp.Content`）
- [ ] 流式调用能按顺序产出 `Start → TextDelta* → Done`，且 `DoneEvent.Response.Usage` 字段非零
- [ ] 工具调用流式能产出 `ToolCallStart → ToolCallDelta* → ToolCallEnd`，且 `ToolCallEnd.Arguments` 是解析后的 `map[string]any`
- [ ] 401 返回 `*ProviderError{Code: ErrCodeAuth}` 且不重试
- [ ] 429 / 5xx 重试 2 次后退避失败，返回 `*ProviderError{Code: ErrCodeRateLimit / ErrCodeInternal}`
- [ ] ctx 取消后流式 channel 立刻关闭，调用方 `Err()` 返回 `context.Canceled`
- [ ] `npm run lint`（仅 Go 改动时跳过此步，因 ESLint 不覆盖 Go；如要更严格用 `go vet` 替代）
- [ ] 现有 `go-base-framework` 的 logger / config / database 模块未被本 spec 改动（`cmd/app/main.go` 也未被修改）
- [ ] 未引入第三方 HTTP / SSE 依赖（仅使用 Go 标准库 + `internal/logger`）

---

## 8. 后续 spec 候选（不在本 spec 范围内）

1. `agent-runtime-core`：Agent 主循环、session 管理、tool 注册表（`runtime.go` / `agent.go` / `session.go` / `executor.go`）——已以 `agent-loop` spec 落地，详见同目录 `2026-07-28-agent-loop-design.md`
2. `openai-provider`：OpenAI Completions / Responses provider 实现 + 跨模型消息转换
3. `gemini-provider`：Google Gemini provider 实现
4. `thinking-and-cache`：思考块跨轮次签名、prompt cache key、cache retention 配置
5. `cost-and-quota`：成本计算 + 限流 + 预算告警
6. `mcp-client`：MCP 客户端封装（与本 spec 平级，单独写）
7. `context-engine`：上下文组装 + 压缩（DAG / projection）

每个后续 spec 都按本 spec 的接口约定（`ModelProvider` + 统一事件）扩展，不在本 spec 内提前暴露。

---

## 9. 实现偏差与说明（Implementation Notes）

落地时对前述章节做了若干有意偏离，逐条记录，便于后续 spec 引用与回溯。

### 9.1 `StreamingResponse.Events` 是 struct 字段

§FR-1 / §4.2.1 原稿把 `Events` 写成 `<-chan StreamEvent` 的**方法**。实际落地为 struct 字段：

```go
type StreamingResponse struct {
    Events <-chan StreamEvent  // 字段
    err     error
    body    io.ReadCloser
}
```

`Err() error` 仍是方法。同步给出三个 provider 包专用工具函数：

- `NewStreamingResponse(events chan StreamEvent, body io.ReadCloser) *StreamingResponse`：provider 构造时用。
- `SetErr(err error)`：goroutine 在 channel close 前记录 terminal error，幂等。
- `Close() error`：释放底层 HTTP body；消费者通常不直接调用（goroutine 退出前会 `body.Close()`）。

### 9.2 `FinishReasonAborted` 由 agent-loop 引入

§FR-2 列出 `stop / length / tool_calls / content_filter / error` 五个常量。`agent-loop` spec 落地时在 `internal/agent/executor/executor.go` 检测 `ctx.Canceled`，把被打断轮次的 assistant 消息打上 `FinishReasonAborted = "aborted"` 写回 Session。该常量加在 `internal/agent/llm/types.go`：

```go
FinishReasonAborted FinishReason = "aborted"
// 由 executor 合成，不会由任何 provider 直接返回。
```

调用方判断被 abort：用 `errors.Is(err, ErrAborted)` 或 `messages[i].StopReason == llm.FinishReasonAborted`。

### 9.3 `Tool.Type` 字段加在 Anthropic 落地期

§FR-2 列出 `Tool{Name, Description, Parameters}` 三字段。Anthropic provider 落地期增加 `Type string`（OpenAI 兼容，默认 `"function"`），为后续接 OpenAI provider 时统一形态。

### 9.4 `HTTPClient.Logger` 是接口注入而非 `*zap.Logger` 直接依赖

§FR-5 写"复用 `internal/logger` 的 logger 打印请求/响应 debug 日志"。实际为避免 llm 包对 zap 的硬依赖，定义了本地最小接口：

```go
type Logger interface {
    Debugw(msg string, keysAndValues ...any)
    Infow(msg string, keysAndValues ...any)
    Warnw(msg string, keysAndValues ...any)
    Errorw(msg string, keysAndValues ...any)
}
```

`*zap.SugaredLogger` / `*slog.Logger` / agent 自身的 `*logger.Logger` 都隐式满足。nil logger 容忍（logDebug 会短路）。**不**引第三方日志库。

### 9.5 默认错误解析器覆盖 Anthropic / OpenAI / Gemini 三家 envelope

§FR-5 只承诺"返回 `*ProviderError`"。`HTTPClient` 同时落地了 `defaultProviderErrorParser` —— 识别三种 envelope：

- Anthropic：`{"type":"error","error":{"type":"<code>","message":"..."}}`
- OpenAI：`{"error":{"type":"<code>","message":"..."}}`
- Google：`{"error":{"code":<int>,"message":"...","status":"..."}}`
- Generic：`{"message":"..."}`

并提供 `ProviderErrorParser func(statusCode, body)` 接缝供后续 provider 替换。这样后续 `openai-provider` / `gemini-provider` spec 不必重复实现 4xx/5xx 错误码映射。

### 9.6 重试退避表为闭包内 `backoff []time.Duration`

§FR-5 写"指数退避（1s / 2s）"但未指定形态。实际在 `HTTPClient` 构造时把 `[1s, 2s]` 固化入 `backoff []time.Duration` 字段，后续测试或自定义 provider 可通过构造 options 覆盖。

### 9.7 `IsCode(err, code)` 与 `NewProviderError(...)` 辅助

§FR-3 只列 `ProviderError` + `Error/Unwrap`。落地加：

- `IsCode(err error, code string) bool`：用 `errors.As` 取出 `*ProviderError` 后比 `Code`。调用方无须手动类型断言。
- `NewProviderError(provider, code, message, status, cause)` 工厂：避免 provider 包各自拼装 struct。

### 9.8 `Tool` 字段命名 / `Message.ToolCallID`

§FR-2 写 `Message` 含 `ToolCalls []*ToolCall`（spec 用了指针 `*ToolCall`，但代码用值类型 `[]ToolCall`）。`ToolCallID string` 与 spec 一致。Agent 循环消费侧统一按值类型处理（无 pointer aliasing 顾虑）。

### 9.9 `cmd/llm-smoke` 已被删除

§6 §7 §8 多处提到 `cmd/llm-smoke/main.go` 作为 M1 烟测入口，已被用户在 v0 重构期删除（理由：与 `cmd/app/main.go` 功能重叠且单独入口增加发布复杂度）。spec 文档保留历史描述以便审计，v0 落地后无对应可执行入口。M2 spec（agent-loop）已不引用此烟测。