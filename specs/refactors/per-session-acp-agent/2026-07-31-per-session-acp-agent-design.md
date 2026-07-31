# per-session AcpSession Agent 重构

> **范围**：把 `internal/acp.Loop` 和 `*agent.Agent` 从单例升级为 per-session 实例，让 `EventCommon.SessionID` 自然来自该 session 自己的 Agent，而不是被一个写死的 `"default"` 字符串污染。
>
> **本次前置**：`specs/features/multi-concurrent-session-runs/2026-07-31-...-design.md` 已经把 main 端 `prompt / abort / EventRouter / DarvinEvent 载荷`等前端和 IPC 边界的全部改造完成；本 spec 只补上 Go 侧 `agent / acp / gateway` 那一段未落地的"per-session AcpSession"，让它和 spec §FR-1、§FR-2 对齐。
>
> **本次不重做**：main 侧已落地的部分一律不动；IPC schema 不动；renderer store 不动；数据库 schema 不动。
>
> **对齐参考**：沿用网易 OpenClaw `packages/acp-core/src/session.ts` 的 `Map<sessionId, AcpSession>` 形态 —— 每个 AcpSession = 独立 Agent + 独立 Loop + 独立 AbortController。
>
> **仓库约定**：AGENTS.md:79-83 明确仓库尚未配置 `npm run test` / `npm run check`，CI 入口只有 `npm run lint`。本 spec 的所有验收命令只用 Go 原生命令（`go vet` / `go test` / `go build`）和 `npm run lint`。UI / Electron 行为验证走 `playwright-cli attach`（不入 CI），与 AGENTS.md:85 一致。

---

## 1. 概述

### 1.1 问题 / 背景

把上一轮 PR 1-6 跑起来后实测发现：UI 切到非默认 session 发 prompt，事件还是路由不到那个 session 的 ledger bucket。

根因不在 IPC，也不在 renderer 路由，而在一个写死的 hardcode：

| 文件 | 现状 | 影响 |
|------|------|------|
| `src/darvin-agent/cmd/app/main.go:139` | `Session: session.NewSession("default")` | 整个进程只有一个 Agent，session id 永远是 `"default"` |
| `src/darvin-agent/cmd/app/main.go:159` | `loop := acp.NewLoop(a)` | 整个进程只有一个 Loop，所有 session 共享它 |
| `src/darvin-agent/cmd/app/main.go:171-172` | `sub := a.Subscribe(64); ledger.AttachSubscription(sub)` | EventLedger 只订了"default Agent"的事件 bus |
| `src/darvin-agent/internal/agent/dispatcher.go` | `EventCommon.SessionID = a.session.ID`（恒等于 `"default"`） | 不管 renderer 订阅的是哪个 UUID，发出来的事件全是 `"default"` 那个 |
| `src/darvin-agent/internal/gateway/handlers.go:187` | `h.Loop.Submit(acp.PromptRequest{...})` 直接喂**全局**单例 Loop（不是 per-session Loop） | 即使 `SessionManager.CreateOrGet(p.SessionID)` 拿到了对的 SessionEntry，prompt 仍落到**唯一**那个 default Agent 上跑 → 跑出来的 `session.ID` 还是 `"default"` |

而 `internal/gateway/sessionmgr.go:55-60` 的 `SessionEntry.cancel` 字段本来就是给 per-session AcpSession 留的占位 —— 现在没人写。说明 PR 1-6 当时漏掉了这一段。

**结果**：spec §1.2 的目标 1「N 个 session 同时跑 N 个独立 agent run」、目标 2「切换 session 不影响后台」、目标 7「session ID 统一」全部失效，UI 看到的还是"单 agent + 多视图"。

### 1.2 目标

落地后：

1. **一个 sessionId 对应一份 AcpSession**：sessionId → {`*agent.Agent`, `*acp.Loop`, `activeRunState`, `cancel func`}，互不共享
2. **事件 SessionID 自然来自该 session 的 Agent**：`a.session.ID` 就是这个 session 的 UUID，`EventLedger.publishLocked` 直接拿到对的 key，不再依赖任何"default fallback"
3. **`handlePrompt` / `handleAbort` 按 sessionID 路由到对应 AcpSession**：LRU/TTL/stopped-window 全部按 `SessionEntry` 维度生效
4. **每个 AcpSession 自己一个 goroutine**：跨 session 真并发，同 session 串行（沿用 Loop 自己的 queue）
5. **main.go 的"default 进程单例"消失**：不再有 `session.NewSession("default")` 这种启动期写死的字符串，session 全部按需懒建
6. **现有对外契约不变**：IPC 协议、`DarvinEvent` schema、`prompt` / `abort` handler 签名、`subscribe_events` 行为 —— 全部保持上一轮 PR 1-6 的样子

### 1.3 非目标

- **不引入**per-session 模型切换（每 session 用什么 model 还是一个全局 cfg）
- **不重做**LRU/TTL/`stoppedUntilMs` 这些已经在 `SessionManager` 里实现的逻辑（沿用）
- **不改**`EventLedger` 的 fanout 语义（`bySession[sessionID]` 已经是对的）
- **不拆**Agent 构造所用的共享 deps（Provider / SQLiteStore / MessageStore / Logger / Config 仍然是进程级单例，被 factory 复用）
- **不引入**per-session 工具注册（tools 还是 Agent 共享的 `*tool.Registry`）
- **不重写**Loop 队列逻辑（steerQueue / followUpQueue / `Wake chan` 维持现状）
- **不做**SteerControl 的 per-session 化：本期 UI 不发 steer message，沿用现成的单例 `SteerControl` 不动，等 UI 真有 steer 需求时再迁（AGENTS.md:391 "不要主动 broad refactor"）
- **不在 AcpSession 退出路径上加清理副作用**：reap/evict 时只关 Loop，DB 落库在 Run 自然完成时已经做了；如果 race 导致 active run session 被 reap，DB 已有部分落库 + `Loop.Abort` 触发 LLM cancel，未完成部分以 `error` 事件落地，不试图把"reap 的 session"标记成特殊状态

