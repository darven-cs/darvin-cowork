# Agent LLM 底层封装 — v2 设计文档

> **本文件是 v2 迭代**。前序：
> - **v0**（`2026-07-28-agent-llm-encapsulation-design.md`）已交付并落地：`ModelProvider` 三方法（Name/Complete/Stream）、统一数据类型（Role/Message/Tool/ToolChoice/CompletionRequest/CompletionResponse/FinishReason/Usage）、统一错误类型（ProviderError + 4 个错误码）、7 个 StreamEvent 实现、Anthropic provider（含 HTTP 客户端 / 2 次重试 / SSE 解析 / tool_use 增量聚合）、Provider 工厂 + 注册表。
> - **v1**（`2026-07-29-agent-llm-encapsulation-design.md`）设计完成（FR-1~FR-9 = P6+P7 全部内容），但**未落地**。v1 一次吞 9 个 FR 体量过大、不利于逐项 review。
>
> **v2 的取舍**：把 v1 的 9 个 FR 拆成 **4 个里程碑（M6.1~M6.4）**，按依赖关系排序，每个里程碑对应一次可独立 review / 合并的 PR。本 spec 是 **v2 计划文档**，逐个里程碑展开时再迭代 v3 / v4 / v5。

---

## 1. 概述

### 1.1 问题 / 背景

v1 文档（785 行）虽然结构完整，但一次性交付存在三个风险：

1. **接口破坏**：`ModelProvider` interface 加 `ListModels` 方法会让 v0 已落地的 `anthropic.Provider` 暂时不满足接口（虽然 FR-1+FR-6+FR-7 一起合进来就修复，但中间态不可编译）。
2. **review 成本**：9 个 FR 涉及 17+ 文件，新增 6 个新文件（`openai/` 3 个、`gemini/` 3 个），单 PR review 容易漏细节。
3. **逐项回退困难**：若 Gemini 流式解析与 OpenAI tool_calls index 聚合的实现细节与 spec 不一致，冲突面太大。

`docs/agent/api/m1-model-provider.md` 与 `docs/agent/01_LLM_INTERFACE.md` 的对齐要求已被 v1 罗列，v2 不再重复 v1 的逐项对齐表，**直接复用 v1 §1.1 的对照表作为本 spec 的合规要求**。

### 1.2 目标

v2 把 v1 的 9 个 FR 拆成 4 个里程碑：

| 里程碑 | FR 范围 | 涉及文件 | 关键交付 |
|--------|--------|---------|---------|
| **M6.1 metadata-only** | v1 FR-2（ModelDescriptor + ModelRegistry）+ FR-4（Usage cache + cost 字段） | `model_registry.go` / `cost.go` / `compat.go` / `types.go` 增量字段 | 不动 interface，纯加字段 + 新增元数据；无功能回归 |
| **M6.2 thinking + Retry-After** | v1 FR-3（ThinkingStart/Delta/End）+ FR-9（Retry-After 解析） | `events.go` 增量 + `httpclient.go` 增量 + `anthropic/stream.go` 增量 | 新增事件类型 + 重试策略强化；Anthropic first-party 实测 |
| **M6.3 provider 多样化** | v1 FR-1（ListModels）+ FR-6（OpenAI Completions）+ FR-7（Gemini） | `provider.go`（interface +ListModels）+ `openai/` 新目录 + `gemini/` 新目录 | 第 2 / 3 个 provider；ListModels 一次性落地三处 |
| **M6.4 上层贯通** | v1 FR-5（StreamOptions）+ FR-8（executor / event 配套） | `types.go` 5 字段 + `executor.go` 4 行 + `event/event.go` 1 类型 | 上层 Agent 循环可消费 cache / cost / thinking；端到端贯通 |

**v2 本 spec 文档**只展开 **M6.1** 的完整设计（其他里程碑写纲要占位 + 引用 v1）。理由：M6.1 是唯一不依赖其他里程碑的纯增量改动，可以立刻进入实现；M6.2~M6.4 必须按顺序递进。

### 1.3 非目标

