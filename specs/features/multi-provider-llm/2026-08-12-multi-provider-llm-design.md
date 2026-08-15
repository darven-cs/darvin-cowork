# 多 Provider LLM 支持设计文档（页面 → IPC → Go 子进程全链路）

## 1. 概述

### 1.1 问题 / 背景

darvin-cowork 目前只有一个真正接通的 LLM provider：`anthropic`。renderer 设置页虽有
`anthropic / openai / custom` 三个下拉项，但 `openai / custom` 只是把凭据存进用户级 yaml 的
`providers` 块，Go agent 不消费、不激活——设置页文案也明说"OpenAI / Custom 凭据先保存，待 Go agent 接入后启用"。

三处割裂：

- **页面**：`SettingsPanelModels.vue` 的 provider 下拉硬编码 3 个；"默认模型"是自由文本输入，
  `ModelPicker.vue`（首页工具栏）走的是 `mock-data.ts` 的假模型列表，选中只写 `localStorage`，
  **不会**影响 Go agent 实际用的模型。
- **主进程**：`darvin:set_llm_config` 只对 `anthropic` 写 `llm` 块并重启 Go；`openai / custom`
  写 `providers` 块但不激活（避免 Go 因未知 provider 启动时 `os.Exit(1)`）。
- **Go 子进程**：`internal/llm/` 的 provider 工厂注册机制（`RegisterProvider` + `init()` 自注册）
  已是多 provider 形态，但只注册了 `anthropic`；`config.LLM` 是单 provider 的 `{provider, api_key, base_url}`，
  没有 `providers` 映射，也没有 `default_model`。

参考的两个开源项目给出了成熟方案：

- **LobsterAI**（Electron + TS）：`src/shared/providers/constants.ts` 是 provider 的**单一事实来源**——
  `ProviderName` / `ApiFormat`（openai / anthropic / gemini）/ `ProviderDef`（id、label、website、
  apiKeyUrl、defaultBaseUrl、defaultApiFormat、defaultModels[]、region），UI 全由这份目录驱动，新增
  provider 只改一个文件；每个 provider 独立 `apiKey / baseUrl / models`。
- **DeepSeek-Reasonix**（Go）：`internal/provider/` 核心包定义 `Provider` 接口（`Name() / Stream()`）、
  `Request` / `Message` / `ToolSchema` 协议类型 + 工厂注册表，`anthropic` / `openai` / `responses`
  子包各自 `init()` 自注册。**关键架构**：`openai` 一个实现覆盖所有 OpenAI-compatible 网关——
  DeepSeek / Qwen / Zhipu / Moonshot / MiniMax / Ollama / Kimi / **Gemini** 都是配置实例而非代码，
  `host.go` 按 base URL 主机识别 Gemini（`generativelanguage.googleapis.com`）并套用其方言。

### 1.2 目标

把**完整的多 provider 矩阵**从页面到 Go 子进程打通，采用"2 个 wire 协议 + N 个预设"架构：

1. **Go 侧两个 wire 格式**：
   - `anthropic-messages`（现有，保留）—— Anthropic 官方 + 任意 Anthropic 格式网关（base_url 可改）。
   - `openai-completions`（新增）—— 一个适配器覆盖**所有 OpenAI 兼容网关**：
     OpenAI / DeepSeek / Qwen(DashScope) / Zhipu GLM / Moonshot(Kimi) / MiniMax / Volcengine Ark /
     OpenRouter / Ollama(本地) / SiliconFlow / 以及 **Gemini（走 Google 官方 OpenAI 兼容端点
     `https://generativelanguage.googleapis.com/v1beta/openai/`）**。支持消息 / 工具调用 /
     SSE 流式 / usage / finish_reason。
2. **共享 provider 目录**：新增 `src/shared/providers.ts`，列出 ~12 个**预设**（每项含 id / label /
   apiFormat / defaultBaseUrl / apiKeyRequired / defaultModels / region），main + renderer 共用，
   是设置页 UI 的唯一驱动。
3. **配置升级**：`providers.<key>` 每项独立 `api_format / api_key / base_url / default_model`；
   `llm.provider` = 当前激活的**预设 key**；运行时按 `api_format` 分发到对应 wire 适配器。
4. **模型目录 RPC**：新增 `agent.llm.list_models`，把 Go `ModelRegistry` 元数据暴露给 renderer，
   替换 mock 模型列表（ModelPicker 用它 + 目录 defaultModels 合并）。
5. **主进程激活**：`set_llm_config` 对任意预设 key 统一写 `llm.provider` + `providers.<key>` 并重启
   Go，保存即生效；切换前校验（apiKey / baseUrl 是否必填），避免 Go 启动失败。
