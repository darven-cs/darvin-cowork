# 01 — Harness 核心接口

> 状态: 草案 v1 · 2026-08-04
> 父 spec: `00-harness-architecture-design.md`
> 输出: `internal/harness/` 新包,~860 行 Go

## 1. 设计目标

为 darvin-cowork 引入一个**稳定、可扩展、可测试**的 harness 抽象层。harness = "一个能跑 agent 一次的实现"。OpenClaw 在 `src/agents/harness/` 有 9 个 capability、20k 行代码,我们平移成 Go,目标 ~860 行(覆盖核心即可,测试覆盖率高)。

## 2. 文件结构

```
internal/harness/
├── types.go           (250)  Harness interface + 9 capability 子类型
├── registry.go        (80)   进程级 Symbol-backed Map
├── policy.go          (150)  HarnessPolicy 解析 + 静态/隐式 runtime 选择
├── support.go         (180)  capability matching 评分
├── builtin-embedded.go(70)   NewEmbeddedHarness (包 Agent)
├── lifecycle.go       (350)  Harness.RunAttempt 接入 + pre/post hook + diagnostics
├── tool-surface-bridge.go       (150) 见 spec 05
├── tool-result-middleware.go    (280) 见 spec 05
├── runtime-plugin.go  (180)  plugin 动态加载(见 spec 03)
├── selection.go       (450)  5 维评分(见 spec 03)
├── harness_test.go
├── registry_test.go
├── support_test.go
└── lifecycle_test.go
```

## 3. 核心类型:`Harness` interface

### 3.1 总体结构

```go
// internal/harness/types.go
package harness

// Harness 是任意 backend agent runtime 的统一抽象。调用方(gateway /
// 调度器 / 其它 harness)只通过这个 interface 交互,不感知具体 backend。
//
// 一个 Harness 是单例(进程级),Factory 模式构造;RunAttempt 的每次调用
// 是 stateless 的,内部可绑定到 session / run 等 long-lived 资源。
type Harness interface {
    // 标识 ────────────────────────────────────────
    ID() string                                 // "embedded" / "cli" / "codex" / ...
    Label() string                              // "Darvin-Cowork embedded agent"
    PluginID() string                           // 注册来源 plugin id,内置为 ""

    // Capability 声明 ─────────────────────────────
    Capabilities() HarnessCapabilities          // 这个 harness 实现哪些能力

    // 评分 ──────────────────────────────────────────
    Supports(ctx SupportContext) SupportResult  // 选 harness 时调
    AutoSelection() *AutoSelectionHint          // provider id 静态白名单(可空)

    // 主路径 ────────────────────────────────────────
    RunAttempt(ctx context.Context, params RunAttemptParams) (*AttemptResult, error)
    // FinalizeSettledTurn 选实现
    FinalizeSettledTurn(ctx context.Context, params SettledTurnParams) (*SettledTurnResult, error)

    // Session lifecycle ────────────────────────────
    Reset(ctx context.Context, params ResetParams) error
    Dispose(ctx context.Context) error

    // 评分辅助(供 Selection 跨 provider 决策)
    PrepareAuthSupport(ctx AuthSupportContext) PreparedAuthSupport
    PrepareRouteSupport(ctx RouteSupportContext) PreparedRouteSupport
}
```

### 3.2 9 个 capability 子类型

```go
// HarnessCapabilities 描述这个 harness 实现了哪些 capability。空结构体
// = 实现了。nil = 不实现。
type HarnessCapabilities struct {
    Compact             *CompactCapability             // 可选:实现 compact()
    Classify            *ClassifyCapability            // 可选:实现 classify()
    RunSideQuestion     *SideQuestionCapability        // 可选:实现 runSideQuestion()
    SessionFork         *SessionForkCapability         // 可选:实现 sessionFork()
    RuntimeArtifact     *RuntimeArtifactCapability     // 可选:实现 runtimeArtifact()
    AuthBinding         *AuthBindingCapability         // 可选:实现 authBinding()
    FetchUsageSnapshot  *UsageSnapshotCapability       // 可选:实现 fetchUsageSnapshot()
    ContextEngineHost   []ContextEngineHostCapability  // 可选:声明支持的 ctx engine
    DelegatedExecution  []string                       // 哪些 plugin id 可委派本 harness
}
```

### 3.3 `RunAttempt` 必选入口的完整参数