---

## 2. 用户场景

### 场景 1：多 session 真并发跑

**Given** main 侧给 session A、B 各发一条 prompt
**When** Go 侧 `handlePrompt(A)` 和 `handlePrompt(B)` 在 100ms 内先后到达
**Then**
- `SessionManager.GetOrCreateEntry(A)` 返回 A 的 `SessionEntry`，A 的 AcpSession 懒建（如果之前没有）
- `SessionManager.GetOrCreateEntry(B)` 同理返回 B 的
- A、B 的 Loop 各自独立 goroutine，并发请求 LLM
- `done` / `error` 事件里 `EventCommon.SessionID` 分别是 A、B 的 UUID，分别落到对应 ledger bucket

### 场景 2：跨 session 真不串扰

**Given** session A 在跑长任务，session B 是空闲
**When** session B 发新 prompt
**Then**
- B 的 Loop 立即接收 prompt 开跑
- A 的 Loop / Agent goroutine 不被中断，A 的 in-flight turn 继续推进

### 场景 3：同 session 串行（同 Loop 内串行）

**Given** session A 上轮 prompt 还在跑（activeRun 存在）
**When** A 内再发一条 prompt
**Then**
- 新 prompt 进 A 的 Loop 的 followUpQueue
- 上轮 `done` 后 A 的 Loop 自然接续下一条
- B 不受任何影响（这是 Loop 内部行为，不是 SessionManager 的事）

### 场景 4：abort 精确停某 session 的某 run

**Given** session A、B 都在跑，A 的 runId = `rA`，B 的 runId = `rB`
**When** active=A 时 main 调 `client.abort({ sessionId: A, runId: rA })`
**Then**
- Go `handleAbort` 在 SessionManager 找到 A 的 entry
- 调 A 的 Loop 的 `Stop(rA)` —— B 的 Loop 不被碰到
- A 的 `EventLedger.Subscribe(A)` 收到 `done`/`error`，B 继续推 B 的事件

### 场景 5：LRU 驱逐一条 idle session

**Given** SessionManager 已有 5000 个 entry，其中 entry A 处于 active run
**When** 新 prompt 到达，达到 maxSessions 阈值
**Then**
- 走 LRU 驱逐：从 idleOrder 尾巴找一个 idle entry（activeRun == nil）驱逐
- 驱逐路径：调该 entry 的 `cancel()` → Loop goroutine 退出 → 从 byID 删除
- A 不被驱逐（active run 永不驱逐，沿用现成逻辑）

### 场景 6：subscribe 早于首个 prompt

**Given** renderer 启动时给 session A 发 `subscribe_events`（A 在 SessionManager 还没有 entry）
**When** Go 收到订阅请求
**Then**
- `subscribe_events` handler 调 `SessionManager.GetOrCreateEntry(A)` 懒建 A 的 SessionEntry（仅 session handle，不建 AcpSession；Loop 在首个 prompt 才建；见 FR-8 两阶段）
- A 立即可以收到后续 prompt 推过来的事件

### 场景 7：进程重启后再收到老 session 的 prompt

**Given** 进程重启，in-memory SessionManager 是空的；DB 有 session A 的历史 messages
**When** main 调 `getMessages(A)` 然后发 prompt 到 A
**Then**
- `getMessages(A)` 从 SQLite 拉历史，正常显示
- 首个 prompt 到达：`GetOrCreateEntry(A)` 建一个新 AcpSession，sessionID 与 DB 行一致即可（session 内部状态从空开始，Run 时由 dispatcher.go 重新从 store 加载历史）

---

## 3. 功能需求

### FR-1 `AcpSession` 类型与 SessionManager 关联

在 `internal/acp/` 下新增：

```go
// internal/acp/session.go （新增文件）

// AcpSession 是一个 session 在 Go 侧的全部 in-flight 状态。
// Agent / Loop 持有全部 per-session 资源；关掉 AcpSession 等于关掉该 session 的整条链。
type AcpSession struct {
    SessionID string
    Agent     *agent.Agent
    Loop      *Loop               // 自家 Loop；steerQueue/followUpQueue 都在这里
    cancel    context.CancelFunc  // 关 Loop 用；由 SessionManager 在 evict 时调
}
```

`internal/gateway/sessionmgr.go:49-60` 的 `SessionEntry` 改造：

```go
type SessionEntry struct {
    Session       *session.Session
    Acp           *acp.AcpSession  // ← 新增；首次 prompt 时懒建
    cancel        context.CancelFunc
    lastTouchedMs int64
    stoppedUntilMs int64
    idleElem      *list.Element
}
```

**取消原来的 `activeRun *activeRunState` 镜像字段**：source-of-truth 只能有一个。让 Loop 自己持 `activeRunState`（已有），SessionManager 通过新加的 `Loop.ActiveRunID() string` 只读 getter 判断"是否有 in-flight turn"，用于 LRU 驱逐判据。两套并存会有 race（Loop 设 activeRun → 还没镜像 → SessionManager 已驱逐）。

注意：`SessionManager` 现有的 LRU / TTL / stoppedUntilMs / maxSessions 逻辑完全不动 —— 只多挂一个 `Acp` 字段、移除 `activeRun` 字段、加一个 `Loop.ActiveRunID()` 调用点。

`internal/acp/loop.go` 新增：

```go
// ActiveRunID returns the runID of the in-flight turn, or "" when idle.
// SessionManager.evictLocked reads this to skip entries that are
// actively running — never evict an in-flight turn.
//
// 与 CurrentRunID 的区别：CurrentRunID 在 idle 时仍返回"上一次跑的 run id"
// （用于 executor 给下一次 turn 的事件也带上 run id）；ActiveRunID 严格
// 反映"此刻是不是有 turn 在跑"。
func (l *Loop) ActiveRunID() string {
    l.mu.Lock()
    defer l.mu.Unlock()
    if l.activeRun == nil {
        return ""
    }
    return l.activeRun.runID
}
```

### FR-2 AcpSession 工厂