6. **页面**：设置页 preset 下拉 + 模型下拉真实化；`ModelPicker` 绑定真实目录并支持**按次切换**
   （prompt 携带 `model` 覆盖默认，Go 端生效）。

### 1.3 非目标

- **不做** OpenAI Responses API 与按 base URL 分支的"思考"方言（deepseek thinking /
  minimax adaptive 等），OpenAI 适配器保持 vanilla Chat Completions；
  `reasoning_content` 字段天然存在，解析时**透传**为 `ThinkingDeltaEvent` 即可。
- **不做** OAuth（LobsterAI 的 GitHub Copilot / Claude OAuth 流程）；copilot 预设先用 api_key 占位。
- **不做** 设置页的模型价格展示 / 用量计费 UI（`ModelDescriptor.Cost` 已存在，不新增 UI）。
- **不迁移** 已有会话的历史模型记录（`LastModel` 保留原样）。

## 2. 完整 Provider 矩阵（与 LobsterAI `src/shared/providers/constants.ts` 1:1 对齐）

| 预设 key | 名称 | 默认 wire 格式 | 默认 base_url | 需 key | 默认模型 |
|---|---|---|---|---|---|
| `deepseek` | DeepSeek | openai | https://api.deepseek.com | ✅ | deepseek-v4-flash / v4-pro / reasoner |
| `moonshot` | Moonshot | openai | https://api.moonshot.cn/v1 | ✅ | kimi-k2.6 / k2.5 |
| `qwen` | Qwen | **anthropic** | https://dashscope.aliyuncs.com/apps/anthropic | ✅ | qwen3.6-plus / 3.5-plus |
| `zhipu` | Zhipu | **anthropic** | https://open.bigmodel.cn/api/anthropic | ✅ | glm-5.1 / 5 / 4.7 |
| `minimax` | MiniMax | **anthropic** | https://api.minimaxi.com/anthropic | ✅ | MiniMax-M3 / M2.7 / M2.5 |
| `volcengine` | Volcengine | **anthropic** | https://ark.cn-beijing.volces.com/api/compatible | ✅ | doubao-seed-2.0-pro/lite/mini |
| `youdaozhiyun` | Youdao | openai（完整 URL） | https://openapi.youdao.com/llmgateway/api/v1/chat/completions | ✅ | deepseek-reasoner / deepseek-inhouse-reasoner |
| `qianfan` | Qianfan | openai | https://qianfan.baidubce.com/v2 | ✅ | kimi-k2.5 / glm-5.1 / deepseek-v4-flash / ernie-4.5 |
| `stepfun` | StepFun | openai | https://api.stepfun.com/v1 | ✅ | step-3.5-flash |
| `xiaomi` | Xiaomi | openai（完整 URL） | https://api.xiaomimimo.com/v1/chat/completions | ✅ | mimo-v2.5-pro / v2.5 |
| `ollama` | Ollama | openai | http://localhost:11434/v1 | ❌ | qwen3-coder-next / glm-4.7-flash |
| `lm-studio` | LM Studio | openai | http://localhost:1234/v1 | ❌ | （无预设） |
| `copilot` | GitHub Copilot | openai | https://api.individual.githubcopilot.com | ✅ | gpt-5-mini / claude-haiku-4.5 / gpt-4.1 / gpt-4o |
| `openai` | OpenAI | openai | https://api.openai.com/v1 | ✅ | gpt-5.4 / 5.5 |
| `gemini` | Gemini | **gemini（原生）** | https://generativelanguage.googleapis.com/v1beta | ✅ | gemini-3.1-pro / 3-flash / 3.1-flash-lite |
| `xai` | xAI (Grok) | openai | https://api.x.ai/v1 | ✅ | grok-4.3 / grok-build-0.1 |
| `anthropic` | Anthropic | anthropic | https://api.anthropic.com | ✅ | claude-opus-4-7 / 4-6 / sonnet-4-6 |
| `openrouter` | OpenRouter | **anthropic** | https://openrouter.ai/api | ✅ | anthropic/claude-sonnet-4.6 / opus-4.7 / openai/gpt-5.5 |
| `custom` | Custom (OpenAI-compatible) | openai | （用户填） | 可选 | （用户填） |

