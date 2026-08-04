# 01 — Harness 核心接口

> 状态: 已实现(部分结论已被 07 修正) · 2026-08-04
> 父 spec: `2026-08-04-harness-architecture-design.md`
> 后续: `2026-08-04-harness-core-corrections.md`(spec 07)
> 输出: `internal/harness/` 新包,1045 行实现 + 1071 行测试

> ⚠️ 本文是**实施当时的 as-built 记录**,保留原样以存档"当初建了什么、为什么"。
> 实施后对照 OpenClaw 源码复核,发现 13 处偏移(4 处 P0),修正方案见 spec 07。
> 受影响的结论已在下文就地标注,以 spec 07 为准。

## 1. 设计目标

为 darvin-cowork 引入一个**稳定、可扩展、可测试**的 harness 抽象层。harness = "一个能跑 agent 一次的实现"。OpenClaw 在 `src/agents/harness/` 有 9 个 capability、20k 行代码,我们平移成 Go,只覆盖核心,测试覆盖率优先。

实际落地 1045 行(不含测试),低于原估算 860 行的同时多带了 `policy.go` 与 capability 校验。

## 2. 文件结构

```
internal/harness/
├── types.go              (424)  Harness 接口 + 6 个可选 capability 接口 + 全部参数/结果类型
├── registry.go           (135)  进程级注册表
├── support.go            (137)  capability 校验 + Rank 评分
├── policy.go             (71)   Policy 解析 + Resolve
├── lifecycle.go          (166)  RunAttemptWithLifecycle + per-session generation
├── builtin_embedded.go   (112)  NewEmbedded(EmbeddedConfig)
├── harness_test.go       (185)  fixture + embedded 集成
├── registry_test.go      (201)
├── support_test.go       (246)
├── policy_test.go        (135)
└── lifecycle_test.go     (298)
```

文件名用下划线(`builtin_embedded.go`),跟仓库既有的 `agent_mini_loop.go` / `text_delta_hook.go` 一致,不用 OpenClaw 的连字符。

`tool-surface-bridge.go` / `tool-result-middleware.go` 见 spec 05,`selection.go` / `runtime-plugin.go` 见 spec 03,本 spec 不产出。

### 2.1 依赖约束

`go list -deps ./internal/harness` 只返回它自己 —— 本包**不 import 任何 `internal/` 包**。

代价是 `Usage` / `ImageAttachment` / `ToolCallRecord` 三个结构在 harness 包内重新声明,而不是复用 `internal/agents/protocol` 与 `internal/agents/queue`。这是有意的:harness 必须能被一个跟 embedded runtime 毫无关系的 backend(CLI 子进程、远程服务)实现,类型转换由 wiring 层负责。

## 3. 核心类型:`Harness` interface

### 3.1 必选接口

原设计把 9 个 capability 全塞进一个 interface,后果是任何 backend 都要写 9 个 stub 方法。实际实现拆成**必选接口 + 可选接口**:必选 8 个方法,可选能力各自独立 interface,harness 按需实现。

```go
// internal/harness/types.go
package harness

// Harness 是任意 backend agent runtime 的统一抽象。调用方(gateway /
// 调度器 / 其它 harness)只通过这个 interface 交互,不感知具体 backend。
//
// 实现是进程级单例;RunAttempt 本身 stateless,需要的一切都从
// RunAttemptParams 进来。
type Harness interface {
    // 标识 ────────────────────────────────────────
    ID() string          // "embedded" / "cli" / "codex" / ...
    Label() string       // "Darvin-Cowork embedded agent"
    PluginID() string    // 注册来源 plugin id,内置为 ""

    // Capability 声明 + 评分 ───────────────────────
    Capabilities() Capabilities
    Supports(SupportContext) SupportResult

    // 主路径 ──────────────────────────────────────
    RunAttempt(ctx context.Context, params RunAttemptParams) (*AttemptResult, error)

    // Session lifecycle ───────────────────────────
    Reset(ctx context.Context, params ResetParams) error
    Dispose(ctx context.Context) error
}
```

### 3.2 可选 capability 接口

```go
type Compactor      interface { Compact(ctx, CompactParams) (*CompactResult, error) }
type Classifier     interface { Classify(ctx, *AttemptResult, *RunAttemptParams) Classification }
type SideQuestioner interface { RunSideQuestion(ctx, SideQuestionParams) (*SideQuestionResult, error) }
type SessionForker  interface { SessionFork(ctx, SessionForkParams) (*SessionForkResult, error) }
type TurnFinalizer  interface { FinalizeSettledTurn(ctx, SettledTurnParams) (*SettledTurnResult, error) }
type UsageReporter  interface { UsageSnapshot(ctx, UsageSnapshotParams) (*UsageSnapshot, error) }
type AutoSelector   interface { AutoSelection() *AutoSelectionHint }
```