```go
// RunAttemptParams 是 RunAttempt 的输入。一个 attempt 等价于 darvin-cowork
// 现有的 Agent.Run() 单次调用(包含 turn 循环直到自然结束或 abort)。
type RunAttemptParams struct {
    SessionID   string
    SessionKey  string
    SessionFile string

    Prompt          string
    PromptImages    []queue.ImageRef    // 见 internal/agents/queue
    Attachments     []string

    Provider    string                  // "anthropic" / "openai" / ...
    Model       string                  // "claude-sonnet-4-5" / ...
    ModelRoute  *provider.RouteOverride // 选定的具体路由(可有可无)
    FallbackActive   bool
    FallbackReason   string

    WorkspaceDir    string
    AgentDir        string
    Cwd             string

    Config          agent.Config        // 见 internal/agents
    AuthProfile     *auth.Profile
    AuthStorage     *sessions.AuthStorage
    ModelRegistry   *sessions.ModelRegistry

    // Abort / cancel
    AbortSignal context.Context
    TimeoutMs   int
    RunTimeoutOverrideMs int

    // 进度回调(由 harness 内部或 lifecycle 包转 event emit)
    OnExecutionStarted  func()
    OnExecutionPhase    func(phase ExecutionPhase)
    OnLaneWait          func(durationMs int)
    OnRunProgress       func(progress RunProgress)
    OnAttemptTimeout    func(armed bool)
    OnAttemptAbort      func()

    // Hook 上下文(由 lifecycle 包注入)
    PreparedAuth    PreparedAuthSupport
    PreparedRoute   PreparedRouteSupport
    RuntimePlan     *runtimeplan.AgentRuntimePlan
    TrajectoryRecorder *TrajectoryRecorder

    // 内部分配
    MessageID    string                 // 由 lifecycle 注入
    RunID        string
    UserMsgID    string
}

// AttemptResult 是 RunAttempt 的输出
type AttemptResult struct {
    Status         AttemptStatus        // OK / ERROR / ABORTED / TIMEOUT
    StopReason     string               // "end_turn" / "tool_use" / ...
    AssistantText  string               // 完整最终回复
    TotalTurns     int
    TotalUsage     protocol.Usage
    ToolCalls      []protocol.ToolCallRecord
    LastError      error
    LifecycleGen   int                  // 用于 abort race 检测
}
```

### 3.4 其它 capability 参数(摘要)

| capability | Go method | 参数 | 返回 |
|---|---|---|---|
| Compact | `Compact(ctx, params)` | `CompactParams{SessionID, TargetTokens}` | `*CompactResult{NewTokens, RemovedMessages, TookMs}` |
| Classify | `Classify(ctx, result, attempt)` | `*AttemptResult, *RunAttemptParams` | `ResultClassification` ("ok" / "drift" / "stalled" / ...) |
| RunSideQuestion | `RunSideQuestion(ctx, params)` | `SideQuestionParams{Question, Provider, Model, SessionID}` | `*SideQuestionResult{Text}` |
| SessionFork | `SessionFork(ctx, params)` | `SessionForkParams{TargetKey, Source, Upstream}` | `*SessionForkResult` (created/failed) |
| RuntimeArtifact | `RuntimeArtifact().Validate(ctx, binding)` | `*RuntimeArtifactBinding` | `bool` |
| AuthBinding | `AuthBinding().Fingerprint(ctx, params)` | `AuthProfileID, AuthProfileStore, AgentDir, Config` | `string` |
| FetchUsageSnapshot | `FetchUsageSnapshot(ctx, ctx)` | `ProviderFetchUsageContext` | `*ProviderUsageSnapshot` |
| Reset | `Reset(ctx, params)` | `ResetParams{AgentID, SessionID, Reason}` | `error` |
| Dispose | `Dispose(ctx)` | - | `error` |

## 4. Registry:进程级 Symbol-backed Map

### 4.1 实现