> 说明：
> - 多个 provider 支持 **可切换的 wire 格式**（`switchableBaseUrls`：anthropic/openai 双端点），默认格式与
>   LobsterAI `defaultApiFormat` 一致；设置页可切换并自动换 base_url。
> - `openai` 格式适配器覆盖所有 OpenAI-compatible 网关（DeepSeek / Qwen / Moonshot / Ollama / xAI /
>   Copilot / custom…），并兼容 Youdao / Xiaomi 的「完整 chat/completions URL」base（不重复拼接路径）。
> - `gemini` 走**原生 Google Generative AI 适配器**（`llm/gemini`，generateContent / streamGenerateContent），
>   与 LobsterAI 一致（不再走 openai 兼容端点）。
> - 国内 provider（qwen / zhipu / minimax / volcengine / openrouter）默认用 Anthropic 格式端点。

## 3. 现状盘点（改动前基准）

- `src/shared/darvin-api.ts`：`DarvinModelProvider = 'anthropic' | 'openai' | 'custom'`；
  `DarvinLLMConfig{provider, activeProvider, apiKey, baseUrl, defaultModel, providers}`；
  `DarvinPromptRequest.model?: DarvinModelId`（**已存在但 gateway 不透传**）。
- `src/main/index.ts`：`darvin:get_llm_config`（读 yaml 拼 providers）、`darvin:set_llm_config`
  （anthropic 写 llm 块+重启；openai/custom 只写 providers 块）。
- `src/main/libs/user-settings.ts`：yaml 结构 `llm{provider, api_key, base_url, default_model}` +
  `providers: Record<name, {api_key, base_url, default_model}>`（**main 已写，Go 不读**）。
- `src/renderer/components/settings/SettingsPanelModels.vue`：3 个 provider 下拉 + 自由文本模型。
- `src/renderer/components/home/ModelPicker.vue` + `composables/useModel.ts` + `services/mock-data.ts`：
  mock 模型列表，选中只写 localStorage。
- `src/darvin-agent/internal/llm/`：`registry.go`（`RegisterProvider`/`NewProvider`，已多 provider 形态）、
  `provider.go`（re-export `protocol.ModelProvider`）、`httpclient.go`（`HTTPClient.Do / DoStream`）、
  `model_registry.go`（re-export `protocol.ModelRegistry`）。
- `src/darvin-agent/internal/llm/anthropic/`：`provider.go`（`New` + `init()` 注册 + 注册 Claude 模型到
  `DefaultModelRegistry`）、`convert.go`、`stream.go`、`compat.go` —— openai 子包的模板。
- `src/darvin-agent/internal/agents/protocol/`：`ModelProvider` 接口（`Name/Complete/Stream`）、
  `StreamEvent` union、`ModelRegistry`、`APIKind`（已含 `openai-completions`）、
  `CompletionRequest / CompletionResponse / Message / ToolSpec / ToolCall / Usage / FinishReason`。
- `src/darvin-agent/internal/config/config.go`：`LLMConfig{Provider, APIKey, BaseURL}`。
- `src/darvin-agent/internal/runtime/`：`provider.go`（`loadProvider` 只用 `cfg.LLM` 顶层字段）、
  `runtime.go:170`（`Model: agent.ModelRef{Provider: cfg.Agent.ProviderName, Model: cfg.Agent.Model}`）、
  `agent_config.go`（`newAgentConfig`）。
- `src/darvin-agent/internal/gateway/handlers.go`：method 分发 switch（`agent.prompt` / `agent.mcp.list` 等）。

## 4. 用户场景

### 场景 1：添加 DeepSeek 作为预设并立即使用
**Given** 用户在设置页 preset 下拉选"DeepSeek"，自动带出 base_url，填 API key、选 `deepseek-chat`
**When** 点击保存
**Then** 凭据写入 `providers.deepseek`，`llm.provider=deepseek`，Go 重启后聊天正常出流；
设置页显示"运行时已切到 DeepSeek"

### 场景 2：本地 Ollama 免 key
**Given** 用户选"Ollama"预设（默认 `http://localhost:11434/v1`，apiKeyRequired=false）
**When** 保存
**Then** 请求不带 `Authorization` 头，正常出流

### 场景 3：OpenAI 官方 provider 一键切换
**Given** 用户选 OpenAI，填 key
**When** 保存
**Then** 生效；设置页"默认模型"下拉列出 gpt-4o / gpt-4o-mini / gpt-4.1 / gpt-4.1-mini，不再自由文本

### 场景 4：自定义 OpenAI 兼容端点（逃生舱）
**Given** 用户选"Custom"，填任意 base_url（如 SiliconFlow / 公司网关）
**When** 保存
**Then** 走 openai 适配器；base_url 为空时保存被拦截并提示

### 场景 5：聊天工具栏按次换模型
**Given** 运行时为 Anthropic，默认模型 claude-sonnet-4-5
**When** 用户在首页 ModelPicker 搜索并选中 gpt-4o 后发消息
**Then** 该次 prompt 用 gpt-4o（Go 端按次覆盖），模型徽标显示 gpt-4o；下次不选则回落默认模型