`RuntimeArtifact` / `AuthBinding` 两个 capability **未实现**:仓库里没有 artifact binding 与 auth profile 层,等对应 spec 落地再加。

> ⚠️ 07 修正:此处漏列。同样缺失的还有 `deliveryDefaults`、`authBootstrap`、
> `sessionFork.upstreamKinds`、SessionFork 失败码。完整清单见 spec 07 §6;
> 其中 `DeliveryDefaults` 因 spec 03 第 5 维依赖,由 spec 07 C13 补上。

### 3.3 `Capabilities`:声明与实现的双轨

`Capabilities()` 是**声明**,让 Selection 不实例化 session 就能评分;可选 interface 是**实现**。两者必须一致,由 `VerifyCapabilities` 在 `Register` 时强制:

```go
type Capabilities struct {
    Healthy bool   // false 时被 auto-selection 过滤掉

    Compact             bool
    Classify            bool
    SideQuestion        bool
    SessionFork         bool
    FinalizeSettledTurn bool
    UsageSnapshot       bool

    ContextEngineHost  []string // 可托管的 ctx engine id,空 = 任意
    DelegatedExecution []string // 允许委派进来的 plugin id,空 = 不接受委派
}
```

> ⚠️ 07 修正(C4):`ContextEngineHost` 建模轴错了。OpenClaw 存的是**能力动词集**
> (bootstrap / assemble-before-prompt / compact / ...),按 operation 对 engine
> 声明的 requiredCapabilities 做超集检查,且不声明 = 不支持(fail-closed);
> 这里写成了 engine id 白名单 + 空为放行(fail-open)。见 spec 07 §3 C4。

规则是**单向**的:

- 「声明了但没实现」→ `Register` 直接报错拒绝。这类 bug 在启动时炸,不会拖到第一次调用。
- 「实现了但没声明」→ 合法。embedded harness 的 `Compact` 方法永远在,但只有 `EmbeddedConfig.Compact` 钩子非 nil 时才声明,`Implements(h, CapCompact)` 才返回 true。

调用方一律走 `Implements(h, cap)` 而不是裸 type assertion,否则未声明的能力会因为具体类型碰巧带了方法而被误激活。

### 3.4 `RunAttemptParams` / `AttemptResult`

原 spec 的 30+ 字段引用了 `provider.RouteOverride` / `auth.Profile` / `sessions.AuthStorage` / `sessions.ModelRegistry` / `runtimeplan.AgentRuntimePlan` / `protocol.ToolCallRecord` —— 这些是 OpenClaw 的包,本仓库**一个都不存在**。实际字段收敛到现有能力:

```go
type RunAttemptParams struct {
    SessionID  string
    SessionKey string

    Prompt      string
    Images      []ImageAttachment
    Attachments []string

    Provider string
    Model    string

    WorkspaceDir string
    Cwd          string

    // 空值由 RunAttemptWithLifecycle 补齐
    RunID         string
    MessageID     string
    UserMessageID string

    TimeoutMs    int    // 0 = 不限时
    LifecycleGen uint64 // 由 lifecycle 打戳,harness 原样回传

    OnExecutionStarted func()
    OnExecutionPhase   func(ExecutionPhase)
    OnRunProgress      func(RunProgress)
    OnAttemptTimeout   func()
    OnAttemptAbort     func()
}

type AttemptResult struct {
    Status        AttemptStatus  // ok / error / aborted / timeout
    StopReason    string
    AssistantText string
    TotalTurns    int
    TotalUsage    Usage
    ToolCalls     []ToolCallRecord

    Classification Classification // ok / drift / stalled / failed
    LastError      error

    LifecycleGen uint64
    Superseded   bool  // 跑的过程中 session 被 reset 了
    DurationMs   int64
}
```

同时删掉了 `PrepareAuthSupport` / `PrepareRouteSupport` 两个 interface 方法 —— 没有 auth 层可以 prepare。等 P4 provider / auth spec 落地再补回。

### 3.5 其它 capability 参数