- v2 **不**重复 v1 已罗列的"非目标"（Bedrock / Vertex / Azure / WebSocket / 跨模型消息转换 / Failover 链 等）
- v2 **不**实现 M6.2 / M6.3 / M6.4 的具体设计（其余里程碑待 v3 / v4 / v5 展开）
- v2 **不**改 v0 已落地的 `ModelProvider` interface（保留 Name / Complete / Stream 三方法），M6.3 才加 `ListModels`
- v2 **不**改 `cmd/app/main.go` 装配流程（与 v0 承诺一致）
- v2 **不**引入第三方 SDK（继续用 v0 自实现 HTTP 客户端）

---

## 2. 用户场景

### 场景 1：用户在 UI 切换模型时，ContextEngine 拿到 contextWindow

**Given** `agent.model = "claude-sonnet-4-5"`，Agent 循环启动
**When** ContextEngine 调 token 估算函数（`ctxengine.EstimateMessageTokens`）
**Then** 通过 `llm.GetModel("claude-sonnet-4-5")` 拿到 `ModelDescriptor.ContextWindow = 200000`，据此算出"消息 60000 tokens 已用 30%"的预算状态
**And** `ModelDescriptor` 是 metadata-only 字段，不发任何网络请求

### 场景 2：Anthropic 返回 cache 命中 token 数

**Given** 同一 session 第二次发同样 messages（除 user 内容外不变）
**When** Anthropic provider 收到 SSE 末尾 `message_delta.usage.cache_read_input_tokens=5000`
**Then** `Usage.CacheReadTokens=5000` 由 anthropic provider 写入；`Cost.CacheRead` 字段由于本次未调 CalculateCost 还是 0（M6.1 不触发 Cost 计算，Cost 字段为占位）

### 场景 3：用户配错 model 字符串

**Given** `agent.model = "claude-future-99"`（未注册）
**When** `llm.GetModel("claude-future-99")` 调用
**Then** 返回 `(ModelDescriptor{}, false)`，调用方决定怎么处理（v1 决定 provider 不报 error，继续发请求；M6.1 仅 registry 层不报错）

---

## 3. 功能需求（M6.1 范围）

### FR-1：ModelDescriptor 结构（v1 FR-2 的实现版）

```go
// internal/agent/llm/types.go（增量）

type APIKind string
const (
    APIAnthropicMessages   APIKind = "anthropic-messages"
    APIOpenAICompletions   APIKind = "openai-completions"
    APIGeminiGenerativeAI  APIKind = "google-generative-ai"
)

type InputModality string
const (
    InputText  InputModality = "text"
    InputImage InputModality = "image"
)

type ModelCost struct {
    Input      float64 // $/M tokens
    Output     float64
    CacheRead  float64 // 通常 = Input * 0.1
    CacheWrite float64 // 通常 = Input * 1.25（5min 缓存写入）
}

type Compat struct {
    SupportsToolCalls      bool
    SupportsImageInput     bool
    SupportsUsageInStream  bool
    SupportsStrictToolMode bool
}

// ModelDescriptor 描述一个具体模型实例的元数据。
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

type ThinkingLevel string
const (
    ThinkingOff    ThinkingLevel = "off"
    ThinkingLow    ThinkingLevel = "low"
    ThinkingMedium ThinkingLevel = "medium"
    ThinkingHigh   ThinkingLevel = "high"
    ThinkingMax    ThinkingLevel = "max"
)
```

**不引入 `Usage.Cost` 字段**（v1 FR-4 的 Cost 计算推迟到 M6.2 / M6.4 配合 CalculateCost 一起做），M6.1 只定义 `ModelCost`（挂在 Model 上）。

### FR-2：ModelRegistry 进程全局表

```go
// internal/agent/llm/model_registry.go（新文件）

type ModelRegistry struct {
    mu      sync.RWMutex
    byID    map[string]ModelDescriptor
    byProv  map[string][]string // provider -> model IDs
}

func NewModelRegistry() *ModelRegistry
func (r *ModelRegistry) RegisterModel(m ModelDescriptor)
func (r *ModelRegistry) MustRegisterModel(m ModelDescriptor) // 重复 ID panic
func (r *ModelRegistry) Get(id string) (ModelDescriptor, bool)
func (r *ModelRegistry) ListByProvider(name string) []ModelDescriptor
func (r *ModelRegistry) All() []ModelDescriptor

var DefaultModelRegistry = NewModelRegistry()
```

