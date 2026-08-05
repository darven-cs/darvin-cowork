# 02 — Agent 重构

> 状态: 草案 v1 · 2026-08-04
> 父 spec: `00-harness-architecture-design.md`
> 前置: `01-harness-core-interface.md`
> 输出: `internal/agents/` 重组,~600 行新代码,~400 行迁出

## 1. 目标

把当前 `internal/agents/agent.go` (532 行) + `dispatcher.go` (427 行) + `agent_mini_loop.go` (51 行) = **1010 行集中在一个 `*Agent` 类型**的状态,重构成:

- `*Agent` 只剩 10 个 public 方法,所有 transient state 迁出
- 5 个职责子模块各自独立可测
- 完全兼容现有测试 (`agent_test.go` + `dispatcher_test.go` + `agent_mini_loop_test.go` + 所有下游 consumer)

## 2. 重构前/后对比

### 2.1 Public API 缩表

| 现有方法 | 留下/迁出/删 | 落地 |
|---|---|---|
| `New(cfg)` | 留下 | 不变 |
| `Session()` / `SessionHandle()` | 留下 | 不变 |
| `Provider()` | 留下 | 不变 |
| `ModelName()` | 留下 | 不变 |
| `Tools()` | 留下 | 不变 |
| `Instructions()` | 留下 | 不变 |
| `Config()` | 留下 | 不变 |
| `Logger()` | 留下 | 不变 |
| `Emit(ev)` | 留下 | 不变 |
| `Subscribe(buffer)` | 留下 | 不变 |
| `IsRunning()` | 留下 | 不变 |
| `Run(ctx)` | 留下 | **签名不变** |
| `Prompt(ctx, ...)` | 留下 | **签名不变** |
| `Steer(ctx, ...)` | 留下 | **签名不变** |
| `FollowUp(ctx, ...)` | 留下 | **签名不变** |
| `Abort(ctx)` | 留下 | **签名不变** |
| `RunSkillSession(ctx, ...)` | 留下 | **签名不变** |
| `RecordUsage(u)` | 留下 | 不变 |
| `LastUsage()` | 留下 | 不变 |
| `Assembler()` | 留下 | 不变 |
| `SystemSections()` | 留下 | 不变 |
| `AssemblerEnabled()` | 留下 | 不变 |
| `SetGrantedReads(paths)` | 迁出 | → `*permissionGate` |
| `ApprovePath(path)` | 迁出 | → `*permissionGate` |
| `EvaluatePermission(tool, args)` | 迁出 | → `*permissionGate` |
| `RequestPermission(ctx, req)` | 迁出 | → `*permissionGate` |
| `ResolvePermission(reqID, result)` | 迁出 | → `*permissionGate` |
| `deliverPermission(...)` | 迁出 | → `*permissionGate` |
| `HasPermissionRule(...)` | 迁出 | → `*permissionGate` |
| `AddPermissionRule(...)` | 迁出 | → `*permissionGate` |
| `AttachMessageIDSrc(src)` | 迁出 | → `*messageIDBridge` |
| `AttachRunIDSrc(src)` | 迁出 | → `*messageIDBridge` |
| `AttachUserMessageIDSrc(src)` | 迁出 | → `*messageIDBridge` |
| `CurrentMessageID()` | 迁出 | → `*messageIDBridge` |
| `CurrentRunID()` | 迁出 | → `*messageIDBridge` |
| `CurrentUserMessageID()` | 迁出 | → `*messageIDBridge` |
| `enqueue(mode, ...)` | 内部 | 保留(私有) |
| `persistUserMessage(...)` | 内部 | 保留(私有) |
| `persistAssistantMessages(...)` | 内部 | 保留(私有) |
| `persistSession(...)` | 内部 | 保留(私有) |
| `formatImportedNote(...)` | 内部 | 保留(私有) |
| `approxTurns(...)` | 内部 | 保留(私有) |
| `toLLMImages(...)` | 内部 | 保留(私有) |

**净结果**: 30+ public → 19 public(其中 6 个留作 facade,见 §4)

### 2.2 字段拆分

`Agent` struct 当前 30+ 字段,按职责拆 5 个子 struct:

| 字段组 | 子模块 | 字段 |
|---|---|---|
| 基础引用 | `*Agent` 自身 | `name / instructions / model / provider / session / store / msgStore / logger / cfg / tools / exec / bus / queue` |
| Run lifecycle | `*runController` | `runMu / state / cancelFn / totalTurns / totalUsage` |
| Permission gate | `*permissionGate` | `permMu / pendingPerms / permRules / 60s timeout` |
| MessageID bridge | `*messageIDBridge` | `msgIDSrc / runIDSrc / userMsgIDSrc` |
| Transient state | `*Agent` 自身(transient,run-scoped) | `runImportedNote / runSkillPrompt / runSkillTools` |
| Usage tracker | `*usageTracker` | `lastUsageMu / lastUsage` |
| ctx engine handle | `*Agent` 自身 | `assembler / assemblerEnabled` |

## 3. 子模块设计

### 3.1 `internal/agents/perm/permission_gate.go` (新子包)

```go
// Package perm implements the per-Agent permission state machine:
// - in-flight request channels (pendingPerms)
// - "remember this session" auto-allow rules (permRules)
// - 60s default-deny timeout
//
// 拆出理由: 9 个 public method 都跟 permission 相关,集中到独立
// 子包后:*Agent 不再持有 permMu/pendingPerms/permRules 3 个字段,
// 子包可独立测试,future change 不会污染 agent.go。
package perm

import (
    "context"
    "sync"
    "time"

    "github.com/google/uuid"
    "go.uber.org/zap"

    "darvin-cowork/backend/internal/agents/event"
    "darvin-cowork/backend/internal/agents/executor"
    "darvin-cowork/backend/internal/agents/protocol"
)

const DefaultTimeout = 60 * time.Second

type PermissionRequest = executor.PermissionRequest
type PermissionResult = executor.PermissionResult

type PermissionEval = protocol.PermissionEval

type pendingPermission struct {
    ch      chan PermissionResult
    timeout *time.Timer
}

type rule struct {
    ToolName  string
    Level     string
    Reason    string
}

// Gate is the permission state machine. Construct via NewGate, owned by
// one *Agent instance. NOT goroutine-shared across agents.
type Gate struct {
    mu       sync.Mutex
    bus      EventEmitter    // small interface satisfied by *event.Bus
    logger   *zap.Logger
    timeout  time.Duration

    pending  map[string]*pendingPermission
    rules    []rule
}

type EventEmitter interface {
    Emit(ev event.Event)
}

// NewGate constructs a Gate. timeout=0 → DefaultTimeout.
func NewGate(bus EventEmitter, logger *zap.Logger, timeout time.Duration) *Gate {
    if timeout <= 0 { timeout = DefaultTimeout }
    return &Gate{
        bus: bus, logger: logger, timeout: timeout,
        pending: make(map[string]*pendingPermission),
    }
}

// 9 个方法,全部跟原 *Agent 上的同名方法语义一致:
//
// SetGrantedReads(paths []string)        - 委托给底层 tools registry
// ApprovePath(path string)               - 委托给底层 tools registry
// EvaluatePermission(tool, args)         - 委托给底层 tools registry
// RequestPermission(ctx, req)            - 发 PermissionRequestEvent,等结果
// ResolvePermission(reqID, result)       - 解锁 pending
// HasPermissionRule(tool, level, reason) bool
// AddPermissionRule(tool, level, reason)
// deliverPermission(id, ch, result)      - 私有,timeout/cancel 触发
//
// (g *Gate) 持有 tools GrantedReadsProvider,SetGrantedReads/ApprovePath/EvaluatePermission
// 转发给它。Wire-up 时 Gate 接收一个 tools.ToolRegistry 引用。
```

### 3.2 `internal/agents/msgid/bridge.go` (新子包)