把现在散在 `main.go:134-162` 的 Agent + Loop 构造抽成可复用的工厂。**Provider / SQLiteStore / SQLiteMessageStore / Logger / Config / 工具 Registry / ctxengine** 都是进程级共享，由 main.go 一次性建好，注入到一个 factory。

```go
// internal/acp/factory.go （新增文件）

// AgentFactory 持有构造一个 *agent.Agent 所需的全部共享 deps。
// main.go 在启动期构造一次，注入到 SessionManager。
type AgentFactory struct {
    Name         string
    Instructions string
    Model        agent.ModelRef
    Provider     llm.ModelProvider
    Store        store.SessionStore
    MessageStore store.MessageStore
    Logger       *zap.Logger
    Config       agent.Config           // 内含 Config.AssemblerEnabled —— 传给 DefaultAssembler 构造
    Tools        *tool.Registry
    Assembler    ctxengine.ContextEngine
    // AssemblerEnabled 是 post-construction 开关（agent.NewAgentConfig.AssemblerEnabled
    // 字段，对应 cfg.yaml `assembler_enabled: true`）。与 Config.AssemblerEnabled
    // 语义不同：前者决定 DefaultAssembler 构造时拿什么参数，后者决定 Agent 是否走
    // assembler pipeline。两者都从 cfg 传入，factory 不合并，让 caller 控制。
    AssemblerEnabled bool
}

// Build 用 sessionID 构造一个 *agent.Agent，并把它的 session 字段指向
// session.NewSession(sessionID)。
func (f *AgentFactory) Build(sessionID string) (*agent.Agent, error) {
    return agent.New(agent.NewAgentConfig{
        Name:             f.Name,
        Instructions:     f.Instructions,
        Model:            f.Model,
        Provider:         f.Provider,
        Session:          session.NewSession(sessionID),  // ← 关键：per-session session
        Store:            f.Store,
        MessageStore:     f.MessageStore,
        Logger:           f.Logger,
        Config:           f.Config,
        Tools:            f.Tools,
        Assembler:        f.Assembler,
        AssemblerEnabled: f.AssemblerEnabled,
    })
}

// NewAcpSession 用 AgentFactory + sessionID 一次性建出 Agent + Loop，
// 并把 Loop.CurrentMessageID / CurrentRunID 挂到 Agent 的 AttachMessageIDSrc
// / AttachRunIDSrc（main.go 现在的两行搬迁过来）。
func (f *AgentFactory) NewAcpSession(sessionID string) (*AcpSession, error) {
    a, err := f.Build(sessionID)
    if err != nil {
        return nil, err
    }
    l := NewLoop(a)
    a.AttachMessageIDSrc(l.CurrentMessageID)
    a.AttachRunIDSrc(l.CurrentRunID)
    return &AcpSession{
        SessionID: sessionID,
        Agent:     a,
        Loop:      l,
    }, nil
}
```

> **契约**：factory.Build 必须填 `NewAgentConfig.Session: session.NewSession(sessionID)`。`Agent.session.ID` 后续被 dispatcher.go 用来填 `EventCommon.SessionID`，这是事件正确路由到 EventLedger 的唯一入口。`§6 涉及文件` 强调"agent/agent.go 接口不变"指 `agent.New` 函数签名不变；调用方负责填对 Session。

`SessionManager` 加构造选项：

```go
// internal/gateway/sessionmgr.go

type SessionManagerOption func(*SessionManager)

func WithAgentFactory(f *acp.AgentFactory) SessionManagerOption {
    return func(m *SessionManager) { m.factory = f }
}

func WithEventLedger(l *EventLedger) SessionManagerOption {
    return func(m *SessionManager) { m.ledger = l }
}

// NewSessionManager 现在接受可变参数 options。
// 不带 options 时仍保持现状（LRU/TTL/stoppedUntilMs 用默认值）。
// 预置 "default" session 这件事**移出 NewSessionManager**：要么由 caller 显式
// 调 GetOrCreateEntry(DefaultSessionID)，要么完全不预置 —— 看调用方需要。
// 本期 main.go 不再需要 warm-up（见 FR-7 直接构造 factory，不再预置 default），直接不预置。
func NewSessionManager(opts ...SessionManagerOption) *SessionManager { ... }
```

### FR-3 SessionManager 懒建路径

在 `GetOrCreateEntry` 命中"未知 id"分支里加（仅在 prompt 路径触发；subscribe 走 FR-9 的两阶段策略，**不**走这里）：

```go
// internal/gateway/sessionmgr.go  GetOrCreateEntry 内，未知 id 分支末尾：

// 仅 factory 不为 nil 时才允许 AcpSession 懒建；handler 测试可不带 factory。
if m.factory != nil && e.Acp == nil {
    acpSess, err := m.factory.NewAcpSession(id)
    if err != nil {
        // 构造失败不能让 entry 半残留在 byID。下次再调 GetOrCreateEntry(id)
        // 会走"命中现有 entry"分支 → 永远不会再试 factory → session 永久残废。
        // 立刻回滚：删 byID entry + 移 LRU；返回 error 让 caller 决定要不要重试。
        delete(m.byID, id)
        if e.idleElem != nil {
            m.idleOrder.Remove(e.idleElem)
        }
        return nil, err
    }
    e.Acp = acpSess
    ctx, cancel := context.WithCancel(context.Background())
    e.cancel = cancel
    go func() {
        <-ctx.Done()
        // Close 阻塞（<-l.done），放后台 goroutine 跑
        acpSess.Loop.Close()
    }()
    // EventLedger 订该 Agent 的事件 bus —— 沿用 main.go:171-172 模式
    if m.ledger != nil {
        sub := acpSess.Agent.Subscribe(64)
        m.ledger.AttachSubscription(sub)
    }
}
```

**不要在 `NewSessionManager()` 里调 `GetOrCreateEntry(DefaultSessionID)`**：factory 还没注入，懒建路径走不通。DefaultSessionID 常量保留作为"兼容/迁移期间"使用的特殊 id（renderer 可能订阅它），但本期不预置；`handlePrompt` / `handleSubscribeEvents` 收到 default id 时仍按正常路径处理（见 FR-7）。

