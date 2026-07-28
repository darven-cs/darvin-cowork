# LLM 接口详解

## 概述

OpenClaw 的 LLM 层位于 `packages/llm-core/src/` 和 `packages/ai/src/`，提供统一的模型抽象和跨提供商接口。

**核心设计目标**：
- 抽象多提供商的差异（OpenAI、Anthropic、Google、Mistral、Azure 等）
- 统一流式事件协议
- 支持工具调用、思考能力、提示缓存等高级特性
- 灵活的可插拔架构

---

## 核心类型体系

### API 类型（KnownApi）

系统定义了 9 种内置 API 类型：

```typescript
type KnownApi =
  | "openai-completions"        // OpenAI Chat Completions API（兼容模式）
  | "mistral-conversations"    // Mistral 对话 API
  | "openai-responses"          // OpenAI 新版 Responses API
  | "azure-openai-responses"   // Azure OpenAI Responses
  | "openai-chatgpt-responses" // GitHub Copilot ChatGPT Responses
  | "anthropic-messages"       // Anthropic Messages API（Claude 主推）
  | "bedrock-converse-stream"   // AWS Bedrock Converse 流式
  | "google-generative-ai"      // Google Gemini Generative AI
  | "google-vertex";           // Google Vertex AI
```

自定义 API 类型可通过 `string & {}` 扩展。

---

## Model 对象

### 完整结构

```typescript
interface Model<TApi extends Api = Api> {
  // ============ 标识信息 ============
  id: string;           // 模型版本标识，如 "claude-sonnet-4-20250514"
  name: string;         // 人类可读名称

  // ============ API 配置 ============
  api: TApi;            // API 类型
  provider: Provider;   // 提供商 ID，如 "anthropic"、"openai"
  baseUrl: string;      // API 端点 URL

  // ============ 模型能力 ============
  reasoning: boolean;              // 是否支持推理/思考能力
  thinkingLevelMap?: ThinkingLevelMap; // 思考级别映射表

  // ============ 输入输出格式 ============
  input: ("text" | "image")[];    // 支持的输入类型

  // ============ Token 限制 ============
  contextWindow: number;           // 最大上下文窗口
  contextTokens?: number;          // 当前使用 token 数（运行时）
  maxTokens: number;               // 最大输出 token 数

  // ============ 成本信息 ============
  cost: {
    input: number;        // 输入价格（美元/百万 tokens）
    output: number;       // 输出价格
    cacheRead: number;    // 缓存读取价格（90% 折扣）
    cacheWrite: number;   // 缓存写入价格
  };

  // ============ 可选配置 ============
  params?: Record<string, unknown>;    // 提供商特定参数
  headers?: Record<string, string>;     // 自定义 HTTP 头
  authHeader?: boolean;                // 是否使用 Authorization: Bearer

  // ============ 兼容性配置 ============
  compat?: TApi extends "openai-completions"
    ? OpenAICompletionsCompat
    : TApi extends "openai-responses"
      ? OpenAIResponsesCompat
      : TApi extends "anthropic-messages"
        ? AnthropicMessagesCompat
        : never;

  // ============ 媒体输入限制 ============
  mediaInput?: {
    image?: {
      maxBytes?: number;              // 最大图像字节数
      maxPixels?: number;            // 最大像素数
      maxSidePx?: number;             // 最大边长像素
      preferredSidePx?: number;       // 推荐边长像素
      tokenMode?: "tile" | "detail" | "provider";  // token 计算模式
    };
  };
}
```

### ThinkingLevelMap（思考级别映射）

思考级别映射定义统一思考级别到提供商特定值的转换：

```typescript
type ThinkingLevel = "minimal" | "low" | "medium" | "high" | "xhigh" | "max";
type ModelThinkingLevel = "off" | ThinkingLevel;
type ThinkingLevelMap = Partial<Record<ModelThinkingLevel, string | null>>;

// null 表示该级别不支持
// string 是提供商特定的值
```