**重复 ID 语义**：与 `RegisterProvider` 一致，重复 ID 必须 panic（v1 决定）。

**不**改 `provider.go`：`ModelProvider` interface 暂时不加 `ListModels`（M6.3 才加）。M6.1 仅提供 `DefaultModelRegistry.Get(id)` 静态查询。

### FR-3：anthropic provider init() 注册 Model 集合

```go
// internal/agent/llm/anthropic/provider.go 的 init() 末尾追加
func init() {
    llm.RegisterProvider("anthropic", New) // v0 已存在

    // v1 已定义的 Claude 模型
    llm.DefaultModelRegistry.MustRegisterModel(llm.ModelDescriptor{
        ID: "claude-sonnet-4-5", Name: "Claude Sonnet 4.5", Provider: "anthropic",
        APIVersion: llm.APIAnthropicMessages, ContextWindow: 200000, MaxTokens: 8192,
        Reasoning: true,
        ThinkingMap: map[llm.ThinkingLevel]string{
            llm.ThinkingLow: "1024", llm.ThinkingMedium: "4096",
            llm.ThinkingHigh: "8192", llm.ThinkingMax: "16384",
        },
        Input:  []llm.InputModality{llm.InputText, llm.InputImage},
        Cost:   llm.ModelCost{Input: 3.0, Output: 15.0, CacheRead: 0.3, CacheWrite: 3.75},
        Compat: llm.Compat{SupportsToolCalls: true, SupportsImageInput: true, SupportsUsageInStream: true},
    })
    // 注册更多 Claude 模型（claude-opus-4 / claude-haiku-4 / claude-3-5-sonnet 等）
    // 详见 v1 FR-2 的 Cost 表
}
```

M6.1 **只**注册 Anthropic 的 Model；OpenAI / Gemini 的注册在 M6.3 一并交付。

### FR-4：Usage 加 cache 字段（v1 FR-4 的拆分版）

```go
// internal/agent/llm/types.go（增量，Usage 修改）
type Usage struct {
    PromptTokens     int
    CompletionTokens int
    TotalTokens      int

    // v2\M6.1 新增（M6.4 才会触发实际写入 + Cost 计算）
    CacheReadTokens     int
    CacheWriteTokens    int
    CacheWrite1hTokens  int
}
```

**M6.1 行为**：
- 字段已定义，零值仍是 0
- `anthropic/stream.go` **不**做 cache 解析（由 M6.2 配合 anthropic 流式 fixture 一起做）
- Usage 上**不**挂 `UsageCost`（M6.4 一起做）

**M6.1 只解决"类型可定义、字段可零值"**，不改 anthropic provider 行为。

### FR-5：Compat 默认值

```go
// internal/agent/llm/compat.go（新文件）
var (
    DefaultAnthropicCompat = Compat{
        SupportsToolCalls: true, SupportsImageInput: true,
        SupportsUsageInStream: true, SupportsStrictToolMode: false,
    }
    DefaultOpenAICompat = Compat{
        SupportsToolCalls: true, SupportsImageInput: true,
        SupportsUsageInStream: true, SupportsStrictToolMode: false,
    }
    DefaultGeminiCompat = Compat{
        SupportsToolCalls: true, SupportsImageInput: true,
        SupportsUsageInStream: false, SupportsStrictToolMode: false,
    }
)
```

M6.1 仅定义这些常量，M6.3 在 OpenAI / Gemini provider 中引用。

---

## 4. 实现方案

### 4.1 目录结构（M6.1 增量）