### 场景 6：Go 离线时设置页仍可编辑
**Given** Go 子进程未启动 / 连接断开
**When** 打开设置页模型 tab
**Then** preset 下拉用共享目录驱动、模型下拉用该 preset 的 defaultModels 兜底；`get_llm_config` 仍返回 yaml 凭据

## 5. 功能需求

### FR-1: Go OpenAI Chat Completions 适配器（覆盖所有 OpenAI 兼容网关）
- 新增 `src/darvin-agent/internal/llm/openai/` 子包，实现 `protocol.ModelProvider`
  （`Name/Complete/Stream`），走 `POST {base_url}/chat/completions`。
- `init()` 注册 `"openai"` 一个 wire 格式名（预设 deepseek/qwen/zhipu/…/gemini/custom 全部复用）；
  所有实例行为一致，仅凭据 / base URL / 模型不同。
- base URL 默认 `https://api.openai.com/v1`（由 main 写入 providers 时填全，Go 侧仅兜底默认值）。
- 复用 `llm.HTTPClient.Do / DoStream`；请求头 `Authorization: Bearer <key>`（apiKey 为空时省略）。
- 流式解析 SSE：`data: {…}` 增量、`data: [DONE]` 收尾；映射 `StreamEvent` union：
  `TextDeltaEvent`（`choices[].delta.content`）、`ThinkingDeltaEvent`（`delta.reasoning_content`，透传）、
  `ToolCallDeltaEvent`（`delta.tool_calls[].function.arguments` 增量拼 JSON）、`ToolCallStart/End`、
  `DoneEvent`（`finish_reason` + `usage`）。非流式 `Complete` 解析 `choices[0].message`。
- 消息转换：`system` → `messages[0]`（或 system role）；工具结果 `role:"tool"` + `tool_call_id`；
  图片附件 → `content` 数组的 `image_url`（base64 data URL，模型无 vision 时跳过）。
- 工具定义：`ToolSpec` → OpenAI `functions`，`ToolChoice` 映射 `tool_choice`。
- 在 `init()` 里向 `DefaultModelRegistry` 注册官方模型描述符（gpt 系列 + gemini 系列，见 6.2）。

### FR-2: 多 provider 配置 + 按 wire 格式分发
- `Config` 顶层加 `Providers`（**与 yaml 对齐：`providers.<key>` 是 `llm` 的平级顶层块**，不是 `llm.providers`）；`LLMConfig` 只保留 active 字段：
  ```go
  type Config struct {
      LLM       LLMConfig `mapstructure:"llm"`
      Providers map[string]LLMProviderConfig `mapstructure:"providers"` // 顶层！
  }
  type LLMConfig struct {
      Provider string `mapstructure:"provider"`
      APIKey   string `mapstructure:"api_key"`     // 顶层遗留：active provider 兜底凭据
      BaseURL  string `mapstructure:"base_url"`
      Model    string `mapstructure:"default_model"` // main 写 llm.default_model
  }
  type LLMProviderConfig struct {
      APIFmt       string `mapstructure:"api_format"`   // "anthropic" | "openai" | "gemini"
      APIKey       string `mapstructure:"api_key"`
      BaseURL      string `mapstructure:"base_url"`
      DefaultModel string `mapstructure:"default_model"`
  }
  ```
- `runtime/provider.go` 的 `loadAllProviders`（读取顶层 `cfg.Providers`）：
  ```go
  for key, entry := range cfg.Providers {
      wire := resolveWire(key, entry)
      p, err := llm.NewProvider(ctx, wire, llm.ProviderConfig{APIKey: entry.APIKey, BaseURL: entry.BaseURL, Logger: log})
      out[key] = p
  }
  // active key 无 providers.<key> 条目时：按 key 名猜 wire（deepseek→openai / anthropic→anthropic），
  // 用顶层 cfg.LLM.{APIKey,BaseURL} 兜底，避免把异族模型送到错误 wire。
  ```
  provider 名未知 / 构造失败 → 记日志并返回错误（main 侧 runtime status 暴露）。
- 新增 `runtime.resolveModelRef(cfg)`（放 `agent_config.go`）：
  ```go
  func resolveModelRef(cfg *config.Config) agent.ModelRef {
      provider := cfg.LLM.Provider
      model := cfg.LLM.Model
      if entry, ok := cfg.Providers[provider]; ok && entry.DefaultModel != "" {
          model = entry.DefaultModel
      }
      if model == "" { model = cfg.Agent.Model }
      if provider == "" { provider = cfg.Agent.ProviderName }
      return agent.ModelRef{Provider: provider, Model: model}
  }
  ```
  `runtime.go` 里 `Model:` 一行改用该 helper。