**默认思考预算**：

```typescript
interface ThinkingBudgets {
  minimal?: number;  // 1024 tokens
  low?: number;      // 2048 tokens
  medium?: number;   // 8192 tokens
  high?: number;     // 16384 tokens
  max?: number;      // 32768 tokens
}
```

---

## 消息类型体系

### 消息联合类型

```typescript
type Message = UserMessage | AssistantMessage | ToolResultMessage;
```

### UserMessage（用户消息）

```typescript
interface UserMessage {
  role: "user";
  content: string | (TextContent | ImageContent)[];
  timestamp: number;  // Unix 时间戳（毫秒）

  // 标记运行时上下文载体（仅当前轮次有效，不用于缓存）
  runtimeContextCarrier?: boolean;
}
```

### AssistantMessage（助手消息）

```typescript
interface AssistantMessage {
  role: "assistant";
  content: (TextContent | ThinkingContent | ToolCall)[];
  api: Api;             // 提供商 API 类型
  provider: Provider;   // 提供商 ID
  model: string;        // 模型标识

  // 响应元数据
  responseModel?: string;   // 实际模型（如 OpenRouter "auto" 解析后的具体模型）
  responseId?: string;      // 提供商响应 ID
  turnId?: string;          // 运行时轮次 ID

  // 诊断信息
  diagnostics?: AssistantMessageDiagnostic[];

  // 使用量统计
  usage: Usage;

  // 停止原因
  stopReason: StopReason;
  errorMessage?: string;
  errorCode?: string;
  errorType?: string;
  errorBody?: string;
  timestamp: number;
}
```

### ToolResultMessage（工具结果消息）

```typescript
interface ToolResultMessage<TDetails = unknown> {
  role: "toolResult";
  toolCallId: string;       // 对应的工具调用 ID
  toolName: string;         // 工具名称
  content: (TextContent | ImageContent)[];  // 支持文本和图像
  details?: TDetails;       // 提供商特定的详细信息
  isError: boolean;         // 是否为错误结果
  timestamp: number;
}
```

### Content Block 类型

```typescript
// 文本内容块
interface TextContent {
  type: "text";
  text: string;
  textSignature?: string;   // 响应签名（如 OpenAI Responses 的元数据）
}

// 思考内容块（Claude/Gemini 等）
interface ThinkingContent {
  type: "thinking";
  thinking: string;
  thinkingSignature?: string;  // 加密的思考签名（用于跨轮次连续性）
  redacted?: boolean;         // 是否被安全过滤器编辑
}

// 图像内容块
interface ImageContent {
  type: "image";
  data: string;      // base64 编码
  mimeType: string; // 如 "image/jpeg"
}

// 工具调用块
interface ToolCall {
  type: "toolCall";
  id: string;
  name: string;
  arguments: Record<string, unknown>;
  thoughtSignature?: string;   // Google 专用：思考上下文签名
  executionMode?: "sequential" | "parallel";
}
```

### Usage（使用量统计）

```typescript
interface Usage {
  input: number;         // 输入 token 数
  output: number;        // 输出 token 数
  cacheRead: number;     // 缓存读取 token 数
  cacheWrite: number;    // 缓存写入 token 数
  cacheWrite1h?: number; // 1 小时保留的缓存写入

  // 上下文使用情况
  contextUsage?: {
    state: "available";
    promptTokens: number;
    totalTokens: number;
  } | {
    state: "unavailable";
  };

  totalTokens: number;   // 总 token 数

  // 成本计算
  cost: {
    input: number;
    output: number;
    cacheRead: number;
    cacheWrite: number;
    total: number;
    totalOrigin?: "provider-billed";  // 提供商报告的权威总额
  };
}
```

---

## 流式处理体系

### EventStream 类

```typescript
class EventStream<T, R = T> implements AsyncIterable<T> {
  push(event: T): void;           // 推送事件
  end(result?: R): void;          // 结束流
  result(): Promise<R>;            // 获取最终结果

  // AsyncIterable 实现
  async *[Symbol.asyncIterator](): AsyncIterator<T>;
}
```