```
src/darvin-agent/internal/agent/llm/
├── provider.go            # v0 不动
├── types.go               # v0 + Usage 3 字段 + FR-1 ModelDescriptor 等类型
├── events.go              # v0 不动
├── errors.go              # v0 不动
├── httpclient.go          # v0 不动
├── registry.go            # v0 不动
├── model_registry.go      # 🆕 ModelRegistry + DefaultModelRegistry
├── compat.go              # 🆕 Compat 默认值常量
└── anthropic/             # v0 + init() 追加 Model 注册
    └── provider.go
```

**0 新增 provider 目录**（OpenAI / Gemini 留给 M6.3）。
**0 修改 anthropic provider 的 stream.go / convert.go**（cache 解析留给 M6.2）。

### 4.2 关键设计决策

#### 4.2.1 拆分成 4 个里程碑的理由

- **M6.1 metadata-only**：纯加类型 + 字段 + 进程全局表，不改任何调用方，0 回归风险
- **M6.2 thinking + Retry-After**：新增 3 个事件类型 + 增强 HTTP 客户端；Anthropic provider 流式解析扩展（一次性把 thinking 块 + cache 解析 + Retry-After 一并稳定）
- **M6.3 provider 多样化**：interface 加方法 + 新建两个 provider 目录；ListModels 一次补齐三方
- **M6.4 上层贯通**：CompletionRequest 加 5 字段 + executor 累加 + event package 透传；端到端 validate

#### 4.2.2 Usage 字段拆分策略

v1 一口气在 Usage 加 4 字段（Cache + Cost）。v2 拆成两步：

- M6.1：只加 3 个 cache counter 字段（无 Cost）
- M6.4：加 `UsageCost` 字段 + `CalculateCost` 函数 + executor 累加

理由：Cost 计算需要上游有 ModelDescriptor 可查（已经有）+ Usage 真实 cache counter（已经在 M6.1 落地）；但 Cost 计算触发的 DoneEvent 装配改动属于"实质行为变化"，应放在 M6.4 单独 review。

#### 4.2.3 ModelRegistry vs Provider.ListModels

M6.1 只提供 `DefaultModelRegistry.Get/All/ListByProvider`，**不**实现 `Provider.ListModels` 方法。

- 上层（ContextEngine）当前只需要 `Get(id)` 静态查表
- `ListModels` 是 provider 接口的扩展（M6.3 会一次性补三个 provider）
- M6.1 时如果加了 `ListModels` 方法，v0 anthropic provider 就 break 接口，破坏 v0 承诺

#### 4.2.4 不引入 ThinkingConfig / CacheRetention

v1 FR-5（CompletionRequest 5 字段）整体推迟到 M6.4：
- ThinkingConfig 暂时在 `Extra map[string]any` 凑合（v0 行为）
- CacheRetention / SessionID / PromptCacheKey 暂时不接
- Signal 字段不引入

理由：M6.1 / M6.2 / M6.3 都不需要这几个字段就能跑通；M6.4 一次性引入，避免半生半熟。

### 4.3 关键代码骨架

```go
// internal/agent/llm/model_registry.go
package llm

import "sync"

type ModelRegistry struct {
    mu     sync.RWMutex
    byID   map[string]ModelDescriptor
    byProv map[string][]string
}

func NewModelRegistry() *ModelRegistry {
    return &ModelRegistry{
        byID:   map[string]ModelDescriptor{},
        byProv: map[string][]string{},
    }
}

var DefaultModelRegistry = NewModelRegistry()

func (r *ModelRegistry) RegisterModel(m ModelDescriptor) {
    r.mu.Lock()
    defer r.mu.Unlock()
    if _, exists := r.byID[m.ID]; exists {
        panic("llm: model " + m.ID + " already registered")
    }
    r.byID[m.ID] = m
    r.byProv[m.Provider] = append(r.byProv[m.Provider], m.ID)
}

func (r *ModelRegistry) MustRegisterModel(m ModelDescriptor) {
    r.RegisterModel(m)
}

func (r *ModelRegistry) Get(id string) (ModelDescriptor, bool) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    m, ok := r.byID[id]
    return m, ok
}

func (r *ModelRegistry) ListByProvider(name string) []ModelDescriptor {
    r.mu.RLock()
    defer r.mu.RUnlock()
    ids := r.byProv[name]
    out := make([]ModelDescriptor, 0, len(ids))
    for _, id := range ids {
        out = append(out, r.byID[id])
    }
    return out
}

func (r *ModelRegistry) All() []ModelDescriptor {
    r.mu.RLock()
    defer r.mu.RUnlock()
    out := make([]ModelDescriptor, 0, len(r.byID))
    for _, m := range r.byID {
        out = append(out, m)
    }
    return out
}
```