### FR-4 handlers.go 路由到 AcpSession

`internal/gateway/handlers.go` 的 `handlePrompt` / `handleAbort` 改造：

```go
// handlePrompt
entry, err := c.sessions.GetOrCreateEntry(p.SessionID)
if err != nil {
    if errors.Is(err, ErrSessionStalled) {
        return errorResp(id, CodeSessionStalled, "session stalled", err)
    }
    return errorResp(id, CodeInternalError, "get session", err)
}
ticket, err := entry.Acp.Loop.Submit(acp.PromptRequest{
    RunID:   p.RunID,
    Content: p.Content,
})
// 返回 SubmitResult{RunID: ticket.RunID, MessageID: ticket.MessageID, Queued: ticket.Queued}
```

```go
// handleAbort
if !c.sessions.Stop(p.SessionID, p.RunID) {
    return errorResp(id, CodeRunMismatch, "abort run mismatch", nil)
}
return successResp(id, AbortResult{Aborted: true})
```

`SessionManager.Stop` 保持上一轮 PR 2 的形状不动；它内部已经能调到对应 entry 的 `Loop.Stop`（见 FR-5）。

### FR-5 SessionManager.Stop / evict 改调 Loop

```go
// internal/gateway/sessionmgr.go  Stop 路径：

func (m *SessionManager) Stop(sessionID, runId string) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    e, ok := m.byID[sessionID]
    if !ok { return ErrSessionNotFound }
    if e.Acp == nil {
        // 还没建 AcpSession（subscribe 早于 prompt），没有 active run 可停。
        return ErrRunMismatch
    }
    if !e.Acp.Loop.Stop(runId) {
        return ErrRunMismatch
    }
    e.stoppedUntilMs = m.nowMs() + m.stopWindow.Milliseconds()
    return nil
}
```

`evictLocked` 改造：

```go
func (m *SessionManager) evictLocked(id string) {
    e, ok := m.byID[id]
    if !ok { return }
    // source-of-truth 是 Loop.activeRun，通过 ActiveRunID() 只读判断
    if e.Acp != nil && e.Acp.Loop.ActiveRunID() != "" {
        return  // 兜底防御：in-flight 的 session 不驱逐
    }
    if e.Acp != nil {
        e.Acp.Loop.Abort(context.Background())  // 清 steerQueue / followUpQueue + cancel in-flight turn
    }
    if e.cancel != nil {
        e.cancel()  // 触发 FR-3 后台 goroutine 调 Loop.Close() 退出
        e.cancel = nil
    }
    delete(m.byID, id)
    if e.idleElem != nil {
        m.idleOrder.Remove(e.idleElem)
        e.idleElem = nil
    }
}
```

### FR-6 EventLedger 多个订阅源

`EventLedger` 的 fanout 逻辑（`bySession[sessionID]`）已经能正确处理多个订阅源 —— 任何 `AttachSubscription` 进来的事件都按 `EventCommon.SessionID` 分桶。新加的懒建路径只需在每个新 AcpSession 建好后调一次 `AttachSubscription`（FR-3 已包含）。

无需改 `EventLedger` 本体。

### FR-7 main.go 收尾

`src/darvin-agent/cmd/app/main.go` 删掉写死的 `"default"` 单例，改为构造 factory 注入 SessionManager：

```go
// src/darvin-agent/cmd/app/main.go

// 1. 构造共享 deps（沿用现状）
provider, _ := llm.NewProvider(...)
sqliteStore := store.NewSQLiteStore(database.Get())
msgStore := store.NewSQLiteMessageStore(database.Get())

// 2. 构造 factory（一次性）
factory := &acp.AgentFactory{
    Name:             cfg.App.Name + "-agent",
    Instructions:     cfg.Agent.Instructions,
    Model:            agent.ModelRef{Provider: cfg.Agent.ProviderName, Model: cfg.Agent.Model},
    Provider:         provider,
    Store:            sqliteStore,
    MessageStore:     msgStore,
    Logger:           log.Logger,
    Config:           agentCfg,
    Assembler:        nil,                  // 用 default assembler（agent.New 内部自动建）
    AssemblerEnabled: cfg.Agent.AssemblerEnabled,
}

// 3. 构造 sessions + ledger + handler
sessions := gateway.NewSessionManager(
    gateway.WithAgentFactory(factory),
)
ledger := gateway.NewEventLedger(log.Logger)
handler := gateway.NewHandler(sessions, ledger, sqliteStore, msgStore)
gs := gateway.NewServer(handler, log.Logger)
gs.Start(rootCtx)

// 4. 不再 warm-up "default"。DefaultSessionID 仍保留为常量（兼容历史 DB 行 /
//    老订阅），但本期 AcpSession 只在首个 prompt 到 default id 时才懒建。
```

`agent.New(...)` / `acp.NewLoop(a)` / `a.AttachMessageIDSrc(loop.CurrentMessageID)` / `a.AttachRunIDSrc(loop.CurrentRunID)` / `a.Subscribe(64)` + `ledger.AttachSubscription(sub)` —— 这五处（main.go:134-145, 159-161, 171-172）整体迁到 `AgentFactory.NewAcpSession` 内部（FR-2）。

`acp.NewSteerControl(a)` 不动 —— 继续接单例 Agent（AGENTS.md:391 "不要主动 broad refactor"）。

### FR-8 subscribe 路径两阶段

`subscribe_events` 拆成两阶段，避免 subscribe 触发 AcpSession 占用：

**阶段 1：subscribe 进 handler**
- 只确保 SessionManager 知道这个 id（建 SessionEntry，但**不**调 factory.NewAcpSession）
- `c.ledger.Subscribe(p.SessionID, c)` 立即生效，订阅成功
- 该 session 暂时没有事件源（没有 AcpSession → 没有 Agent → 没有事件 bus），但订阅**本身**成功

**阶段 2：首个 prompt 到该 id**
- `handlePrompt` 调 `GetOrCreateEntry(sessionID)` 命中已有 entry
- 走 FR-3 的懒建路径补出 AcpSession + Agent + Loop + 事件订阅
- 此后 subscribe 的连接才能收到事件