**Specialized Stream**：

```typescript
class AssistantMessageEventStream
  extends EventStream<AssistantMessageEvent, AssistantMessage>
  implements AssistantMessageEventStreamContract
{
  // 在 done/error 事件时自动解析最终结果
}
```

### 事件协议（AssistantMessageEvent）

事件协议定义了流式响应的完整生命周期：

```typescript
// ============ 开始事件 ============
| { type: "start"; partial: AssistantMessage }
// 流开始，partial 包含初始状态

// ============ 文本事件 ============
| { type: "text_start"; contentIndex: number; partial: AssistantMessage }
// 文本块开始
| { type: "text_delta"; contentIndex: number; delta: string; partial?: AssistantMessage }
// 文本增量（delta 是新增片段）
| { type: "text_end"; contentIndex: number; content: string; partial: AssistantMessage }
// 文本块结束

// ============ 思考事件（Claude/Gemini） ============
| { type: "thinking_start"; contentIndex: number; partial: AssistantMessage }
// 思考块开始
| { type: "thinking_delta"; contentIndex: number; delta: string; partial: AssistantMessage }
// 思考增量
| { type: "thinking_end"; contentIndex: number; content: string; partial: AssistantMessage }
// 思考块结束

// ============ 工具调用事件 ============
| { type: "toolcall_start"; contentIndex: number; partial: AssistantMessage }
// 工具调用开始
| { type: "toolcall_delta"; contentIndex: number; delta: string; partial: AssistantMessage }
// 工具调用参数增量（JSON 字符串）
| { type: "toolcall_end"; contentIndex: number; toolCall: ToolCall; partial: AssistantMessage }
// 工具调用完成

// ============ 结束事件 ============
| { type: "done"; reason: "stop" | "length" | "toolUse"; message: AssistantMessage }
// 成功结束
| { type: "error"; reason: "aborted" | "error"; error: AssistantMessage }
// 错误结束
```

### 停止原因（StopReason）

```typescript
type StopReason =
  | "stop"        // 正常停止
  | "length"      // 达到最大长度
  | "toolUse"     // 生成工具调用后停止
  | "error"       // 发生错误
  | "aborted";    // 被中止
```

---

## 请求选项（StreamOptions）

### 完整定义

```typescript
interface StreamOptions {
  // ============ 生成控制 ============
  temperature?: number;       // 温度参数
  maxTokens?: number;        // 最大输出 token
  stop?: string[];           // 停止序列

  // ============ 信号控制 ============
  signal?: AbortSignal;     // 中止信号

  // ============ 认证 ============
  apiKey?: string;          // API Key（覆盖默认）

  // ============ 传输配置 ============
  transport?: Transport;     // 传输类型："sse" | "websocket" | "websocket-cached" | "auto"

  // ============ 缓存配置 ============
  cacheRetention?: CacheRetention;  // 缓存保留期："none" | "short" | "long"
  sessionId?: string;            // 会话 ID（用于缓存亲和性）
  promptCacheKey?: string;         // 缓存键

  // ============ 回调钩子 ============
  onPayload?: (payload: unknown, model: Model) => MaybePromise<unknown>;
  // 发送前检查/修改请求体
  onResponse?: (response: ProviderResponse, model: Model) => void | Promise<void>;
  // 收到响应后、开始消费前回调

  // ============ HTTP 配置 ============
  headers?: Record<string, string>;  // 自定义请求头
  timeoutMs?: number;               // 请求超时
  maxRetries?: number;               // 最大重试次数
  maxRetryDelayMs?: number;         // 最大重试延迟（默认 60000ms）

  // ============ 元数据 ============
  metadata?: Record<string, unknown>;  // 提供商支持的元数据
}

// 缓存保留期
type CacheRetention = "none" | "short" | "long";

// 传输类型
type Transport = "sse" | "websocket" | "websocket-cached" | "auto";
```