### FR-3: 模型目录 RPC（Go → renderer）
- 新增 `src/darvin-agent/internal/gateway/handler_llm.go`，method `agent.llm.list_models`：
  从 `protocol.DefaultModelRegistry.All()` 输出 wire 安全的描述符：
  ```go
  type ModelDescriptorWire struct {
      ID string `json:"id"`; Name string `json:"name"`; Provider string `json:"provider"`
      APIKind string `json:"apiKind"`; ContextWindow int `json:"contextWindow"`
      MaxTokens int `json:"maxTokens"`; Reasoning bool `json:"reasoning"`; Input []string `json:"input"`
  }
  ```
  （不含凭据 / 成本，纯元数据。）
- 在 `handlers.go` 分发 switch 加 `case "agent.llm.list_models":`。
- main 端新增 `darvin:get_llm_models` handler，转发 `client.request('agent.llm.list_models')`；
  Go 离线返回 `[]`。

### FR-4: 主进程激活任意预设 + 校验 + 重启
- `darvin:set_llm_config` 改造：
  - 入参 `{ provider: string; apiKey: string; baseUrl?: string; defaultModel?: string }`（provider 为预设 key）。
  - 前置校验按 `DARVIN_PROVIDERS` 目录：`apiKeyRequired` 且 apiKey 空 → 拒绝（`saved:false`）；
    `requiresBaseUrl`（custom）且 baseUrl 空 → 拒绝。
  - 写盘：统一写 `llm.provider=<key>` + `providers.<key>={api_format, api_key, base_url, default_model}`；
    `api_format` 来自目录；`anthropic` 同时保留顶层 `llm.api_key / base_url / default_model`（向后兼容）。
  - 任何预设都 `restartGoSubprocess()`，返回 `{saved:true, restarted}`。
- `darvin:get_llm_config`：`providers` 块补 `apiFormat` 字段返回；`activeProvider` = yaml `llm.provider`。

### FR-5: 共享 provider 目录（单一事实来源）
- 新增 `src/shared/providers.ts`：
  ```ts
  export type DarvinApiFormat = 'anthropic' | 'openai';
  export interface DarvinProviderPreset {
    id: string;                     // config key：anthropic / openai / deepseek / ... / custom
    label: string;                  // 显示名：'Anthropic' / 'DeepSeek' / 'Ollama (本地)' ...
    apiFormat: DarvinApiFormat;     // wire 格式（写进 providers.<key>.api_format）
    defaultBaseUrl: string;
    apiKeyRequired: boolean;        // ollama=false，custom=false
    requiresBaseUrl: boolean;       // custom=true
    apiKeyPlaceholder: string;      // 'sk-...' / 'ollama 免 key'
    website?: string;               // provider 控制台
    defaultModels: { id: string; label: string; contextWindow?: number }[];
    region: 'china' | 'global';
  }
  export const DARVIN_PROVIDERS: DarvinProviderPreset[];
  export function darvinProviderPreset(id: string): DarvinProviderPreset | undefined;
  export function darvinProviderModels(id: string): DarvinProviderPreset['defaultModels'];
  ```
  `custom` 的 `defaultModels` 放知名 OpenAI 兼容端点提示（siliconflow / moonshot / 公司网关等）。
- main + renderer 都从该文件 import；**provider id 字符串不得在组件里硬编码**。

### FR-6: 设置页真实 preset / 模型选择
- `SettingsPanelModels.vue` 改造：
  - preset 下拉遍历 `DARVIN_PROVIDERS`（按 region 分组展示）；删掉"待接入"的 `pending_note`。
  - 选中 preset 自动带出 `defaultBaseUrl`（可改）；`apiKeyRequired=false` 时 key 输入框变可选并给提示。
  - "默认模型"由自由文本改为**下拉**：优先 `getLLMModels()` 里 `provider===当前preset` 的目录，
    无目录回落该 preset `defaultModels`；保留"自定义输入"兜底（custom 允许任意模型 id）。
  - 保存：`setLLMConfig`；文案统一 `saved_restarted`；校验失败展示对应错误。

### FR-7: ModelPicker 绑定真实目录 + 按次切换
- `useModel.ts` / `ModelPicker.vue` 改造：
  - 选项 = 合并（`getLLMModels()` Go 目录 + 各 preset `defaultModels`），按 provider 分组，
    vendor 徽标 = provider id 首字符。
  - `currentModel` 仍存 localStorage；选中即记。