```go
// internal/gateway/handlers.go  handleSubscribeEvents
if _, _, err := c.sessions.CreateOrGet(p.SessionID); err != nil {
    return errorResp(id, CodeInternalError, "subscribe session create", err)
}
c.ledger.Subscribe(p.SessionID, c)
return successResp(id, SubscribeEventsResult{Subscribed: true})
```

注意：`CreateOrGet` 当前总是返回成功（无 error 路径），handler 这层不需要分支。

**资源代价上限**：subscribe 只创建轻量 SessionEntry（`session.NewSession(id)` + LRU 节点），不创建 AcpSession。即使 renderer 对每个历史 session 都 subscribe，最多占 5000 个 SessionEntry（`maxSessions` 上限），与现状持平 —— 没有引入新资源类型。

### FR-9 兼容性

- **IPC 协议**：prompt / abort / subscribe_events / ping 的 request/response 形状**主体不动**
- **`DarvinEvent` schema**：sessionId / runId 字段已存在上一轮 PR 1，无需改
- **`prompt` handler 返回扩展**：在 `PromptResult` 加 `Queued bool`（`json:"queued,omitempty"`），来自 `Loop.Submit` 的 `RunTicket.Queued`，前端可显示"已排队，下一条将在上一条完成后开始"。`handlers.go:26-30` 的 PromptResult 改成：

  ```go
  type PromptResult struct {
      SessionID string `json:"sessionId"`
      RunID     string `json:"runId"`
      MessageID string `json:"messageId"`
      Queued    bool   `json:"queued,omitempty"`
  }
  ```

  `src/shared/darvin-api.ts` 的 `DarvinPromptResponse` 同步加 `queued?: boolean`：

  ```ts
  export interface DarvinPromptResponse {
      sessionId: string;
      messageId: string;
      runId: string;
      queued?: boolean;  // ← 新增；false 表示立刻起跑，true 表示入 followUpQueue
  }
  ```

- **`abort` handler 返回扩展**：保持 `Aborted bool` + `SessionID`，不动
- **DB schema**：不动；Agent 启动期从 `sqliteStore` 加载 messages，per-session 重建（dispatcher.go 已有该路径，懒建 AcpSession 后 `Agent.Run` 自动从 store 拿历史）
- **历史 session 行**：启动后无需做迁移 —— 首次 prompt 到该 id 时 SessionManager 懒建

---

## 4. 实现方案

### 4.1 `internal/acp/factory.go`（新增）

如 FR-2 草图。包含 `AgentFactory` 结构 + `Build(sessionID)` + `NewAcpSession(sessionID)` + `(*AcpSession).Close()`（可选，封装 cancel）。

### 4.2 `internal/acp/session.go`（新增）

`AcpSession` 类型（FR-1 草图）。本期不挂 Steer（§1.3 非目标）。

### 4.3 `internal/gateway/sessionmgr.go`（修改）

- 加 `factory *acp.AgentFactory` 和 `ledger *EventLedger` 字段
- 加 `WithAgentFactory` / `WithEventLedger` / `WithDefaultSession` options
- `NewSessionManager` 改为变参；保留 default session 注册语义（如果有 `WithDefaultSession` 就注册，没有就不注册 —— 默认行为退化为"不预置 default"，但保留 `DefaultSessionID` 常量供 main.go 显式调一次）
- `GetOrCreateEntry` 未知 id 分支末尾追加"懒建 AcpSession"（FR-3）
- `Stop` / `evictLocked` 改走 entry.Acp.Loop（FR-5）

### 4.4 `internal/gateway/handlers.go`（修改）

- `Handler` 构造签名：`NewHandler(sessions, ledger, store, msgStore)` —— **移除** `loop *acp.Loop` 形参（不再走全局 Loop）；**保留** `steer acp.SteerControl` 形参（Steer 仍接单例 Agent，§1.3 非目标）
- `handlePrompt` 走 `entry := sessions.GetOrCreateEntry(p.SessionID); entry.Acp.Loop.Submit(...)`
- `handleAbort` 走 `sessions.Stop(p.SessionID, p.RunID)`
- `handleSubscribeEvents` 走 `sessions.GetOrCreateEntry(p.SessionID)` 触发 SessionEntry 懒建（**不**建 AcpSession，见 FR-8）
- `handleSteer` 不变（仍走 `h.Steer`）

### 4.5 `internal/gateway/server.go`（如有需要）

- WS 连接 / `attachSubscription` 路径不动 —— EventLedger 内部已经按 SessionID 分桶
- 不需要移除 Steer 形参

### 4.6 `cmd/app/main.go`（修改）

如 FR-7 草图。净效果：

- 删除 `a, err := agent.New(...)` 大块（约 25 行）
- 删除 `loop := acp.NewLoop(a); a.AttachMessageIDSrc(...); a.AttachRunIDSrc(...);`（3 行；**保留** `steer := acp.NewSteerControl(a)`）
- 删除 `sub := a.Subscribe(64); ledger.AttachSubscription(sub)`（2 行；改为 lazy：在 `SessionManager.GetOrCreateEntry` 懒建路径里订阅每个新 AcpSession，FR-3）
- 删除 `if err := a.Abort(...); ...` 关闭路径（约 3 行；per-session AcpSession 自己 Close）
- 增加 `factory := &acp.AgentFactory{...}` + `sessions := gateway.NewSessionManager(gateway.WithAgentFactory(factory))`（约 25 行）
- **不再**有"暖通道"（DefaultSessionID 不预置）

净变化：约 +20 行、-40 行。

### 4.7 测试

新增单测覆盖以下：