### SimpleStreamOptions（简化选项）

```typescript
interface SimpleStreamOptions extends StreamOptions {
  reasoning?: ModelThinkingLevel;      // 思考级别
  thinkingBudgets?: ThinkingBudgets;  // 自定义思考预算
}
```

---

## 兼容性接口

### OpenAI Completions 兼容性

```typescript
interface OpenAICompletionsCompat {
  // ============ 功能支持 ============
  supportsStore?: boolean;                    // 是否支持 store 字段
  supportsDeveloperRole?: boolean;            // 是否支持 developer 角色
  supportsReasoningEffort?: boolean;          // 是否支持 reasoning_effort
  supportsUsageInStreaming?: boolean;         // 流式响应是否包含 usage
  requiresToolResultName?: boolean;           // 工具结果是否需要 name 字段
  requiresAssistantAfterToolResult?: boolean; // 工具结果后是否需要 assistant 消息
  requiresThinkingAsText?: boolean;           // 是否将思考块转为文本
  requiresReasoningContentOnAssistantMessages?: boolean;
  supportsStrictMode?: boolean;              // 是否支持 strict 字段
  supportsJsonSchemaResponseFormat?: boolean; // 是否支持 response_format

  // ============ thinking 格式 ============
  thinkingFormat?:
    | "openai"       // reasoning_effort
    | "openrouter"   // reasoning: { effort }
    | "deepseek"     // thinking: { type } + reasoning_effort
    | "together"     // reasoning: { enabled } + reasoning_effort
    | "zai"          // top-level enable_thinking: boolean
    | "qwen"         // top-level enable_thinking: boolean
    | "qwen-chat-template";  // chat_template_kwargs.enable_thinking

  // ============ 缓存控制 ============
  cacheControlFormat?: "anthropic";  // Anthropic 风格缓存标记
  sendSessionAffinityHeaders?: boolean;
  supportsPromptCacheKey?: boolean;
  supportsLongCacheRetention?: boolean;

  // ============ 路由配置 ============
  openRouterRouting?: OpenRouterRouting;
  vercelGatewayRouting?: VercelGatewayRouting;
  zaiToolStream?: boolean;
}
```

### OpenAI Responses 兼容性

```typescript
interface OpenAIResponsesCompat {
  supportsDeveloperRole?: boolean;     // 是否支持 developer 角色
  supportsTemperature?: boolean;       // 是否支持 temperature
  sendSessionIdHeader?: boolean;     // 是否发送 session_id 头
  supportsLongCacheRetention?: boolean;
}
```

### Anthropic Messages 兼容性

```typescript
interface AnthropicMessagesCompat {
  supportsEagerToolInputStreaming?: boolean;  // 是否支持工具输入流
  supportsLongCacheRetention?: boolean;         // 是否支持长缓存保留
  sendSessionAffinityHeaders?: boolean;         // 是否发送会话亲和性头
  supportsCacheControlOnTools?: boolean;         // 工具定义是否支持 cache_control
  allowEmptySignature?: boolean;                // 是否允许空签名重放
}
```

### OpenRouter 路由配置

```typescript
interface OpenRouterRouting {
  allow_fallbacks?: boolean;           // 允许备用提供商
  require_parameters?: boolean;         // 仅路由到支持所有参数的提供商
  data_collection?: "deny" | "allow";  // 数据收集设置
  zdr?: boolean;                        // 零数据保留端点
  enforce_distillable_text?: boolean;   // 仅允许可蒸馏文本的模型
  order?: string[];                     // 按顺序尝试的提供商
  only?: string[];                     // 仅允许的提供商
  ignore?: string[];                   // 排除的提供商
  quantizations?: string[];            // 量化级别过滤
  sort?: string | { by?: string; partition?: string | null };
  max_price?: { prompt?: number | string; completion?: number | string; ... };
  preferred_min_throughput?: number | { p50?: number; p75?: number; ... };
  preferred_max_latency?: number | { p50?: number; p75?: number; ... };
}
```