| capability | Go method | 参数 | 返回 |
|---|---|---|---|
| Compact | `Compact(ctx, params)` | `CompactParams{SessionID, TargetTokens}` | `*CompactResult{NewTokens, RemovedMessages, TookMs}` |
| Classify | `Classify(ctx, result, params)` | `*AttemptResult, *RunAttemptParams` | `Classification` |
| SideQuestion | `RunSideQuestion(ctx, params)` | `SideQuestionParams{SessionID, Question, Provider, Model}` | `*SideQuestionResult{Text, Usage}` |
| SessionFork | `SessionFork(ctx, params)` | `SessionForkParams{Source, TargetKey, Upstream}` | `*SessionForkResult{SessionID, Created}` |
| FinalizeSettledTurn | `FinalizeSettledTurn(ctx, params)` | `SettledTurnParams{SessionID, RunID, MessageID, Result}` | `*SettledTurnResult{Changed}` |
| UsageSnapshot | `UsageSnapshot(ctx, params)` | `UsageSnapshotParams{Provider, Model}` | `*UsageSnapshot{WindowUsed, WindowLimit, ResetsAtUnix}` |
| Reset | `Reset(ctx, params)` | `ResetParams{SessionID, Reason}` | `error` |
| Dispose | `Dispose(ctx)` | - | `error` |

## 4. Registry

### 4.1 为什么没有 Symbol

原 spec 要求平移 OpenClaw 的 `Symbol.for("openclaw.agentHarnessRegistryState")` + `globalThis[KEY]`。**这层在 Go 里不需要**:OpenClaw 用 Symbol 是因为 Node 里同一模块可能被加载多份,而 Go 单二进制里 package 级 var 本身就是进程单例。加一层假 Symbol 是 cargo cult。

`internal/harness/symbol/symbol.go` 不存在,也不该存在。

### 4.2 API

```go
type Registration struct {
    Harness       Harness
    OwnerPluginID string    // "" = 内置
    RegisteredAt  time.Time
}

func Register(h Harness, ownerPluginID string) error  // 幂等,同 id 覆盖
func MustRegister(h Harness, ownerPluginID string)    // 启动期用,失败 panic
func Unregister(id string)
func Get(id string) (Harness, bool)                   // 空 id = 最高优先级的健康 harness
func Lookup(id string) (*Registration, bool)
func List() []*Registration
func ResetRegistryForTests()
```

`Register` 会先跑 `VerifyCapabilities`,声明与实现不一致直接拒绝。

`List()` 按「AutoSelection 优先级降序 → id 升序」排序,**不暴露 map 迭代顺序**。`Get("")` 依赖这个顺序,否则"第一个健康的"每次调用结果都不一样。

### 4.3 与 OpenClaw 的命名差异

| OpenClaw | darvin-cowork | 说明 |
|---|---|---|
| `Symbol.for(...)` + `globalThis[KEY]` | package 级 var | Go 单二进制天然进程单例 |
| `registerAgentHarness` | `Register` | 已经在 harness 包里,`RegisterHarness` 结巴 |
| `getRegisteredAgentHarness` | `Get` | 同上 |
| `listRegisteredAgentHarnesses` | `List` | 同上 |
| `clearAgentHarnesses` (test only) | `ResetRegistryForTests` | 名字自带用途 |

## 5. Policy 与 Rank

`policy.go` 负责"这个 session 该用哪个 harness"。spec 03 的 5 维评分是它的扩展,不是替代。

```go
type Policy struct {
    HarnessID     string // 空 = auto
    AllowFallback bool   // pin 的 harness 不在时是否回落到 auto
}

func ParsePolicy(raw string) Policy   // "" / "auto" → auto;"embedded" → 软 pin;"embedded!" → 硬 pin
func (p Policy) Resolve(sc SupportContext) (Harness, error)
```

`Resolve` 的优先级:

1. `sc.RequestedHarnessID` 非空 → 精确查,**找不到直接报错,永不回落**。调用方点名要某个 harness,就是要它或者要错误。
2. `p.HarnessID` 非空 → 精确查;找不到且 `AllowFallback` 为 false 报错,否则继续。
3. auto → `Rank(sc)` 取第一个。

`Rank` 过滤掉:不健康 / 托管不了请求的 ctx engine / 拒绝委派方 plugin / provider 不在自己 AutoSelection 白名单 / `Supports` 返回 false。存活的按「优先级降序 → id 升序」排。