```go
// Package msgid implements the in-flight turn identity bridge between
// the loop (agentloop.Loop) and the agent runtime (executor / dispatcher).
// 拆出理由: 6 个 method + 3 个 src func,纯转发逻辑,可独立测试。
package msgid

import "sync"

// Bridge holds the per-turn ids the agent runtime needs to stamp on
// every emitted event. Wired by main.go via Attach* methods.
type Bridge struct {
    mu            sync.RWMutex
    msgIDSrc      func() string
    runIDSrc      func() string
    userMsgIDSrc  func() string
}

func NewBridge() *Bridge { return &Bridge{} }

func (b *Bridge) AttachMessageID(src func() string)    { b.mu.Lock(); b.msgIDSrc = src; b.mu.Unlock() }
func (b *Bridge) AttachRunID(src func() string)        { b.mu.Lock(); b.runIDSrc = src; b.mu.Unlock() }
func (b *Bridge) AttachUserMessageID(src func() string) { b.mu.Lock(); b.userMsgIDSrc = src; b.mu.Unlock() }

func (b *Bridge) CurrentMessageID() string {
    b.mu.RLock(); defer b.mu.RUnlock()
    if b.msgIDSrc == nil { return "" }
    return b.msgIDSrc()
}

func (b *Bridge) CurrentRunID() string {
    b.mu.RLock(); defer b.mu.RUnlock()
    if b.runIDSrc == nil { return "" }
    return b.runIDSrc()
}

func (b *Bridge) CurrentUserMessageID() string {
    b.mu.RLock(); defer b.mu.RUnlock()
    if b.userMsgIDSrc == nil { return "" }
    return b.userMsgIDSrc()
}
```

### 3.3 `internal/agents/runtime/controller.go` (新子包)

```go
// Package runtime 持有 Agent 的 run lifecycle 状态(state / cancel /
// totalTurns / totalUsage)。Run loop 直接调它,不绕过。
package runtime

type State int
const (
    Idle State = iota
    Running
)

type Controller struct {
    mu       sync.Mutex
    state    State
    cancelFn context.CancelFunc
}

func (c *Controller) TryStart() (ok bool) {
    c.mu.Lock(); defer c.mu.Unlock()
    if c.state == Running { return false }
    c.state = Running
    return true
}

func (c *Controller) End() {
    c.mu.Lock(); defer c.mu.Unlock()
    c.state = Idle
    if c.cancelFn != nil {
        c.cancelFn()
        c.cancelFn = nil
    }
}

func (c *Controller) SetCancel(cancel context.CancelFunc) {
    c.mu.Lock(); defer c.mu.Unlock()
    c.cancelFn = cancel
}

func (c *Controller) Abort() {
    c.mu.Lock(); defer c.mu.Unlock()
    if c.cancelFn != nil {
        c.cancelFn()
    }
}

func (c *Controller) IsRunning() bool {
    c.mu.Lock(); defer c.mu.Unlock()
    return c.state == Running
}
```

### 3.4 `internal/agents/usage/tracker.go` (新子包)

```go
// Package usage 持有 lastUsage(最近一次 LLM API 报告的 token 用量)。
// Mutex 保护 read/write 并发,Read 路径上调用频率高,Write 仅 turn 收尾时。
package usage

import "sync"
import "darvin-cowork/backend/internal/agents/protocol"

type Tracker struct {
    mu      sync.RWMutex
    last    protocol.Usage
}

func NewTracker() *Tracker { return &Tracker{} }

func (t *Tracker) Record(u protocol.Usage) {
    t.mu.Lock(); t.last = u; t.mu.Unlock()
}

func (t *Tracker) Last() protocol.Usage {
    t.mu.RLock(); defer t.mu.RUnlock()
    return t.last
}
```

### 3.5 `internal/agents/agent.go` 重构后(目标 ~300 行)