- 按次切换管道（页面 → Go 生效）：
  - `DarvinPromptRequest.model` 类型放宽为 `model?: string`；`provider` 传 **preset key**（不是 wire 名）。
  - main `darvin:prompt` 透传 `model`；Go `PromptParams` 加 `Provider/Model`，`handlePrompt` 传入
    `sessionruntime.PromptRequest`。
  - `Loop` 的 `RunAttemptParams.Provider/Model` 改为 `firstNonEmpty(req.*, <default>)`；
    **`extractProviderName` 返回 preset key（`a.ModelProviderKey()`），不是 `Provider().Name()` 的 wire 名**——
    否则按次覆盖会用 anthropic 预设的凭据去请求别的 provider（如 MiniMax 用 deepseek key → 404）。
  - embedded/CLI harness runner 闭包在 `params.Provider/Model != ""` 时 `SetRunModel`（限当次 attempt）；
    `Agent.Provider()/ModelName()` 在 override 存在且 preset 已接线时返回 override。
  - 按次覆盖不持久化。

### FR-8: 类型与 i18n
- `darvin-api.ts`：新增 `DarvinModelInfo`；`DarvinLLMConfig` 增加 `registeredProviders: string[]`；
  `providers` 值类型加 `apiFormat`；`DarvinPromptRequest.model?: string`。
- i18n：删 `settings.models.pending_note` / `saved_pending`；新增校验文案
  （api_key_required / custom_base_url_required）、`settings.models.region.china/global` 分组标题；
  preset label 直接展示目录 `label` 字段，不硬编码中文进字典。

## 6. 实现方案

### 6.1 分层调用链（改动后）

```
renderer SettingsPanelModels.vue / ModelPicker.vue
        │  window.darvin.getLLMConfig / getLLMModels / setLLMConfig / prompt{model}
        ▼
main    index.ts  (get_llm_config / set_llm_config 按目录校验 / get_llm_models / prompt 透传 model)
        │  writeUserSettingsYAML → restartGoSubprocess；AgentClient.request
        ▼
shared  providers.ts（DARVIN_PROVIDERS 目录）· darvin-api.ts（DarvinLLMConfig + DarvinModelInfo + model?: string）
        ▼
Go      gateway/handler_llm.go (agent.llm.list_models) · handler_prompt.go (PromptParams.Model)
        ▼
        runtime/provider.go (loadProvider 按 api_format 分发) · runtime/agent_config.go (resolveModelRef)
        ▼
        llm/registry.go → llm/openai (openai wire) · llm/anthropic (anthropic wire)
        → protocol.ModelProvider / StreamEvent
```

### 6.2 `llm/openai` 文件清单（对照 anthropic 子包模板）

| 文件 | 职责 |
|---|---|
| `openai.go` | `Provider` struct（`name/apiKey/baseURL/model/hc`）、`New(cfg)`、`init()` 注册 `"openai"`、`Name/Complete/Stream`、请求头 |
| `convert.go` | `buildChatMessages(messages, system)`、`buildTools(tools, toolChoice)`、图片 `image_url` 块、响应 `message` → `CompletionResponse` |
| `stream.go` | SSE 解析器（bufio.Scanner 按行、`data:` 前缀、`[DONE]`）、`toStreamEvent` 映射、usage/finish_reason 累积 |
| `models.go` | `init()` 向 `DefaultModelRegistry` 注册模型描述符 |
| `openai_test.go` / `convert_test.go` / `stream_test.go` | 纯函数 + 流式解析测试（httptest server） |

模型描述符注册（v1，成本近似；`Compat` 内联 `llm.DefaultOpenAICompat` 或类似）：
- `openai` provider：`gpt-4o`（vision/tools/128k）、`gpt-4o-mini`（128k）、`gpt-4.1`（1M/tools）、`gpt-4.1-mini`（1M）。
- `gemini` provider（走 openai 兼容端点）：`gemini-2.0-flash`、`gemini-2.5-flash`、`gemini-2.5-pro`，
  `APIKind` 标 `openai-completions`。
- deepseek/qwen/zhipu/…/ollama/custom 的模型**不静态注册**（模型 id 任意），运行时无描述符时沿用
  `dispatcher.go` 现有的"未命中用默认 context window"兜底。

### 6.3 关键接口签名

```go
// llm/openai/openai.go
type Provider struct {
    name    string
    apiKey  string
    baseURL string
    model   string
    hc      *llm.HTTPClient
}
func New(cfg llm.ProviderConfig) (*Provider, error)
func (p *Provider) Name() string
func (p *Provider) Complete(ctx context.Context, req *llm.CompletionRequest) (*llm.CompletionResponse, error)
func (p *Provider) Stream(ctx context.Context, req *llm.CompletionRequest) (*llm.StreamingResponse, error)
```