> ⚠️ 07 修正(C1/C2/C3):`Rank` 把 `AutoSelectionHint.Priority` 当 bonus 叠加到
> `Supports` 的 Priority 上,而 embedded 两处填的是同一个值 → **优先级被算两次**,
> 实测声明 6 的能压过声明 10 的。OpenClaw 的 autoSelection 根本没有 priority 字段。
> 另有:白名单空列表在 OpenClaw 表示 explicit-only(硬拒绝),这里写成了放行;
> provider id 未归一化,大小写不同即不匹配。见 spec 07 §3 C1–C3。

## 6. Lifecycle

### 6.1 职责

`RunAttemptWithLifecycle` 是调用方真正打交道的入口,不直接调 `Harness.RunAttempt`:

1. 入参校验(harness / SessionID / Prompt 非空)
2. 补齐 RunID / MessageID / UserMessageID(caller 给了就不动)
3. 打 lifecycle generation 戳
4. `TimeoutMs > 0` 时套 `context.WithTimeout`
5. `recover` 兜住 backend 的 panic —— 第三方 plugin harness 不能把进程带走
6. 结果归一化:`result == nil` 时按 err 合成,status 与 err 对齐(`DeadlineExceeded` → timeout,`Canceled` → aborted)
7. generation 变了 → `result.Superseded = true`
8. 声明了 Classify 的跑分类

### 6.2 为什么不发 event

原 spec §6.1 的示例代码里写了 `eventBus.Emit(event.RunStartEvent{...})`。**实现里没有,也不能有**。

`Agent.Run` 已经在发 `RunStartEvent` / `RunEndEvent` / `AgentEndEvent`(`internal/agents/dispatcher.go:111,216`)。在 lifecycle 再发一遍,订阅者会收到重复事件,直接破坏 README §5 不变式 2「EventBus 协议稳定」。

事件归 runtime 发,lifecycle 只做编排。

> ⚠️ 07 修正(C5/C6/C10):三点。
> 1. lifecycle **没有**做 ctx engine host 断言,OpenClaw 在每次 attempt 的 prepare
>    阶段都断言一次(pin 的 harness 也要过闸);这里只在 `Rank` 里过滤,
>    `RequestedHarnessID` 与直调 `RunAttemptWithLifecycle` 两条路径都能绕过。
> 2. 分类前不清除旧 classification,retry / wrapper 留下的陈旧标签会存活。
> 3. 本节「不发 event」的理由对 embedded 成立但被过度泛化:OpenClaw 是按 harness
>    过滤(`harness.id !== "openclaw"`)而非整体删除,且发的是独立的诊断总线。
>    spec 07 C10 加一个默认 nil 的 Observer 钩子,不违反不变式 2。

### 6.3 Generation 与 abort 竞态

```go
func BumpLifecycleGeneration(sessionID string) uint64
func LifecycleGeneration(sessionID string) uint64
func ResetLifecycleForTests()
```

per-session 单调计数器。`embedded.Reset` **无条件**递增它(哪怕没配 Reset 钩子),所以一个还在飞的 attempt 结束时会发现 generation 变了,`Superseded` 置位,调用方可以丢弃这个结果而不是让它覆盖新一轮的状态。

> ⚠️ 07 修正(C11):`AttemptResult.Superseded` 与 `lifecycle.go` 的注释都写了
> 「或被更新的 attempt 抢占」,但 `BumpLifecycleGeneration` 全仓库只有
> `embedded.Reset` 一个调用点 —— **并发 attempt 之间不会互相 supersede**。
> 注释需收窄到只承诺 reset 语义。另:该机制在 OpenClaw 无对应物(那边用
> `terminal` / `externalAbort`),属本仓库自创,注释应写明免得后来者去对面找。

## 7. Builtin Embedded Harness

### 7.1 为什么不是空 stub

原 spec 让 Phase 1 写一个 `RunAttempt` 直接 `return nil, errors.New("...not yet implemented (Phase 2)")` 的壳。两个问题:

- 这是 `AGENTS.md` 注释规范明令禁止的「阶段、版本、迭代规划类注释」
- Phase 2 要把这个类型重写一遍

改成钩子注入:

```go
const EmbeddedID = "embedded"

type Runner func(ctx context.Context, params RunAttemptParams) (*AttemptResult, error)

type EmbeddedConfig struct {
    Label   string
    Run     Runner                                                    // nil → RunAttempt 返回 ErrNotImplemented
    Compact func(ctx, CompactParams) (*CompactResult, error)          // nil → 不声明 Compact capability
    Reset   func(ctx, ResetParams) error
    Dispose func(ctx) error

    Providers         []string // provider 白名单,空 = 任意
    Priority          int
    ContextEngineHost []string
}

func NewEmbedded(cfg EmbeddedConfig) Harness
```