```go
// internal/harness/registry.go
package harness

import "sync"

var (
    registryStateKey = symbol.New("darvin-cowork.harnessRegistry")
    registryState    *registryStateT
    once             sync.Once
)

type registryStateT struct {
    mu        sync.RWMutex
    harnesses map[string]*RegisteredHarness
}

type RegisteredHarness struct {
    Harness       Harness
    OwnerPluginID string    // "" = 内置
    RegisteredAt  time.Time
}

func ensureRegistry() *registryStateT {
    once.Do(func() {
        // Symbol-backed singleton: tests can isolate by creating
        // a fresh process or by resetHarnessRegistryForTests()
        existing := globalLoad(registryStateKey)
        if existing != nil {
            registryState = existing.(*registryStateT)
            return
        }
        registryState = &registryStateT{harnesses: make(map[string]*RegisteredHarness)}
        globalStore(registryStateKey, registryState)
    })
    return registryState
}

// RegisterHarness 幂等注册(同 id 覆盖)。调用方负责构造 Harness。
// 通常在 main.go 启动时调,或在 runtime plugin 加载时调。
func RegisterHarness(h Harness, ownerPluginID string) error {
    id := strings.TrimSpace(h.ID())
    if id == "" {
        return errors.New("harness: ID required")
    }
    r := ensureRegistry()
    r.mu.Lock()
    defer r.mu.Unlock()
    r.harnesses[id] = &RegisteredHarness{
        Harness: h, OwnerPluginID: ownerPluginID, RegisteredAt: time.Now(),
    }
    return nil
}

// UnregisterHarness 移除(用于 plugin 卸载 / 测试 cleanup)
func UnregisterHarness(id string) {
    r := ensureRegistry()
    r.mu.Lock()
    defer r.mu.Unlock()
    delete(r.harnesses, strings.TrimSpace(id))
}

// GetHarness 精确按 id 取。空 id = 第一个健康的(向后兼容)
func GetHarness(id string) (Harness, bool) {
    r := ensureRegistry()
    r.mu.RLock()
    defer r.mu.RUnlock()
    if id != "" {
        e, ok := r.harnesses[strings.TrimSpace(id)]
        return unwrap(e), ok
    }
    // 第一个健康
    for _, e := range r.harnesses {
        if e.Harness.Capabilities().Healthy() {
            return unwrap(e), true
        }
    }
    return nil, false
}

// ListHarnesses 返回所有已注册的快照(用于 status / plugin 列表)
func ListHarnesses() []*RegisteredHarness { ... }

// ResetHarnessRegistryForTests 仅供测试
func ResetHarnessRegistryForTests() { ... }
```

### 4.2 与 OpenClaw 差异

| OpenClaw | darvin-cowork | 说明 |
|---|---|---|
| `Symbol.for("openclaw.agentHarnessRegistryState")` | `symbol.New("darvin-cowork.harnessRegistry")` | Go 不支持 Symbol,自己实现 |
| `globalThis[KEY]` | `sync.Once + globalState` (我们用 sync.Map 或全局 var) | Go 等价 |
| `registerAgentHarness` | `RegisterHarness` | 命名风格化 |
| `getRegisteredAgentHarness` | `GetHarness` | 短名 |
| `listRegisteredAgentHarnesses` | `ListHarnesses` | 短名 |
| `clearAgentHarnesses` (test only) | `ResetHarnessRegistryForTests` | 加注释 |

## 5. Builtin Embedded Harness(占位,02 spec 详细设计)

```go
// internal/harness/builtin-embedded.go
package harness

import "darvin-cowork/backend/internal/agents"

// NewEmbeddedHarness 返回包了当前 *agent.Agent 的 Harness 实现。
// Phase 1: 仅占位,Capabilities 全 false,RunAttempt 暂返回 ErrNotImplemented。
// Phase 2: 与 02 spec 的 agent.Agent 重构同步落地。
func NewEmbeddedHarness() Harness {
    return &embeddedHarness{}
}

type embeddedHarness struct{}

func (h *embeddedHarness) ID() string   { return "embedded" }
func (h *embeddedHarness) Label() string { return "Darvin-Cowork embedded agent" }
func (h *embeddedHarness) PluginID() string { return "" }

func (h *embeddedHarness) Capabilities() HarnessCapabilities {
    return HarnessCapabilities{
        // Phase 1: 全 false。Phase 2 启用 Compact, Phase 5 启用 Classify 等。
    }
}

func (h *embeddedHarness) Supports(ctx SupportContext) SupportResult {
    // Phase 1: 所有 provider 都返回 true(向后兼容)
    return SupportResult{Supported: true, Priority: 0}
}

func (h *embeddedHarness) RunAttempt(ctx context.Context, params RunAttemptParams) (*AttemptResult, error) {
    return nil, errors.New("harness: embedded RunAttempt not yet implemented (Phase 2)")
}
// ...其它方法也 stub
```

## 6. Lifecycle:RunAttempt 接入

### 6.1 职责

`internal/harness/lifecycle.go` 是调用方真正打交道的入口。它做 6 件事:

```go
// internal/harness/lifecycle.go
package harness

// RunAttemptWithLifecycle 是 gateway / spawner 调用的入口。它包了原始
// harness.RunAttempt,加上 pre-hook / post-hook / diagnostics / abort race 检测。
func RunAttemptWithLifecycle(ctx context.Context, h Harness, params RunAttemptParams) (*AttemptResult, error) {
    // 1. 入参校验
    if err := validateRunAttemptParams(&params); err != nil {
        return nil, err
    }
    // 2. abort race 检测(lifecycle generation)
    gen := captureAgentRunLifecycleGeneration(params.SessionID)
    params.LifecycleGen = gen

    // 3. 分配 RunID / MessageID(若 caller 未给)
    if params.RunID == "" { params.RunID = uuid.NewString() }

    // 4. emit agent.run.started(在 gateway event bus 上,不在 harness 里)
    eventBus.Emit(event.RunStartEvent{...})
    defer eventBus.Emit(event.RunEndEvent{...})

    // 5. 调 harness.RunAttempt(ctx, params)
    result, err := h.RunAttempt(ctx, params)

    // 6. classification + diagnostics
    if h.Capabilities().Classify != nil {
        if c := h.Classify(ctx, result, &params); c != nil {
            result.Classification = *c
        }
    }

    return result, err
}
```

### 6.2 与 OpenClaw 对应

| OpenClaw | darvin-cowork |
|---|---|
| `runAgentHarnessLifecycleAttempt` (lifecycle.ts:271) | `RunAttemptWithLifecycle` |
| `emitAgentHarnessRunStarted/Completed/Error` | 调 eventBus.Emit |
| `withFallbackDiagnosticTrace` | 暂省略(Phase 4 加) |
| `withFallbackFinalizationDiagnosticTrace` | 暂省略 |
| `agentRunCompletion` 推断 stopReason | Phase 2 |

## 7. 测试要求

| 文件 | 测试 | 必含 case |
|---|---|---|
| `registry_test.go` | `TestRegisterAndGet` / `TestGetFirstHealthy` / `TestUnregister` / `TestDuplicateID` / `TestEmptyID` / `TestConcurrent` / `TestSymbolBackedAcrossImports` | ≥ 6 |
| `support_test.go` | `TestSupportsPriority` / `TestSupportsNoMatch` / `TestCapabilityMatching` | ≥ 4 |
| `lifecycle_test.go` | `TestRunAttemptSuccess` / `TestRunAttemptError` / `TestRunAttemptAbort` / `TestLifecycleGeneration` | ≥ 6 |
| `harness_test.go` (集成) | `TestBuiltinEmbeddedStub` / `TestRunAttemptNotImplemented` | ≥ 2 |

总测试数 ≥ 18,覆盖率 ≥ 80%。

## 8. Phase 1 提交清单

```
$ git add internal/harness/
$ git status  # 11 个 .go 文件
$ go build ./...   # 通过
$ go vet ./...     # 通过
$ go test -count=1 -short ./internal/harness/...  # 全 PASS
$ git commit -m "feat(harness): add Harness interface + Registry + Lifecycle skeleton

平移 OpenClaw src/agents/harness/ 核心:
- 9-capability Harness interface
- 进程级 Symbol-backed Registry
- Lifecycle wrapper (RunAttemptWithLifecycle)
- BuiltinEmbedded stub (Phase 2 接入 agent.Agent)

不动现有代码:gateway / acp / agents 全保留

Spec: specs/features/agent-harness-architecture/01-harness-core-interface.md"
```

## 9. 风险与缓解

| 风险 | 缓解 |
|---|---|
| Symbol 在 Go 里要自己实现,vitest 风格的多 globalThis 隔离不存在 | 用 sync.Once 初始化,测试用 `ResetHarnessRegistryForTests` 重建 |
| `Harness` interface 在 Phase 1 只有 1 个实现,过度设计 | 接受:Phase 1 是骨架,Phase 2-3 加 builtin + cli 时证明价值 |
| `RunAttemptParams` 30+ 字段,builder 模式泛滥 | 用 required vs optional 注释区分;Phase 1 暂不引入 Functional Options(后续 spec 可加) |
| Test 覆盖率 ≥ 80% 在 Phase 1 难达到(都是 stub) | 接受 Phase 1 覆盖率 50%,Phase 2+ 提升到 80% |

## 10. 与其它 spec 的接口

- **02 spec**: `Harness.RunAttempt` 内部调瘦身后的 `agent.Agent.Run`,需要本 spec 暴露的 `RunAttemptParams` 字段
- **03 spec**: Selection 通过 `Harness.Supports(ctx)` 评分,需要 `SupportContext` / `SupportResult` 已稳定
- **04 spec**: Gateway 调 `RunAttemptWithLifecycle`,需要本 spec 入口函数已稳定
- **05 spec**: `HarnessCapabilities.ContextEngineHost` 由本 spec 定义,05 spec 用它做 tool 桥
- **06 spec**: `HarnessCapabilities.Compact` 由本 spec 定义,06 spec 用它接 ctx engine compact