```ts
// shared/providers.ts（示意）
export const DARVIN_PROVIDERS: DarvinProviderPreset[] = [
  { id: 'anthropic',  label: 'Anthropic', apiFormat: 'anthropic', defaultBaseUrl: 'https://api.anthropic.com',
    apiKeyRequired: true,  requiresBaseUrl: false, apiKeyPlaceholder: 'sk-ant-...',
    defaultModels: [{ id: 'claude-sonnet-4-5', label: 'Claude Sonnet 4.5' }, { id: 'claude-opus-4-5', label: 'Claude Opus 4.5' }],
    region: 'global' },
  { id: 'deepseek',   label: 'DeepSeek',  apiFormat: 'openai', defaultBaseUrl: 'https://api.deepseek.com/v1',
    apiKeyRequired: true,  requiresBaseUrl: false, apiKeyPlaceholder: 'sk-...',
    defaultModels: [{ id: 'deepseek-chat', label: 'DeepSeek V3' }, { id: 'deepseek-reasoner', label: 'DeepSeek R1' }],
    region: 'china' },
  { id: 'ollama',     label: 'Ollama (本地)', apiFormat: 'openai', defaultBaseUrl: 'http://localhost:11434/v1',
    apiKeyRequired: false, requiresBaseUrl: false, apiKeyPlaceholder: '本地免 key',
    defaultModels: [{ id: 'qwen2.5:7b', label: 'Qwen2.5 7B' }, { id: 'llama3.1:8b', label: 'Llama3.1 8B' }],
    region: 'global' },
  // ... openai / qwen / zhipu / moonshot / minimax / volcengine / openrouter / gemini / custom
];
```

### 6.4 main / IPC 变更点

- `index.ts` 新增 `darvin:get_llm_models`：`client.isConnected() ? client.request('agent.llm.list_models') : []`。
- `darvin:set_llm_config`：按 `darvinProviderPreset(provider)` 校验 → 统一写 `llm.provider` +
  `providers.<key>`（含 `api_format`；anthropic 兼写顶层）→ `restartGoSubprocess()`。
- `darvin:get_llm_config`：`providers` 值带 `apiFormat`。
- `src/main/libs/user-settings.ts`：`UserSettingsProviderEntry` 加 `api_format?: string`。
- `preload` 暴露 `getLLMModels()`。
- `darvin:prompt`：确认转发 body 含 `model`。

### 6.5 按次覆盖的最小管道（FR-7）

| 层 | 文件 | 改动 |
|---|---|---|
| TS API | `darvin-api.ts` | `DarvinPromptRequest.model?: string` |
| renderer | `useChatActions.ts`（发消息处） | prompt 带 `model: currentModel.value` |
| main | `index.ts` prompt handler | payload 透传 `model` |
| Go gateway | `handler_prompt.go` | `PromptParams.Model`；`handlePrompt` 传入 `PromptRequest.Model` |
| Go sessionruntime | `loop.go` | `PromptRequest{…, Model string}`；`RunAttemptParams.Model = firstNonEmpty(req.Model, l.agent.ModelName())` |
| Go harness | `builtin_embedded.go` / `builtin_cli.go` runner 闭包 | `params.Model != ""` 时覆盖 Deps 的 `ModelName()`（scope 限当次 attempt） |

## 7. 边界情况

| 场景 | 处理方式 |
|---|---|
| 切 preset 时 apiKeyRequired 且 key 为空（anthropic/openai/deepseek…） | main 拒绝激活（saved:false），不写 llm.provider、不重启 |
| custom 的 base_url 为空 | main 拒绝激活；Go `New(custom)` 走 openai 工厂，base_url 为空时报错（双保险） |
| 本地 Ollama 免 key | apiKeyRequired=false，请求头省略 Authorization |
| Go 启动时 provider 构造失败（key 失效等） | `loadProvider` 返回错误，main runtime status 暴露；设置页可改回可用 provider |
| 模型 id 不在 ModelRegistry | `dispatcher` 现有兜底（默认 context window），不崩 |
| Go 离线时打开设置页 / ModelPicker | `getLLMModels` 返回 []；UI 回落 preset `defaultModels` / `mockModels` |
| 按次切换后未选模型 | 回落 `resolveModelRef` 的默认模型 |
| yaml `llm.provider` 指向 catalog 之外的未知 key | `loadProvider`：providers 无该 entry → 回落顶层凭据 + 按 key 名猜 wire（非 anthropic 一律 openai） |
| 旧 yaml 只有 `llm.provider: anthropic` + 顶层凭据（无 providers.anthropic） | 向后兼容：wire=anthropic、凭据取顶层，行为与现状一致 |
| 并发 / 多会话同 provider | `ModelProvider` 单例并发安全（anthropic 已是该模式），openai 沿用 `HTTPClient` |