---

## API 注册机制

### ApiProvider 接口

```typescript
interface ApiProvider<TApi extends Api = Api, TOptions extends StreamOptions = StreamOptions> {
  api: TApi;                           // 处理的 API 类型
  stream: StreamFunction<TApi, TOptions>;        // 完整流适配器
  streamSimple: StreamFunction<TApi, SimpleStreamOptions>;  // 简单流适配器
}
```

### ApiRegistry（注册表）

```typescript
function createApiRegistry() {
  const providers = new Map<string, RegisteredApiProviderEntry>();

  return {
    // 注册提供商
    registerApiProvider<TApi, TOptions>(
      provider: ApiProvider<TApi, TOptions>,
      sourceId?: string  // 用于批量注销
    ): void;

    // 获取提供商
    getApiProvider(api: Api): RegisteredApiProvider | undefined;

    // 获取所有提供商
    getApiProviders(): RegisteredApiProvider[];

    // 注销（按 sourceId）
    unregisterApiProviders(sourceId: string): void;

    // 清空
    clearApiProviders(): void;
  };
}
```

### StreamFunction 类型

```typescript
type StreamFunction<
  TApi extends Api = Api,
  TOptions extends StreamOptions = StreamOptions,
> = (
  model: Model<TApi>,
  context: Context,
  options?: TOptions,
) => AssistantMessageEventStreamContract;
```

---

## 传输层主机配置（AiTransportHost）

传输层主机是连接 LLM 包和外部环境（OpenClaw Core）的桥梁：

### 核心接口

```typescript
interface AiTransportHost {
  // ============ Fetch 配置 ============
  buildModelFetch(
    model: Model,
    timeoutMs?: number,
    options?: { sanitizeSse?: boolean }
  ): typeof fetch | undefined;
  // 构建带策略保护的 fetch，不返回则使用默认

  // ============ 密钥处理 ============
  resolveSecretSentinel(value: string): string;
  // 解析密钥哨兵（如 ${API_KEY}）
  redactSecrets<T>(value: T): T;
  // 结构性密钥脱敏
  redactToolPayloadText(text: string): string;
  // 工具负载文本脱敏

  // ============ 提供商特定处理 ============
  normalizeAnthropicInlineContentBlocks?:
    AnthropicInlineContentNormalizer;
  // Anthropic 内联图像规范化

  resolveOpenAIStrictToolSetting(
    model: Model,
    options?: OpenAIStrictToolSettingOptions
  ): boolean | undefined;
  // OpenAI 严格模式工具设置

  // ============ 插件宿主 ============
  plugin: AiTransportPluginHost;

  // ============ Copilot 支持 ============
  buildCopilotDynamicHeaders(messages: Context["messages"]): Record<string, string>;

  // ============ 端点分类 ============
  resolveProviderEndpointClass(baseUrl?: string): string;
  // 端点分类：default、openai、anthropic 等

  // ============ 提供商能力 ============
  resolveProviderRequestCapabilities(
    input: AiProviderRequestPolicyInput
  ): AiProviderRequestCapabilities;

  // ============ 请求头合并 ============
  resolveProviderRequestHeaders(input: {
    provider?: string;
    api?: string;
    baseUrl?: string;
    providerHeaders?: Record<string, string>;
    callerHeaders?: Record<string, string>;
    precedence?: "caller-wins" | "defaults-win";
  }): Record<string, string> | undefined;

  // ============ 超时配置 ============
  resolveModelRequestTimeoutMs(model: Model): number | undefined;

  // ============ 托管传输 ============
  requiresManagedTransport(model: Model): boolean;
  // 是否需要托管传输
  inheritManagedTransport(source: Model, target: Model): Model;
  // 继承托管传输状态

  // ============ 消息转换 ============
  transformTransportMessages: AiTransformTransportMessages;
  // 消息转换（跨提供商工具调用 ID 规范化等）

  // ============ 自定义 API ============
  registerCustomApi(registry: ApiRegistry, api: Api, streamFn: StreamFn): boolean;

  // ============ 日志 ============
  logDebug(subsystem: string, build: () => { message: string; data?: Record<string, unknown> } | null): void;
  logInfo(subsystem: string, message: string, data?: Record<string, unknown>): void;
  logWarn(subsystem: string, message: string, data?: Record<string, unknown>): void;
}
```

