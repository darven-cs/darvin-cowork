# Provider Registry 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 问题

darvin-cowork 当前只假设一个 LLM Provider。商业化阶段必须支持多 Provider：

- Chat Completions / Responses / Anthropic-style messages 等 API 类型不一致
- 不同厂商模型 registry / pricing / tool use 协议互不兼容
- 用户在设置页切换 provider，runtime 不能 hold 旧 session
- 各 Provider 鉴权机制、base URL、extra_headers、租户隔离差异大

LobsterAI 的 `featureFlags.ts` 已实现 5 套 provider env，darvin-cowork 的 Provider Registry 应当更具结构化、类型化、可并发注册。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 统一 `Provider` 接口（`chat / stream / tool_use / usage`） | Go interface + TS DarvinApi 对照 |
| G2 | `Factory / Register / Get / List / Remove` 五个核心方法 | runtime/provider/registry.go |
| G3 | 启动期校验配置：baseUrl / apiKey / model 一致性 | `Validator` |
| G4 | 能力声明（capability matrix）：tool_use / vision / audio_in / audio_out / structured_output / system_tool | table |
| G5 | 并发安全：`sync.RWMutex` + 原子加载 | unit test ≥ 10 场景 |
| G6 | 跨 provider 切换不重建 session | `SwitchProvider` 保留 session_id |

### 1.3 非目标

- 不实接任何云账号凭据（仅 spec 约束）。
- 不引入第三方 LLM SDK（如 LangChain、litellm 等）。
- 不在 Provider 层做业务校验（prompt 内容、token 预算）—— 由 ContextEngine / Heartbeat 主理。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `src/darvin-agent/internal/llm/` | 占位（推断） |
| `src/shared/darvin-api.ts` | `DarvinApi` 已含 `agent.request(method, params)` 形状 |
| `specs/features/agent-llm-encapsulation/` | 早期 spec，思路可参考但不直接复用 |
| `specs/features/provider-mistral/` 等 | 本次新增，每个 provider 一份 |

## 3. 用户/系统场景

### 场景 1：注册新 provider

**Given** Go runtime 启动，settings.json 含 `providers.openai.apiKey` 与 `providers.gemini.apiKey`
**When** `Registry.Bootstrap()` 读取配置并 `Register(NewOpenAIProvider(...))` / `Register(NewGeminiProvider(...))`
**Then** ProviderRegistry 持有 2 个 provider；`Get("openai")` 返回非 nil

### 场景 2：切换 provider 保留 session

**Given** session `s1` 已在 openai 跑 3 轮
**When** 设置页切到 `gemini`，runtime 调用 `SwitchProvider("gemini", "gemini-2.5-flash")`
**Then** session_id 仍为 `s1`；events 序列继续追加；user-visible 提示「已切换，下条消息生效」

### 场景 3：能力不匹配降级

**Given** user 在 OpenAI 选中 `claude-sonnet-4-5` 实际不可用
**When** runtime 检测到 provider 不支持目标 capability
**Then** 抛 `provider.capability.unsupported` 错误；UI 提示降级方案（默认 fallback provider）

### 场景 4：配置错误

**Given** `apiKey` 缺失 / `baseUrl` 不是 https
**When** 启动期 `Validate()`
**Then** panic 时记录 `provider.config.invalid` 事件并退出 supervisor，不进入 `ready`

## 4. 功能需求

### FR-1 接口

```go
// runtime/provider/types.go
type Provider interface {
    ID() string
    Capabilities() CapabilitySet
    Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
    Stream(ctx context.Context, req *ChatRequest, onDelta func([]byte) error) error
    ToolUse(ctx context.Context, req *ToolRequest) (*ToolResponse, error)
    Usage(req *ChatRequest, resp *ChatResponse) UsageBreakdown
}
```

### FR-2 Registry

```go
type Registry struct {
    mu     sync.RWMutex
    items  map[string]Provider
    order  []string
}

func (r *Registry) Register(p Provider) error
func (r *Registry) Get(id string) (Provider, bool)
func (r *Registry) List() []ProviderSummary
func (r *Registry) Remove(id string) error
func (r *Registry) SwitchProvider(id, model string) error
```

- `Register` 校验 `p.ID()` 全局唯一。
- `SwitchProvider` 是 atomic，刷新 `current` 字段。

### FR-3 能力声明

```go
type CapabilitySet struct {
    ToolUse        bool
    Vision         bool
    AudioIn        bool
    AudioOut       bool
    StructuredOut  bool
    SystemTool     bool
    Streaming      bool
}
```

### FR-4 配置校验

```go
type Validator interface {
    Validate(cfg ProviderConfig) error
}
```

校验项：

- `apiKey` 非空（含 mock key `darvin-mock-*`）
- `baseUrl` 是 https / http://localhost / http://127.0.0.1 三选一
- `model` 与 provider registry 对照（如有内置 default model）

### FR-5 并发安全

- `Registry.items` 读写都加锁。
- `current` provider 切换使用 atomic.Pointer。

### FR-6 切换语义

`SwitchProvider` 不重连 WS、不重置 session、不丢 events。Go runtime 在下个 `Chat/Stream` 调用时使用新 provider。

## 5. 安全与隐私

- `apiKey` 不进日志；仅出现 `***redacted***`。
- 远端调用必须 TLS（`http://localhost` 仅 dev 模式允许）。
- 模型 ID / base URL 通过 `ProviderConfig` 注入，runtime 不向用户输入预测。
- 任何 provider 错误统一归一化 `DarvinProviderError`，含 `code/retryable/cause` 字段。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| Register 同名 provider | 返回 `ErrProviderAlreadyRegistered` |
| Get 未知 provider | 返回 `ErrProviderNotFound`，UI 提供「添加 Provider」按钮 |
| Stream 中途断流 | 上抛 `ErrProviderStreamTruncated`，由 Failover 接管 |
| Tools schema 不被支持 | `CapabilityCheck` 在调用前 fail-fast |
| provider panic | watcher 隔离，仅本 provider 失效，registry 不重建 |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/provider/registry.go`（新） | Registry + 锁 |
| `src/darvin-agent/internal/provider/types.go`（新） | 接口与 Capability |
| `src/darvin-agent/internal/provider/config.go`（新） | Validator |
| `src/darvin-agent/internal/provider/errors.go`（新） | 归一化错误 |
| `src/darvin-agent/internal/provider/factory.go`（新） | Provider constructor |
| `src/shared/darvin-api.ts` | `DarvinProviderSummary` / `DarvinProviderConfig` 类型 |
| `src/renderer/services/providers.ts`（新） | composable：UI 增删 provider |

## 8. 实施顺序与依赖

1. `types.go` + `errors.go` + 单元测试 5 条。
2. `registry.go` + `SwitchProvider` 单元测试 5 条（并发场景）。
3. `factory.go` + `config.go` + `Validator` 单元测试 5 条。
4. 各 Provider spec 接入 factory。
5. UI 接入 `services/providers.ts`。

> 前置：`specs/features/darvin-api-extension/` 已确认（channel 名称枚举）。
> 并行：每个 Provider spec 可独立推进。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go 单元测试覆盖注册 / 切换 / 移除 / 并发 / 校验 ≥ 10 条 |
| V3 | 启动期配置错误 fail-fast 并发 `provider.config.invalid` |
| V4 | `npm run smoke -- provider-registry` 通过 |
| V5 | `npm run dev` 手动验证：设置页切换 provider 不中断 session |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- 具体 Provider 协议细节由各 provider spec 主理。
- Failover 由 `failover-and-circuit-breaker` 主理。