## 8. 涉及文件

### Go（`src/darvin-agent/internal/`）
| 文件 | 变更说明 |
|---|---|
| `llm/openai/openai.go`（新） | OpenAI-compatible Chat Completions 适配器，注册 `"openai"` wire |
| `llm/openai/convert.go`（新） | 消息 / 工具 / 图片 / 响应转换 |
| `llm/openai/stream.go`（新） | SSE 解析 → `StreamEvent` |
| `llm/openai/models.go`（新） | 注册 gpt 系列 + gemini 系列描述符 |
| `llm/openai/*_test.go`（新） | convert / stream / provider 测试 |
| `config/config.go` | `LLMConfig` 加 `Model` + `Providers` 映射；新增 `LLMProviderConfig`（含 `APIFmt`） |
| `runtime/provider.go` | `loadProvider` 按 api_format 分发 + providers 映射解析；blank-import openai |
| `runtime/agent_config.go` | 新增 `resolveModelRef` |
| `runtime/runtime.go` | `Model:` 改用 `resolveModelRef(cfg)` |
| `gateway/handler_llm.go`（新） | `agent.llm.list_models` handler |
| `gateway/handlers.go` | 分发 switch 加 `agent.llm.list_models` case |
| `gateway/handler_prompt.go` | `PromptParams.Model` + 透传 |
| `sessionruntime/loop.go` | `PromptRequest.Model` + `RunAttemptParams.Model` 覆盖 |
| `harness/builtin_embedded.go` / `builtin_cli.go` | runner 闭包按 `params.Model` 覆盖 `ModelName()` |

### main / shared / preload
| 文件 | 变更说明 |
|---|---|
| `src/shared/providers.ts`（新） | `DARVIN_PROVIDERS` 预设目录（单一事实来源） |
| `src/shared/darvin-api.ts` | `DarvinModelInfo`、`DarvinLLMConfig.registeredProviders`、`providers` 加 `apiFormat`、`DarvinPromptRequest.model?: string` |
| `src/main/index.ts` | `get_llm_models`；`set_llm_config` 任意预设激活 + 校验 + 重启；prompt 透传 model |
| `src/main/libs/user-settings.ts` | `UserSettingsProviderEntry` 加 `api_format` |
| `src/preload/index.ts` | 暴露 `getLLMModels()` |

### renderer
| 文件 | 变更说明 |
|---|---|
| `components/settings/SettingsPanelModels.vue` | preset 目录驱动 + 默认模型下拉 + 校验文案 + region 分组 |
| `composables/useModel.ts` | 选项 = Go 目录 + preset defaultModels，回落 mock |
| `components/home/ModelPicker.vue` | 目录驱动、vendor 徽标、按 provider 分组 |
| `composables/useChatActions.ts` | prompt 携带 `model` |
| `services/mock-data.ts` | 保留作为离线兜底 |
| `services/i18n.ts` | 删 pending 文案，加校验 / region 分组文案 |

### 文档
| 文件 | 变更说明 |
|---|---|
| `specs/features/multi-provider-llm/2026-08-12-multi-provider-llm-design.md` | 本文档 |

## 9. 验收标准

- [ ] 场景 1：DeepSeek 预设保存 → yaml 出现 `providers.deepseek{api_format: openai, ...}` + `llm.provider=deepseek` → Go 重启 → 聊天正常出流（deepseek-chat）
- [ ] 场景 2：Ollama 免 key 保存生效，请求不带 Authorization
- [ ] 场景 3：OpenAI preset 保存即生效，设置页默认模型下拉列出 gpt-4o 等（不再自由文本）
- [ ] 场景 4：custom base_url 为空保存被拦截，`saved:false` + 错误文案
- [ ] 场景 5：ModelPicker 选中 gpt-4o 发消息 → 该 turn 用 gpt-4o（`LLMStartEvent.model` 验证），次 turn 回落默认模型
- [ ] 场景 6：Go 离线时设置页 / ModelPicker 用目录兜底，不白屏
- [ ] `go build ./...` + `go vet ./...` + `go test ./...`（含 openai 子包）通过；`npm run build:agent` 出新二进制
- [ ] `npm run lint` 通过；新增 i18n key 在 dictZh / dictEn 对齐
- [ ] `npm run test`（vitest）通过；覆盖 `convert` / `stream` 纯函数
- [ ] 手动验证：`npm start` 后设置页 ~12 个预设均可保存并重启；首页 ModelPicker 列表来自 Go 目录 + preset defaultModels