| 测试 | 覆盖 |
|------|------|
| `gateway/sessionmgr_test.go::TestSessionManager_LazyBuildPerSession` | 未知 id 触发 AcpSession 懒建；不同 id 各建一份 |
| `gateway/sessionmgr_test.go::TestSessionManager_StopGoesToPerSessionLoop` | 同 runId 调 A 的 Stop 不影响 B 的 Loop |
| `gateway/sessionmgr_test.go::TestSessionManager_EvictClosesAcpSession` | LRU 驱逐调 `cancel` → Loop goroutine 退出 |
| `gateway/handlers_test.go::TestHandlePrompt_RoutesBySessionID` | prompt 到 A 落到 A 的 Loop；B 不被碰到 |
| `gateway/handlers_test.go::TestHandleAbort_RoutesBySessionIDAndRunID` | abort 到 A 不影响 B |
| `gateway/handlers_test.go::TestHandleSubscribeEvents_LazyBuildsAcp` | subscribe 触发懒建（沿用上一轮 PR 的 `TestDispatchSubscribeEventsCreatesUnknownSession`，断言 `entry.Acp != nil`） |
| `acp/factory_test.go::TestFactory_BuildAttachesLoopAndSources` | factory 构造的 AcpSession.AttachMessageIDSrc / AttachRunIDSrc 已挂上 |
| `acp/factory_test.go::TestFactory_DifferentSessionIDsDifferentAgents` | 不同 sessionID → 不同 `*agent.Agent` 实例，`a.Session().ID` == sessionID |

---

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| **renderer 创建新 session 但还没 subscribe 就发 prompt** | `GetOrCreateEntry` 命中懒建路径（FR-3），prompt 直接落到 entry 上；该 session 的事件被 EventLedger 持有，但没订阅者就 fanout 到空（不报错，等订阅者连上后看不到历史 —— 这是已知限制，沿用上一轮 spec §5） |
| **renderer 对一个没建过的 session 发 subscribe 但再不发 prompt** | subscribe 只建 SessionEntry（轻量，`session.NewSession(id)` + LRU 节点），不建 AcpSession；该 session 永久 idle 也会被 TTL reap；不会浪费 Agent / Provider / Loop goroutine |
| **renderer 发 prompt 但同 session 还在跑** | `Loop.Submit` 内部看 `activeRun != nil` 自动入 `followUpQueue`；返回 `RunTicket{Queued: true}`；handler 把 `queued` 字段透到 IPC 响应（FR-9） |
| **同 session abort 后 1s 内又发 prompt** | `SessionEntry.stoppedUntilMs` 阻止，`GetOrCreateEntry` 返回 `ErrSessionStalled` → IPC 返 `code: 'session-stalled'`（沿用上一轮） |
| **LRU 驱逐 active run** | 跳过；evictLocked 改读 `entry.Acp.Loop.ActiveRunID() == ""` 判断；与上一轮 spec §FR-1 一致 |
| **进程重启后内存 entry 丢失** | 首个 prompt 触发懒建（FR-3）；Agent 启动时由 dispatcher.go 从 sqliteStore 加载历史 messages |
| **不同 session 共享 Provider** | Provider / Store / MessageStore / Logger / Config / Tools 都是 factory 里的指针，多个 Agent 共享同一份；DB 连接受 SQLite 自身池化，Go 侧不重复开 |
| **subscribe 触发大量 AcpSession 占用资源** | FR-8 拆两阶段：subscribe 只建 SessionEntry（不建 AcpSession）；资源代价上限与现状持平（`maxSessions=5000` + LRU），没有引入新资源类型 |
| **Agent 构造失败（如 Provider 临时不可用）** | `GetOrCreateEntry` 返回 error；**FR-3 err 分支前 delete entry + 移 LRU**，不让半残 entry 留在 byID；IPC 返 `code: 'agent-init-failed'`（新增）；下次重试会再走懒建路径 |
| **handler 测试 stub** | `NewHandler` 仍接受 `*SessionManager`；handler 测试可以注入不带 factory 的 SessionManager（用 `NewSessionManager()` 不带 options）—— 此时 GetOrCreateEntry 走"只建 session 不建 Agent"路径；handlePrompt 命中 nil entry.Acp → 返 `code: 'no-acp-session'`（避免 panic） |
| **active run session 被 reap 的 race** | 理论上 LRU 不应驱逐 active，但 evictLocked 用 `entry.Acp.Loop.ActiveRunID() == ""` 判据；如果在判断和 evict 中间有 turn 完成（activeRun 设为 nil），evict 仍然继续 —— 此时 `Loop.Abort` 是 no-op（run 已完成）；DB 落库在 Run 自然完成时已经做了，无副作用 |
| **`Loop.Close()` 在 evict 路径上阻塞** | Close 等 `<-l.done`（loop.go:198）会阻塞；evictLocked 不直接调 Close，改在 `go func() { <-ctx.Done(); acpSess.Loop.Close() }()` 后台 goroutine 调（FR-3 已写） |
| **Steer 调用发到没有 AcpSession 的 entry** | 本期不迁 SteerControl（§1.3 非目标），Steer 仍走全局单例 Agent；功能上与现状一致 |

---

## 6. 涉及文件

### 新增

| 文件 | 说明 |
|------|------|
| `src/darvin-agent/internal/acp/session.go` | `AcpSession` 类型 + `Close()` |
| `src/darvin-agent/internal/acp/factory.go` | `AgentFactory` + `Build` + `NewAcpSession` |
| `src/darvin-agent/internal/acp/factory_test.go` | factory 单测 |
| `src/darvin-agent/internal/acp/session_test.go`（可选） | `AcpSession` 收尾测试 |

### 修改

| 文件 | 变更 |
|------|------|
| `src/darvin-agent/internal/gateway/sessionmgr.go` | 加 `factory` / `ledger` 字段；`WithAgentFactory` / `WithEventLedger` options；`NewSessionManager` 变参；`GetOrCreateEntry` 懒建路径 + 失败回滚；`Stop` / `evictLocked` 走 `entry.Acp.Loop`；**删 `activeRun` 字段** |
| `src/darvin-agent/internal/acp/loop.go` | 加 `ActiveRunID() string` 只读 getter（FR-1） |
| `src/darvin-agent/internal/gateway/sessionmgr_test.go` | 新增 lazy / per-session 路由 / evict / 失败回滚 四组用例 |
| `src/darvin-agent/internal/gateway/handlers.go` | `PromptResult` 加 `Queued` 字段（FR-9）；`handlePrompt` / `handleAbort` 改走 `entry.Acp`；`handleSubscribeEvents` 维持现状（FR-8 两阶段） |
| `src/darvin-agent/internal/gateway/handlers_test.go` | 新增 `TestHandlePrompt_RoutesBySessionID` / `TestHandleAbort_RoutesBySessionIDAndRunID` / `TestHandlePrompt_QueuedForActiveSession` |
| `src/shared/darvin-api.ts` | `DarvinPromptResponse` 加 `queued?: boolean` |
| `src/darvin-agent/cmd/app/main.go` | 删单例 + 构造 factory 注入；删 `a.Abort(...)` 收尾（per-session AcpSession 不需要全局 Abort） |