```go
package agent

import (
    "darvin-cowork/backend/internal/agents/msgid"
    "darvin-cowork/backend/internal/agents/perm"
    "darvin-cowork/backend/internal/agents/runtime"
    "darvin-cowork/backend/internal/agents/usage"
    // ... 其它已有 import
)

// Agent 是 harness 看到的 runtime facade。绝大部分状态已迁到子模块,
// 这里只做组装 + 转发。
type Agent struct {
    name         string
    instructions string
    model        ModelRef
    provider     protocol.ModelProvider
    session      *session.Session
    store        store.SessionStore
    msgStore     store.MessageStore
    logger       *zap.Logger
    cfg          Config
    tools        protocol.ToolRegistry
    exec         executor.Executor
    bus          *event.Bus
    queue        *queue.Queue

    // 5 个子模块
    controller   *runtime.Controller
    perm         *perm.Gate
    msgidBridge  *msgid.Bridge
    usage        *usage.Tracker

    // transient(run-scoped,Run 入口 set / 出口 clear)
    runImportedNote string
    runSkillPrompt  string
    runSkillTools   protocol.ToolRegistry

    // ctx engine handle(本身已轻,不再拆)
    assembler         ctxengine.ContextEngine
    assemblerEnabled  bool
}

// Public 方法全部 thin forward,不再持有任何 mutex,逻辑全在子模块。
// 例:
func (a *Agent) SetGrantedReads(p []string) { a.tools.SetGrantedReads(p) }
func (a *Agent) ApprovePath(p string)       { a.tools.ApprovePath(p) }
func (a *Agent) EvaluatePermission(t string, args map[string]any) protocol.PermissionEval {
    return a.perm.EvaluatePermission(t, args)
}
func (a *Agent) RequestPermission(ctx context.Context, req executor.PermissionRequest) (executor.PermissionResult, error) {
    return a.perm.RequestPermission(ctx, req, a.tools)
}
func (a *Agent) ResolvePermission(id string, r executor.PermissionResult) { a.perm.ResolvePermission(id, r) }
func (a *Agent) HasPermissionRule(t, l, r string) bool { return a.perm.HasRule(t, l, r) }
func (a *Agent) AddPermissionRule(t, l, r string)       { a.perm.AddRule(t, l, r) }

func (a *Agent) AttachMessageIDSrc(src func() string)  { a.msgidBridge.AttachMessageID(src) }
func (a *Agent) AttachRunIDSrc(src func() string)      { a.msgidBridge.AttachRunID(src) }
func (a *Agent) AttachUserMessageIDSrc(src func() string) { a.msgidBridge.AttachUserMessageID(src) }
func (a *Agent) CurrentMessageID() string   { return a.msgidBridge.CurrentMessageID() }
func (a *Agent) CurrentRunID() string        { return a.msgidBridge.CurrentRunID() }
func (a *Agent) CurrentUserMessageID() string { return a.msgidBridge.CurrentUserMessageID() }

func (a *Agent) RecordUsage(u protocol.Usage) { a.usage.Record(u) }
func (a *Agent) LastUsage() protocol.Usage    { return a.usage.Last() }
func (a *Agent) IsRunning() bool              { return a.controller.IsRunning() }
```

## 4. Facade 方法(保持向后兼容)

为不破坏现有 `gateway/handlers.go` / `acp/loop.go` 等下游,以下 6 个方法以 facade 形式保留在 `*Agent`:

| 方法 | 实现 |
|---|---|
| `Session()` / `SessionHandle()` | 字段直接返回 |
| `Provider()` / `ModelName()` / `Tools()` / `Instructions()` | 字段直接返回 |
| `Config()` / `Logger()` | 字段直接返回 |
| `Emit(ev)` | 转发 `a.bus.Emit(ev)` |
| `Subscribe(buf)` | 转发 `a.bus.Subscribe(buf)` |
| `Assembler()` / `SystemSections()` / `AssemblerEnabled()` | 字段直接返回 |

**规则**:facade 方法不持锁、不做逻辑、纯转发。

## 5. 迁移步骤

### 5.1 Phase 2a:加子包,不改 Agent (1 个 commit)

```bash
mkdir internal/agents/perm internal/agents/msgid internal/agents/runtime internal/agents/usage
# 写 4 个子包 + 各自测试
go test ./internal/agents/...   # 0 FAIL
git commit -m "feat(agents): add perm/msgid/runtime/usage sub-packages (no behavior change)"
```

### 5.2 Phase 2b:Agent 切换到子包 (1 个 commit,大改)