### 插件宿主接口

```typescript
interface AiTransportPluginHost {
  resolveProviderStream(params: {
    provider: string;
    config?: unknown;
    workspaceDir?: string;
    env?: NodeJS.ProcessEnv;
    allowRuntimePluginLoad?: boolean;
    context: AiProviderStreamHookContext;
  }): StreamFn | undefined;

  resolveTransportTurnState(params: {
    provider: string;
    modelId?: string | null;
    config?: unknown;
    workspaceDir?: string;
    env?: NodeJS.ProcessEnv;
    allowRuntimePluginLoad?: boolean;
    context: {
      provider: string;
      modelId: string;
      model?: Model;
      sessionId?: string;
      turnId: string;
      attempt: number;
      transport: "stream" | "websocket";
    };
  }): { headers?: Record<string, string>; metadata?: Record<string, string> } | undefined;

  wrapSimpleCompletionStream(params: {
    provider: string;
    config?: unknown;
    context: AiProviderStreamHookContext & { streamFn: StreamFn };
  }): StreamFn | undefined;

  createAnthropicVertexStream(
    model: Pick<Model, "baseUrl">,
    env?: NodeJS.ProcessEnv
  ): StreamFn;
}
```

---

## 消息转换机制

### transformMessages 函数

消息转换处理跨提供商场景的关键问题：

```typescript
function transformMessages<TApi extends Api>(
  messages: Message[],
  model: Model<TApi>,
  normalizeToolCallId?: (
    id: string,
    targetModel: Model<TApi>,
    source: AssistantMessage
  ) => string,
): Message[]
```

**核心转换逻辑**：

1. **图像降级**：不支持图像的模型将图像替换为占位符文本

2. **思考块处理**：
   - 相同模型：保留思考块和签名
   - 跨模型：丢弃加密签名，安全过滤的思考被丢弃
   - 空思考块被跳过
   - 非相同模型的思考转为普通文本

3. **工具调用 ID 规范化**：
   - OpenAI Responses API 生成 450+ 字符的 ID
   - Anthropic 要求 ID 匹配 `^[a-zA-Z0-9_-]+$`（最多 64 字符）
   - 转换函数负责规范化

4. **孤儿工具调用处理**：
   - 为没有对应结果的工具调用插入合成结果
   - 防止 API 错误

5. **错误消息过滤**：
   - 跳过 error/aborted 的 assistant 消息
   - 这些是不完整的轮次，不应重放

---

## 厂商 API 详细对接

### 1. Anthropic Messages（Claude）

**认证**：

| 环境变量 | 说明 |
|---------|------|
| `ANTHROPIC_API_KEY` | 标准 API Key |
| `ANTHROPIC_OAUTH_TOKEN` | OAuth Token（优先） |

**思考能力（Extended Thinking）**：

- `thinkingLevelMap` 定义 off/low/medium/high/xhigh/max 对应的 `budget_tokens`
- 思考通过专用事件输出（`thinking_start/delta/end`）
- 支持 `thinkingSignature` 用于加密思考的跨轮次连续性

**工具调用格式**：

```typescript
// 输入工具格式
{
  type: "tool_use",
  id: "toolu_xxx",
  name: "read_file",
  input: { path: "/path/to/file" }
}

// cache_control 标记（仅最后一条消息）
{
  type: "input_text",
  text: "...",
  cache_control: { type: "ephemeral" }
}
```