### 不动的文件

- `src/darvin-agent/internal/agent/agent.go`：`Agent.New` 函数签名不变；调用方（factory.Build）必须填对 `NewAgentConfig.Session: session.NewSession(sessionID)`
- `src/darvin-agent/internal/acp/steer.go`：`NewSteerControl` 仍接单例 `*agent.Agent`，本期不迁
- `src/darvin-agent/internal/gateway/eventledger.go`：fanout 不变
- 数据库 schema、IPC 协议（除 `PromptResult.Queued`）、`DarvinEvent`、renderer store、main 端 IPC handler、preload

---

## 7. 验收标准

### 7.1 自动化 / 静态检查

- [ ] `cd src/darvin-agent && go vet ./...` 通过
- [ ] `cd src/darvin-agent && go build ./...` 通过
- [ ] `cd src/darvin-agent && go test ./...` 全包通过
- [ ] `npm run lint` 通过（AGENTS.md:82 CI 入口）
- [ ] `npm run build:agent` 在本机平台产出 `bin/darvin-agent-<platform>-<arch>`

### 7.2 新增单测（FR 对应）

- [ ] `acp/factory_test.go::TestFactory_BuildAttachesLoopAndSources`
- [ ] `acp/factory_test.go::TestFactory_DifferentSessionIDsDifferentAgents`
- [ ] `acp/loop_test.go::TestLoop_ActiveRunID` —— 空闲时返回 ""，in-flight 返回 runID，defer 后回到 ""
- [ ] `gateway/sessionmgr_test.go::TestSessionManager_LazyBuildPerSession`
- [ ] `gateway/sessionmgr_test.go::TestSessionManager_LazyBuildFailureRollsBack` —— factory 返回 error 后 byID + LRU 无残留
- [ ] `gateway/sessionmgr_test.go::TestSessionManager_StopGoesToPerSessionLoop`
- [ ] `gateway/sessionmgr_test.go::TestSessionManager_EvictClosesAcpSession` —— 验证 ActiveRunID() 判据
- [ ] `gateway/handlers_test.go::TestHandlePrompt_RoutesBySessionID`
- [ ] `gateway/handlers_test.go::TestHandleAbort_RoutesBySessionIDAndRunID`
- [ ] `gateway/handlers_test.go::TestHandlePrompt_QueuedForActiveSession` —— 断言返回 `Queued: true`
- [ ] `gateway/handlers_test.go::TestHandleSubscribeEvents_BuildsEntryNotAcp` —— 断言 entry.Acp == nil，subscribe 仍成功

### 7.3 手工验证（playwright-cli attach 到 Electron DevTools）

接上一轮 PR 1-6 已经做过的实测场景，重跑一遍：

- [ ] **场景 1**：A 发长 prompt，DevTools console 看 A 的 `text_delta` 事件 `EventCommon.SessionID == A`（不再是 `"default"`）
- [ ] **场景 2**：A、B 各发短 prompt；WS 帧并发
- [ ] **场景 3**：A 已发一轮在跑，再发一条；DevTools console 看"第二条 prompt 的 `text_delta` 事件 `SessionID == A` 且在前一条 `done` 之后"；IPC 返回 `queued: true`
- [ ] **场景 4**：active=A 时点"停止生成"；A 收 `done`/`error`，B 不动
- [ ] **场景 5**：A 后台跑完，窗口失焦，系统通知弹出
- [ ] **场景 6**：DevTools console 调 `window.darvin.listSessions()` 拿当前 session 列表（走 `agent.list_sessions` IPC）；active 切到 B 后再 list，A 仍在列表里
- [ ] **场景 7**：用 `window.darvin.subscribeEvents(sid, cb)` 对一个**新建未用**的 session 订阅 → 立即成功；但不发 prompt 时 callback 不会触发；首个 prompt 到该 session 后 callback 开始收事件（验证 FR-8 两阶段）

### 7.4 兼容性回归

- [ ] DB schema 不变
- [ ] 旧 session 行启动后 `listSessions` 正常
- [ ] 历史 messages 切回不丢（dispatcher.go 通过 sqliteStore 加载，路径不变）
- [ ] preload API 形状不变（renderer 调用点零改动除可选 `queued`）
- [ ] `DarvinEvent` schema 不变
- [ ] IPC `prompt` / `abort` / `subscribe_events` / `list_sessions` / `get_messages` / `steer` 协议**主体**不变；`PromptResult` 加可选 `queued`

### 7.5 非目标确认

- [ ] 不引入 per-session 模型切换
- [ ] 不重写 Loop 队列逻辑
- [ ] 不改 EventLedger fanout
- [ ] 不引入新的外部持久化（数据库 schema 不变）
- [ ] 不迁 SteerControl 到 per-session（AGENTS.md:391 不要 broad refactor）

---

## 附录 A：与上一轮 spec 的关系

上一轮 `2026-07-31-multi-concurrent-session-runs-design.md` 的 §FR-1 已经定义了"per-session 状态 + LRU 驱逐 + TTL"、`§FR-2` 已经定义了"AcpSession / Loop 改造"。

PR 1-6 落地后：

- §FR-1（SessionManager LRU/TTL/stoppedUntilMs）：**已落地**
- §FR-2（per-session AcpSession / Loop）：**未落地** —— 这是本 spec 的唯一目标
- §FR-3 / §FR-4 / §FR-5 / §FR-6 / §FR-7 / §FR-8 / §FR-9 / §FR-10：上一轮 PR 已落地，不动