```go
// internal/agent/llm/anthropic/provider.go（init() 追加）
func init() {
    llm.RegisterProvider("anthropic", New)

    models := []llm.ModelDescriptor{
        {
            ID: "claude-sonnet-4-5", Name: "Claude Sonnet 4.5",
            Provider: "anthropic", APIVersion: llm.APIAnthropicMessages,
            ContextWindow: 200000, MaxTokens: 8192, Reasoning: true,
            ThinkingMap: map[llm.ThinkingLevel]string{
                llm.ThinkingLow: "1024", llm.ThinkingMedium: "4096",
                llm.ThinkingHigh: "8192", llm.ThinkingMax: "16384",
            },
            Input:  []llm.InputModality{llm.InputText, llm.InputImage},
            Cost:   llm.ModelCost{Input: 3.0, Output: 15.0, CacheRead: 0.3, CacheWrite: 3.75},
            Compat: DefaultAnthropicCompat,
        },
        // claude-opus-4 / claude-3-5-sonnet-latest / claude-3-5-haiku-latest
        // 按 v1 FR-2 表填
    }
    for _, m := range models {
        llm.DefaultModelRegistry.MustRegisterModel(m)
    }
}
```

### 4.4 测试策略

M6.1 测试集中在三个文件：

- `internal/agent/llm/model_registry_test.go`：
  - Register 后 Get 命中
  - 重复 ID panic
  - ListByProvider 过滤
  - All 遍历完整
  - Get 未注册 ID 返回 `(ModelDescriptor{}, false)`
- `internal/agent/llm/types_test.go`（增量）：
  - Usage 字段 round-trip（缓存字段零值）
  - ModelDescriptor JSON tag（无）— 跳过
- `internal/agent/llm/anthropic/provider_test.go`（增量）：
  - `init()` 后 `DefaultModelRegistry.Get("claude-sonnet-4-5")` 命中
  - ListByProvider("anthropic") 至少返回 1 个 Model

仓库当前没有 `go test` runner（`AGENTS.md` §测试），M6.1 落地测试代码但不要求 CI 跑通。

### 4.5 依赖

v0 已用：`net/http` / `encoding/json` / `bufio` / `strings` / `strconv` / `errors` / `context` / `time` / `sync` / `internal/logger`。

**M6.1 不新增任何依赖**。

---

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| `Get(id)` 查不到 model | 返回 `(ModelDescriptor{}, false)`，不 panic |
| `Get("")` 空字符串 | 同上，返回 false |
| 重复 model ID 注册 | `MustRegisterModel` panic（与 `RegisterProvider` 语义一致） |
| Provider 名为空字符串 | 注册端不阻止（与 v0 一致），但 `Model.Provider` 字段照样写入 |
| 并发调用 `Get` / `List` | 走 `sync.RWMutex` 保护 |
| `init()` 顺序：anthropic provider 先于 openai 注册 | M6.1 只注册 anthropic，无顺序问题；M6.3 需要时再做 |
| `Model.ThinkingMap` 字段为 `nil` 但 `Reasoning=true` | M6.1 不校验；M6.2 解析时 nil-safe 处理 |
| `Model.Input` 为空切片 | M6.1 不校验；M6.2 解析时默认 `[]InputModality{InputText}` |