`Capabilities().Healthy = (cfg.Run != nil)`,`Capabilities().Compact = (cfg.Compact != nil)`。

后续 phase 只需在 wiring 层传闭包,harness 包本身不用动,**也永远不需要 import `internal/agents`**。

## 8. 测试

| 文件 | 用例数 | 覆盖 |
|---|---|---|
| `registry_test.go` | 11 | 注册/覆盖/注销/空 id 选举/跳过不健康/顺序确定性/capability 校验拒绝/并发 |
| `support_test.go` | 14 | VerifyCapabilities 双向/Implements 双条件/Compact helper/Rank 5 类过滤/优先级排序 |
| `policy_test.go` | 8 | ParsePolicy 表驱动/String 往返/显式优先/硬软 pin/回落/无候选 |
| `lifecycle_test.go` | 12 | 成功/校验/补 ID/错误/abort/timeout/panic 兜底/superseded/分类/回调序列 |
| `harness_test.go` | 6 | fixture + embedded 端到端(注册 → Resolve → RunAttemptWithLifecycle) |

实测:

```
go test -count=1 -race -cover ./internal/harness/...
ok  darvin-cowork/backend/internal/harness  1.058s  coverage: 92.9% of statements
```

51 个用例 / 92.9% 覆盖,高于 spec 要求的 ≥18 / ≥80%。

## 9. 验收结果

```
go build ./...                            PASS
go vet ./...                              PASS
gofmt -l internal/                        clean
go test -count=1 -short ./...             21 个包全 PASS(既有 test 0 改动 0 失败)
make lint-agents-boundaries               PASS
go list -deps ./internal/harness | grep darvin-cowork   → 只有它自己
```

## 10. 风险与缓解(实施后复盘)

| 风险 | 结果 |
|---|---|
| Symbol 要在 Go 里自己实现 | 消解:Go 不需要这层,直接删 |
| `Harness` 只有 1 个实现,过度设计 | 部分缓解:拆成必选+可选接口后,最小实现只有 8 个方法 |
| `RunAttemptParams` 30+ 字段,builder 泛滥 | 消解:砍到 17 个字段,不需要 Functional Options |
| Phase 1 全 stub,覆盖率上不去 | 消解:钩子注入让 stub 也可测,实测 92.9% |
| 声明与实现不一致 | 新增缓解:`VerifyCapabilities` 在 `Register` 时拒绝 |
| 第三方 plugin harness panic 带走进程 | 新增缓解:lifecycle `recover` 兜底 |

## 11. 与其它 spec 的接口

> ⚠️ 07 修正:下方「已稳定 / 已就位」三条结论**作废**。`SupportContext` 缺
> `providerOwnerStatus` / `providerOwnerPluginIds`(spec 03 需要),
> `Capabilities.ContextEngineHost` 的建模轴不对(spec 06 需要),
> `Capabilities` 还缺 `DeliveryDefaults`(spec 03 第 5 维需要)。以 spec 07 为准。

- **02 spec**: `Harness.RunAttempt` 内部调瘦身后的 `agent.Agent.Run`。接法是给 `EmbeddedConfig.Run` 传闭包,harness 包不动。
- **03 spec**: Selection 扩展 `Rank`,新增 3 个维度到现有的 healthy / ctxEngineHost / delegation / provider / Supports 之上;~~`SupportContext` / `SupportResult` 已稳定~~(见上方修正)。Plugin 注册走 `Register(h, pluginID)`。
- **04 spec**: Gateway 调 `RunAttemptWithLifecycle`;注册用 `harness.Register(harness.NewEmbedded(...), "")`;关闭路径调 spec 07 新增的 `DisposeAll`。
- **05 spec**: ~~`Capabilities.ContextEngineHost` 已就位~~(见上方修正)。
- **06 spec**: `EmbeddedConfig.Compact` 钩子 + `harness.Compact(ctx, h, params)` helper 已就位;host capability 匹配须先落 spec 07 C4/C5。
- **07 spec**: 本 spec 产出的 13 处修正,Phase 3 之前必须完成 P0 四项。
- **未来**: `RuntimeArtifact` / `AuthBinding` 两个 capability 与 `PrepareAuthSupport` / `PrepareRouteSupport` 待 auth / provider spec 落地后补。