**图像处理**：
- 支持 base64 和 URL 类型
- 自动 media type 检测和转换
- 超过限制时自动降级

### 2. OpenAI Completions

**认证**：`OPENAI_API_KEY`

**补全模式 vs 对话模式**：
- 实际使用 `chat.completions` 而非纯补全
- 通过 `messages` 数组模拟对话

**工具调用（Function Calling）**：

```typescript
// 工具定义转换为 function_declarations
{
  type: "function",
  function: {
    name: "tool_name",
    description: "...",
    parameters: { ... }
  }
}
```

**thinkingFormat 映射**：

| Format | 请求结构 |
|--------|---------|
| openai | `reasoning_effort: "low"` |
| openrouter | `reasoning: { effort: "low" }` |
| deepseek | `thinking: { type: "enabled" }, reasoning_effort: "low"` |
| together | `reasoning: { enabled: true }, reasoning_effort: "low"` |
| zai | `enable_thinking: true` |
| qwen | `enable_thinking: true` |

**严格模式（strict）**：
- 启用 JSON Schema 严格解码
- 部分提供商需要此标志

### 3. Mistral Conversations

**认证**：`MISTRAL_API_KEY`

**关键限制**：
- 工具调用 ID 长度限制为 9 字符
- 需要通过 hash 派生规范化的 ID

**字节流保护**：
- 16 MiB 流式响应体上限
- 防止恶意端点耗尽内存

**缓存键**：
```typescript
headers["x-affinity"] = options.sessionId;
```

### 4. OpenAI Responses

**认证**：`OPENAI_API_KEY`

**请求格式**：

```typescript
{
  model: "gpt-4o",
  input: messages,  // 不同于 completions 的 messages
  stream: true,
  tools: [...],
  reasoning: {
    effort: "medium",
    summary: "auto"  // "auto" | "detailed" | "concise"
  }
}
```

**reasoningEffort 映射**：

```typescript
type ResponsesReasoningEffort = "minimal" | "low" | "medium" | "high" | "xhigh" | "max";
```

**工具调用 ID 规范化**：

OpenAI Responses API 生成的 ID 格式：`callId|itemId`（如 `fc_xxx|rs_xxx`）
需要转换为兼容格式：

```typescript
function normalizeResponsesToolCallId(id: string): string {
  const [callId, itemId] = splitResponsesToolCallId(id);
  // callId 规范化，itemId 前面加 fc_ 前缀
  return `${normalizedCallId}|${normalizedItemId}`;
}
```

### 5. Azure OpenAI Responses

**认证**：`AZURE_OPENAI_API_KEY`

**Azure 特定配置**：

```typescript
{
  azureResourceName: "my-resource",
  azureDeploymentName: "gpt-4o",
  azureApiVersion: "v1",
  azureBaseUrl: "https://xxx.openai.azure.com/openai/v1"
}
```

**Base URL 规范**：
- 标准域名：`*.openai.azure.com` 或 `*.cognitiveservices.azure.com`
- 自动规范化路径为 `/openai/v1`

**双客户端支持**：
- 兼容 URL → 标准 `OpenAI` 客户端
- Azure 域名 → `AzureOpenAI` 客户端

**部署名称映射**：

```bash
AZURE_OPENAI_DEPLOYMENT_NAME_MAP="claude-sonnet-4->my-claude;gpt-4o->my-gpt"
```

### 6. Google Gemini（Generative AI）

**认证**：`GOOGLE_API_KEY` 或 `GOOGLE_GENAI_API_KEY`

**生成参数构建**：

```typescript
function buildGoogleGenerateContentParams(model, context, options) {
  return {
    model: model.id,
    contents: convertMessages(model, context),
    config: {
      temperature: options.temperature,
      maxOutputTokens: options.maxTokens,
      stopSequences: options.stop,
      systemInstruction: context.systemPrompt,
      tools: convertTools(context.tools),
      toolConfig: { functionCallingConfig: { mode: "auto" } },
      thinkingConfig: { includeThoughts: true, thinkingBudget: 1024 }
    }
  };
}
```