## 附录 B：实施拆分（建议 PR 顺序）

为方便 review、便于回滚、便于分批跑 playwright-cli 实测，拆 3 个 PR：

1. **PR 1：`AcpSession` + `AgentFactory` 骨架 + `Loop.ActiveRunID()`**
   - 新增 `internal/acp/session.go`、`internal/acp/factory.go`
   - `internal/acp/loop.go` 加 `ActiveRunID() string` getter
   - `factory_test.go` / `loop_test.go` 覆盖 Build / NewAcpSession / 不同 sessionID 不同实例 / ActiveRunID 状态转换
   - **不改**任何现有文件；纯新增

2. **PR 2：SessionManager 接入 factory**
   - `sessionmgr.go` 加 `factory` / `ledger` 字段 + `WithAgentFactory` / `WithEventLedger` options
   - **删 `SessionEntry.activeRun` 字段**；`Stop` / `evictLocked` 改读 `entry.Acp.Loop.ActiveRunID()`
   - `GetOrCreateEntry` 未知 id 分支末尾追加懒建路径（含失败回滚）
   - `acp/loop.go` 现有 `Stop` 调用方（SessionManager.Stop）路径仍兼容 nil Acp
   - 新增 `sessionmgr_test.go` 四组用例
   - **不迁** handlers.go（这步 PR 2 完成后所有 handle* 函数仍走全局 Loop；facto­ry 注入但 handler 没切；无运行时行为变化，但为 PR 3 准备好数据）

3. **PR 3：handlers.go 路由迁移 + main.go 收尾 + IPC 扩展**
   - `handlers.go` 的 `handlePrompt` / `handleAbort` 改走 `entry.Acp`（`handleSubscribeEvents` 维持 FR-8 两阶段）
   - `PromptResult` 加 `Queued` 字段；`src/shared/darvin-api.ts` 同步加 `DarvinPromptResponse.queued?: boolean`
   - `main.go` 删单例 + 构造 factory 注入；删 `a.Abort(...)` 收尾
   - `handlers_test.go` 新增路由 / Queued / subscribe 三组用例
   - 跑 `go vet` + `go test` + `npm run lint` + `npm run build:agent` + 手测 7.3 全部

> 拆分理由：PR 1 是纯新增 + Loop getter，0 行为变化；PR 2 是 SessionManager 内部改造（删 activeRun 字段 + 懒建路径），handler 还不切所以 IPC 行为不变；PR 3 才触及 handler / main / IPC schema —— 是真正的行为变更。三步都可独立回滚。

---

## 附录 C：和网易 OpenClaw / LobsterAI 的对照

沿用上一轮 spec 附录 A 的对照表，本 spec 补充 per-session AcpSession 一行的状态变化：

| 维度 | 网易实现 | 上一轮 spec 目标 | 本 spec 修复前 | 本 spec 修复后 | 一致性 |
|------|----------|-----------------|-------------|-------------|--------|
| session-keyed Agent | `Map<sessionId, AcpSession>` | `map[sessionID]*SessionEntry` (单 Agent) | `map[sessionID]*SessionEntry` (单 Agent) | `map[sessionID]*SessionEntry` (per-session AcpSession) | ❌ → ✅ 本 spec 修复 |
| `EventCommon.SessionID` 来源 | 该 session 自己的 Agent | `"default"` hardcode | `"default"` hardcode | 该 session 自己的 Agent | ❌ → ✅ 本 spec 修复 |
| AcpSession 懒建触发点 | subscribe / prompt 都触发 | subscribe 自动建 entry，prompt 才建 Agent | subscribe 建 entry，prompt 不建 Agent（无 factory 路径） | subscribe 只建 SessionEntry；prompt 才建 AcpSession（FR-8 两阶段） | 🟡 → ✅ 本 spec 修复 |
| in-flight 判据 source-of-truth | Loop 内置 activeRun | `SessionEntry.activeRun` 镜像 | `SessionEntry.activeRun` 镜像（但没人写） | `Loop.ActiveRunID()` getter | ❌ → ✅ 本 spec 修复（去镜像） |
| AcpSession 驱逐 | LRU + TTL | LRU + TTL | LRU + TTL（无 AcpSession 概念） | LRU + TTL（读 `Loop.ActiveRunID()` 判据） | ✅ 沿用 |
| AbortController | per-session | per-session（via Stop） | per-Loop（via Stop，但都是单 Loop） | per-Loop（每 session 一个 Loop） | ✅ 沿用 |
| 跨 session 真并发 | 独立 ctx + goroutine | 独立 ctx + goroutine | 独立 ctx + goroutine（但只有 1 个） | 独立 ctx + goroutine（每 session 一份） | ❌ → ✅ 本 spec 修复 |

**本 spec 修复的四条**：单 Agent → per-session AcpSession、subscribe 路径降级为"只建 entry 不建 AcpSession"（防资源爆炸）、in-flight 判据去镜像（消除 race）、IPC `PromptResult` 加 `Queued` 字段（让前端可见 follow-up queue 状态）。

## 附录 D：差异点说明（沿用上一轮 spec §附录 A 末段）

1. **session key 复合形态**：网易 OpenClaw 用 `agent:<agentId>:lobsterai:<uuid>` 复合 key，目的是让多 agent 共享同一 OpenClaw 网关时 session 不冲突；darvin-cowork 当前只有一个 agent 端点，**暂不引入复合 key**，只传 `uuidv4`；后续如需多 agent 再加
2. **im_session_mappings 表**：网易为了 IM 通道（飞书 / 钉钉等）与 UI session 双向映射独立建表；darvin-cowork 本期不接 IM 通道，**不做该表**
3. **SteerControl per-session 化**：网易 Steer 是核心交互（每个 AcpSession 自带 steerQueue）；darvin-cowork UI 本期不发 steer message，**不迁 SteerControl**，等 UI 真有 steer 需求时再迁（AGENTS.md:391 "不要主动 broad refactor"）