```bash
# 修改 agent.go 字段 + 方法
# 修改 dispatcher.go 调用 (eg a.perm.RequestPermission vs a.RequestPermission)
# 修改 agent_mini_loop.go (a.perm / a.controller)
go test ./internal/agents/...   # 必须 0 FAIL,既有 13 个 test 全过
git commit -m "refactor(agents): decompose Agent into 4 sub-packages (perm/msgid/runtime/usage)

- Agent struct 字段从 30+ 减到 15
- 9 个 permission method 迁到 perm.Gate
- 6 个 messageID method 迁到 msgid.Bridge
- 5 个 lifecycle 状态迁到 runtime.Controller
- 2 个 usage method 迁到 usage.Tracker
- 公共方法保留 19 个 facade,纯转发
- 0 业务逻辑改动,0 API 破坏

Agent.go: 532 → 300 行
dispatcher.go: 427 → 380 行
agent_mini_loop.go: 51 → 35 行

Spec: specs/features/agent-harness-architecture/02-agent-refactor.md"
```

### 5.3 Phase 2c:删除 agent_mini_loop 的多余逻辑(可选)

发现 `agent_mini_loop.go` 实际是 `RunSkillSession` 的一个 51 行小函数,留在 `agent_mini_loop.go` 还是合并回 `dispatcher.go` 由 maintainer 决定。

## 6. 风险与缓解

| 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|
| 字段重命名漏改,compiler 报错一片 | 高(经验) | 中 | Phase 2b 用 `gofmt -r` 做 sed 替换:`a.permMu → a.perm.mu` |
| permission 60s timeout 状态机迁出后行为不一致 | 中 | 高 | perm.Gate 复用现有 `deliverPermission` 逻辑,**算法完全平移** |
| 现有 `agent_test.go` 模拟整 Agent,子包化后结构变化导致 break | 中 | 中 | 测试不感知子包边界,只测 public method,facade 保留保证 API 稳定 |
| `internal/agentloop/factory.go` 在 Phase 2b 后拿不到某些字段 | 低 | 中 | factory 拿的是 *Agent,通过 facade 即可 |
| Permission 60s timeout 在 cancel 之后没清理 | 低 | 中 | Phase 2a 单独测试 timeout cleanup,确保 0 泄漏 |

## 7. 测试要求

### 7.1 既有测试必须全过

```
$ go test -count=1 -short ./internal/agents/...
ok  darvin-cowork/backend/internal/agents           (现有 agent_test.go PASS)
ok  darvin-cowork/backend/internal/agents           (现有 dispatcher_test.go PASS)
ok  darvin-cowork/backend/internal/agents           (现有 agent_mini_loop_test.go PASS)
ok  darvin-cowork/backend/internal/agents/ctxengine
ok  darvin-cowork/backend/internal/agents/event
ok  darvin-cowork/backend/internal/agents/executor
ok  darvin-cowork/backend/internal/agents/queue
ok  darvin-cowork/backend/internal/agents/session
ok  darvin-cowork/backend/internal/agents/store
```

### 7.2 新增子包测试

| 子包 | 测试 |
|---|---|
| `perm` | 8 个 case: RequestPermission / ResolvePermission / TimeoutDefaultDeny / CancelCleanup / AddRule / HasRule / EvaluatePermission / ConcurrentRequests |
| `msgid` | 4 个 case: AttachAndRead / NilSrcReturnsEmpty / ConcurrentAttach / ReadWhileWrite |
| `runtime` | 4 个 case: TryStartOnce / End / Abort / IsRunning |
| `usage` | 2 个 case: RecordAndRead / ConcurrentRead |

总新增: 18 个 case,覆盖率 100%(子包逻辑纯简单)

## 8. 与其它 spec 的接口

- **01 spec**: `*Agent` 是 `Harness` 的 builtin 实现对象,本 spec 不直接 import `harness`,但确保 facade 兼容 harness 调用
- **03 spec**: Selection 评分时,需要看 `Harness.Capabilities()`,这字段由 `*Agent` 决定。本 spec 不实现
- **04 spec**: Gateway 通过 `Harness.RunAttempt` 调 `*Agent.Run`,本 spec 不动
- **05 spec**: Tool bridge 接 `*Agent.Tools()`,本 spec 不动 tools
- **06 spec**: ctx engine 接 `*Agent.Assembler()`,本 spec 不动 assembler