**工具格式**：
- 使用 `function_declarations` 而非 `tools`
- 支持 `parametersJsonSchema`（完整 JSON Schema）

**thinking 配置**：
- Gemma4 等模型支持
- 通过 `thinkingBudget` 或 `thinkingLevel` 控制

### 7. Google Vertex AI

**认证**：ADC（Application Default Credentials）或 `GOOGLE_CLOUD_API_KEY`

**凭证解析优先级**：

```typescript
// 1. GOOGLE_APPLICATION_CREDENTIALS 环境变量
// 2. ~/.config/gcloud/application_default_credentials.json
// 3. GCP 元数据服务（ECS/IAM）
```

**配置参数**：

```typescript
{
  vertexai: true,
  project: "my-project",
  location: "us-central1",
  apiVersion: "v1"
}
```

**Base URL 处理**：
- 自定义 URL 需要特殊处理 API 版本路径
- 自动检测是否已包含 `v1`

### 8. AWS Bedrock

**认证**：AWS SDK 自动处理

```typescript
// 支持的凭证源
// 1. AWS_PROFILE - named profile
// 2. AWS_ACCESS_KEY_ID + AWS_SECRET_ACCESS_KEY
// 3. AWS_BEARER_TOKEN_BEDROCK
// 4. ECS task roles (AWS_CONTAINER_CREDENTIALS_RELATIVE_URI)
// 5. IRSA (AWS_WEB_IDENTITY_TOKEN_FILE)
```

**Converse 流式传输**：
- 使用 AWS Bedrock Converse API
- 支持 Claude、Llama 等多种模型

---

## 成本计算

### 计算公式

```typescript
function calculateCost(model: Model, usage: Usage): Usage["cost"] {
  const cacheWrite1h = Math.min(usage.cacheWrite, Math.max(0, usage.cacheWrite1h ?? 0));
  const cacheWrite5m = usage.cacheWrite - cacheWrite1h;

  usage.cost.input = (model.cost.input / 1000000) * usage.input;
  usage.cost.output = (model.cost.output / 1000000) * usage.output;
  usage.cost.cacheRead = (model.cost.cacheRead / 1000000) * usage.cacheRead;
  usage.cost.cacheWrite =
    (model.cost.cacheWrite * cacheWrite5m + model.cost.input * 2 * cacheWrite1h) / 1000000;

  usage.cost.total =
    usage.cost.input + usage.cost.output + usage.cost.cacheRead + usage.cost.cacheWrite;

  return usage.cost;
}
```

**缓存成本特殊性**：
- 1 小时缓存写入按 2x 输入价格计算
- 5 分钟缓存写入按标准缓存写入价格计算

---

## 工具定义

### Tool 接口

```typescript
interface Tool<TParameters extends TSchema = TSchema> {
  name: string;           // 工具名称
  description: string;     // 工具描述
  parameters: TParameters; // JSON Schema 参数定义
}
```

### 参数 Schema 示例

```typescript
const readFileTool: Tool = {
  name: "read_file",
  description: "Read contents of a file from the filesystem",
  parameters: {
    type: "object",
    properties: {
      path: {
        type: "string",
        description: "Path to the file to read"
      },
      limit: {
        type: "number",
        description: "Maximum number of lines to read"
      }
    },
    required: ["path"]
  }
};
```

---

## 文档导航

- [Agent 框架概述](./00_OVERVIEW.md)
- [LLM 接口详解](./01_LLM_INTERFACE.md) - 本文档
- [上下文管理详解](./02_CONTEXT_MANAGEMENT.md)
- [记忆系统详解](./03_MEMORY_SYSTEM.md)
- [Skills 系统详解](./04_SKILLS_SYSTEM.md)
- [MCP 集成详解](./05_MCP_INTEGRATION.md)