---

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/darvin-agent/internal/agent/llm/types.go` | 修改：Usage 加 3 字段；新增 ModelDescriptor / APIKind / InputModality / ThinkingLevel / ModelCost / Compat |
| `src/darvin-agent/internal/agent/llm/model_registry.go` | 🆕 ModelRegistry + DefaultModelRegistry |
| `src/darvin-agent/internal/agent/llm/model_registry_test.go` | 🆕 单元测试 |
| `src/darvin-agent/internal/agent/llm/compat.go` | 🆕 DefaultAnthropicCompat / DefaultOpenAICompat / DefaultGeminiCompat |
| `src/darvin-agent/internal/agent/llm/anthropic/provider.go` | 修改：init() 追加 Model 描述符注册 |
| `src/darvin-agent/internal/agent/llm/anthropic/provider_test.go` | 修改：新增 init() 注册后默认表断言 |
| `specs/features/agent-llm-encapsulation/2026-07-29-agent-llm-encapsulation-design-v2.md` | 🆕 本 spec 文件 |

**不修改**：
- `provider.go` / `events.go` / `errors.go` / `httpclient.go` / `registry.go`：v0 已完整
- `anthropic/stream.go` / `anthropic/convert.go`：M6.2 才动
- `executor.go` / `event/event.go` / `cmd/app/main.go`：M6.4 才动
- `ctxengine/*`：M6.4 联动，本 spec 不动

不涉及：renderer、preload、`src/main/`、Tailwind / Vue 栈。

---

## 7. 验收标准

**通用**：
- [ ] `cd src/darvin-agent && go build ./...` 编译通过
- [ ] `go vet ./...` 无警告
- [ ] `gofmt -l .` 干净
- [ ] 既有 v0 行为不回归（Anthropic provider 流式产出 `Start → TextDelta* → Done` 不变）
- [ ] `executor.go` 不回归（无字段改动即可）
- [ ] M6.1 不引入第三方依赖

**FR-1 ModelDescriptor**：
- [ ] `ModelDescriptor` 暴露至少 11 个字段（ID/Name/Provider/APIVersion/ContextWindow/MaxTokens/Reasoning/ThinkingMap/Input/Cost/Compat）
- [ ] `ThinkingMap[ThinkingHigh]` 是 string 数值（"8192"），Reasoning=false 时整张表允许为空
- [ ] `ModelCost` 4 个字段都是 `$/M tokens` 单位
- [ ] `Compat` 4 个 bool 字段均有合理默认值
- [ ] `APIKind` 三种 `anthropic-messages` / `openai-completions` / `google-generative-ai` 全部定义

**FR-2 ModelRegistry**：
- [ ] `DefaultModelRegistry.Get("claude-sonnet-4-5")` 返回非空 `ModelDescriptor`
- [ ] 重复 ID 注册 panic
- [ ] `ListByProvider("anthropic")` 至少返回 1 个 Model
- [ ] `All()` 至少返回 4 个 Model（Claude 系列）
- [ ] `_test.go` 覆盖 Register / Get / ListByProvider / All / 重复 panic

**FR-3 anthropic init**：
- [ ] 至少注册 4 个 Claude Model（sonnet-4-5 / opus-4 / 3-5-sonnet / 3-5-haiku）
- [ ] 每个 Model 字段完整无空指针（`ThinkingMap` 为 nil 时 Reasoning=false）

**FR-4 Usage cache 字段**：
- [ ] `Usage` 加 3 字段（CacheReadTokens / CacheWriteTokens / CacheWrite1hTokens）
- [ ] 零值兼容：v0 既有测试 fixture（assert `Usage.PromptTokens = 10000`）仍通过
- [ ] **未**引入 `UsageCost` 字段

**FR-5 Compat 默认值**：
- [ ] `DefaultAnthropicCompat` 4 个 bool 字段全为 true（除 `SupportsStrictToolMode=false`）
- [ ] `DefaultOpenAICompat` / `DefaultGeminiCompat` 同样定义
- [ ] 至少 1 个 `_test.go` 断言这些常量存在

**集成手测路径**（v0 §4.4 风格的扩展）：
```bash
cd src/darvin-agent
go build ./... && go vet ./...
# 临时验证脚本：
cat > /tmp/registry_check.go <<'EOF'
package main
import (
    "fmt"
    "darvin-cowork/backend/internal/agent/llm"
)
func main() {
    m, ok := llm.DefaultModelRegistry.Get("claude-sonnet-4-5")
    fmt.Println(m, ok)
}
EOF
go run /tmp/registry_check.go
# 期望输出：{claude-sonnet-4-5 Claude Sonnet 4.5 anthropic anthropic-messages 200000 8192 ...} true
```

---

## 8. 后续 spec 候选（不在本 spec 范围内）

| 候选 | 内容 | 依赖 |
|------|------|------|
| **v3 (M6.2)** | ThinkingStart/Delta/End 事件 + Retry-After 解析 + Anthropic provider 流式 thinking 块识别 + cache token 解析 | M6.1 |
| **v4 (M6.3)** | `ModelProvider` interface 加 `ListModels` 方法 + OpenAI Completions provider + Gemini provider + 三家 Model 描述符 | M6.1 |
| **v5 (M6.4)** | `CompletionRequest` 5 字段（Thinking/CacheRetention/SessionID/PromptCacheKey/Signal）+ executor 4 行累加 + event package 加 `ThinkingDeltaEvent` 透传 + `UsageCost` + `CalculateCost` | M6.2 + M6.3 |
| `messages-transform` | 跨 provider 消息转换（图像降级 / 思考签名丢弃 / tool_call id 规范化 / 孤儿工具结果补齐） | M6.4 |
| `openai-responses-provider` | OpenAI 新版 Responses API + tool call ID 规范化 | M6.3 |
| `failover-chain` | 主 provider 失败自动切备用 + 熔断器 | M6.3 |
| `bedrock-vertex-azure` | 三个长尾 provider | 远期 |

每个里程碑对应一份新的 spec 文件（v3 / v4 / v5），不向前回退。

---

## 9. 与 v1 spec 的关系

v1 是 P6+P7 全部 9 个 FR 的设计，v2 是把 v1 拆成 4 个 reviewable 里程碑的**计划文档**。本 spec **不替代 v1**：

- **继承**：v1 §FR-1~§FR-9 全部需求 + §4 实现方案 + §5 边界情况 + §7 验收标准仍是本系列 spec 的权威设计
- **拆分**：v1 的 9 个 FR 按依赖关系切到 M6.1 / M6.2 / M6.3 / M6.4
- **v2 仅展开 M6.1**（metadata-only），M6.2~M6.4 写纲要占位 + 引用 v1
- **回归保护**：v0 §7 验收标准（`/v0` 阶段的 anthropic 流式 / Usage 3 字段 / 错误码 4 个）必须仍然通过

v2 + v1 合并后的 `internal/agent/llm/` 最终目标接口（v5 完成后）：

```go
type ModelProvider interface {
    Name() string
    Complete(ctx, *CompletionRequest) (*CompletionResponse, error)
    Stream(ctx, *CompletionRequest) (*StreamingResponse, error)
    ListModels(ctx) ([]ModelDescriptor, error)  // v3 / M6.3
}

// StreamEvent: 7 类（v0）+ 3 类（M6.2）= 10 类
// Usage: 3 字段（v0）+ 3 字段（M6.1 cache counter）+ 1 字段（M6.4 UsageCost）= 7 字段
// CompletionRequest: 12 字段（v0）+ 5 字段（M6.4）= 17 字段
// Provider 注册: 1（v0）+ 2（M6.3）= 3 个
// Model 描述符注册: 4+（M6.1 anthropic）+ 7+（M6.3 openai + gemini）
```

---

## 10. 实施顺序

v2 本 spec 落地后，**立即**进入 M6.1 实现（按 §6 涉及文件清单 7 项内容）。M6.1 完成后单独立项 v3 spec，进入 M6.2。

- ✅ v0 — 已交付
- ✅ v1 — 设计完成
- ⏳ v2 — 本 spec，待审核
- 📋 v3 — M6.2 实施 spec（M6.1 通过后启动）
- 📋 v4 — M6.3 实施 spec（M6.2 通过后启动）
- 📋 v5 — M6.4 实施 spec（M6.3 通过后启动）
