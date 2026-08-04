# 04 — Gateway 集成

> 状态: 草案 v1 · 2026-08-04
> 父 spec: `00-harness-architecture-design.md`
> 前置: `01-harness-core-interface.md`, `02-agent-refactor.md`, `03-selection-and-plugin.md`
> 输出: `internal/gateway/handlers.go` + `sessionmgr.go` + `internal/acp/` 调整

## 1. 目标

把 darvin-cowork gateway 从**直接绑 `acp.AcpSession`** 改为**走 harness 抽象**。具体说:

- 保留 `acp.AgentFactory` 和 `acp.Loop`(因为它们是 per-session turn queue + steer + abort,这些 OpenClaw 也没有)
- 但 AgentFactory 内部从"new 一个 *Agent"改为"从 harness registry 拿 harness"
- handlers.handlePrompt 从"调 `entry.Acp.Loop.Submit`"改为"调 `harness.RunAttemptWithLifecycle`"
- SessionManager 的 `Acp *acp.AcpSession` 字段保留(向后兼容 RPC),但内部走 harness

**核心思路**: 让 `acp.AcpSession` 变成 harness 的 thin wrapper,**不是**反过来。`acp.Loop` 仍存在但内部走 `harness.RunAttempt`,而不是直接 `agent.Prompt` / `agent.Run`。

## 2. 当前调用链(改造前)

```
WebSocket message  "agent.prompt"
   ↓
handlePrompt(ctx, params)
   ↓
c.sessions.GetOrCreateEntry(sessionID)
   ↓ entry.Acp = factory.NewAcpSession(id)
entry.Acp.Loop.Submit(acp.PromptRequest{...})
   ↓
Loop.admit(...)  [内部 push to followUpQueue]
   ↓
Loop.run() goroutine
   ↓
Loop.executeTurn(req)
   ↓
l.agent.Prompt(...)  +  l.agent.Run(...)
   ↓
agent.dispatcher.Run()
   ↓
agent.exec.RunConversation(ctx, a)
   ↓
LLM stream + tool executor
   ↓ emit event
agent.bus.Emit()
   ↓
EventLedger.AttachSubscription 接到 agent.bus
   ↓
publishLocked → WS broadcast
```

## 3. 目标调用链(改造后)

```
WebSocket message  "agent.prompt"
   ↓
handlePrompt(ctx, params)
   ↓
c.sessions.GetOrCreateEntry(sessionID)
   ↓ entry.Harness = harness.SelectHarnessFor(...)
       entry.Acp = newAcpSession(entry.Harness, ...)
entry.Acp.Submit(acp.PromptRequest{...})   // API 保持
   ↓
Loop.run() goroutine
   ↓
harness.RunAttemptWithLifecycle(ctx, entry.Harness, params)
   ↓ (entry.Harness.RunAttempt)
embeddedHarness.RunAttempt(ctx, params)
   ↓ (Phase 2 已经瘦身的 Agent)
agent.Prompt + agent.Run  (acp 内部继续调 Agent)
   ↓
executor.RunConversation
   ↓
emit event → bus → ledger → WS
```

**关键变化**:
- `entry.Acp` 仍然存在(向后兼容),但内部 `Loop.executeTurn` 不再直接调 `agent.Prompt`,而是构造 `harness.RunAttemptParams` 调 `entry.Harness.RunAttempt`
- `entry.Harness` 是新字段(由 `harness.SelectHarness` 在懒建时填)

## 4. 详细改动

### 4.1 `internal/acp/factory.go` 改动

```go
// 改造前
type AgentFactory struct {
    Name         string
    Instructions string
    Model        agent.ModelRef
    Provider     llm.ModelProvider
    Store        store.SessionStore
    MessageStore store.MessageStore
    Logger       *zap.Logger
    Config       agent.Config
    Tools        *tool.Registry
    Assembler    ctxengine.ContextEngine
    Plugins      []tool.Plugin
    AssemblerEnabled bool
}

func (f *AgentFactory) Build(sessionID string) (*agent.Agent, error) { ... }
func (f *AgentFactory) NewAcpSession(sessionID string) (*AcpSession, error) { ... }

// 改造后
type AgentFactory struct {
    // ... 现有字段(继续用,Build 内部构造 agent)
    HarnessID string                  // 新增:本次 factory 绑定哪个 harness;空 = 运行时 SelectHarness
}

func (f *AgentFactory) NewAcpSession(sessionID string) (*AcpSession, error) {
    h, err := f.resolveHarness()      // 1. 优先 f.HarnessID;2. 兜底 harness.SelectHarness
    if err != nil { return nil, err }

    a, err := f.Build(sessionID)      // 保留
    if err != nil { return nil, err }

    return &AcpSession{
        SessionID: sessionID,
        Agent:     a,
        Harness:   h,                 // 新增字段
        Loop:      NewLoop(a, h),     // Loop 接受 harness
        DeltaHook: deltaHook,
    }, nil
}

func (f *AgentFactory) resolveHarness() (harness.Harness, error) {
    if f.HarnessID != "" {
        h, ok := harness.Get(f.HarnessID)
        if !ok { return nil, fmt.Errorf("harness %q not registered", f.HarnessID) }
        return h, nil
    }
    // 运行时选择:按 provider + model
    decision, err := harness.SelectHarness(harness.SelectionParams{
        Provider: extractProviderName(f.Provider),
        ModelID:  f.Model.Model,
        Config:   f.ConfigRef,         // 新增字段
    })
    if err != nil { return nil, err }
    return decision.Harness, nil
}
```

### 4.2 `internal/acp/loop.go` 改动

```go
// 改造前
type Loop struct {
    ctx      context.Context
    agent    *agent.Agent
    // ...
}

func (l *Loop) executeTurn(req promptReq) {
    if req.skill != nil { l.agent.RunSkillSession(...); return }
    l.agent.Prompt(...)
    // ...
}

// 改造后
type Loop struct {
    ctx     context.Context
    agent   *agent.Agent
    harness harness.Harness          // 新增
    // ...
}

func NewLoop(a *agent.Agent, h harness.Harness) *Loop { ... }

func (l *Loop) executeTurn(req promptReq) {
    if req.skill != nil {
        // skill 走特殊路径:还直接调 Agent
        l.agent.RunSkillSession(...)
        return
    }

    // 走 harness
    params := harness.RunAttemptParams{
        SessionID:   l.sessionID,
        SessionKey:  l.sessionKey,
        SessionFile: l.sessionFile,
        Prompt:      req.content,
        PromptImages: req.images,
        Attachments:  req.attachments,
        Provider:    extractProviderName(l.agent.Provider()),
        Model:       l.agent.ModelName(),
        AbortSignal:  runCtx,
        // ... 其它字段从 Agent / AcpSession 抽
    }

    _, err := harness.RunAttemptWithLifecycle(runCtx, l.harness, params)
    if err != nil { ... }
}
```

### 4.3 `internal/gateway/sessionmgr.go` 改动

```go
type SessionEntry struct {
    Session *session.Session
    Acp     *acp.AcpSession        // 保留(向后兼容 acp.Loop 公开 API)
    Harness harness.Harness         // 新增(可空:handler 测试 stub 不注入时)

    lastTouchedMs  int64
    stoppedUntilMs int64
    cancel         context.CancelFunc
    idleElem       *list.Element
}
```

懒建流程:

```go
func (m *SessionManager) attachAcpLocked(e *SessionEntry) error {
    if m.factory == nil { return nil }  // handler 测试 stub
    a, err := m.factory.NewAcpSession(e.Session.ID)
    if err != nil { return err }
    e.Acp = a
    e.Harness = a.Harness              // 复制过来
    // 启动 cancel monitor
    e.cancel = ...
    return nil
}
```

### 4.4 `internal/gateway/handlers.go` 改动

`handlePrompt` / `handleAbort` / `handleSteer` / `handleCompactContext` 4 个函数:

```go
// handlePrompt 几乎不动(API 兼容)
func handlePrompt(ctx, id, params, c, h) *Response {
    // ... 现有校验 + GetOrCreateEntry ...

    // 改造前:
    // ticket, err := entry.Acp.Loop.Submit(...)

    // 改造后:多一步解析 harness,然后让 AcpSession 内部走
    if entry.Acp == nil {
        return errorResp(id, CodeNoAcpSession, "no AcpSession bound", nil)
    }
    ticket, err := entry.Acp.Loop.Submit(acp.PromptRequest{
        RunID: p.RunID, Content: p.Content,
        Attachments: p.Attachments, Images: p.Images,
    })
    // ... 现有 return successResp ...
}
```

**关键**: handler public API 完全不变。`entry.Acp.Loop.Submit` 仍然存在(向后兼容 renderer),只是 AcpSession 内部走 harness。

**新增能力**: 未来加 "agent.change_harness" RPC 可让 renderer 切 harness;Phase 6 不实现。

## 5. 启动流程(main.go 改动)

```go
// 改造前
factory := &acp.AgentFactory{
    Name: ..., Instructions: ..., Model: ..., Provider: ...,
    Store: ..., MessageStore: ..., Logger: ..., Config: ...,
    Tools: ..., Assembler: ..., Plugins: ...,
}
sessions := gateway.NewSessionManager(gateway.WithAgentFactory(factory), ...)

// 改造后
factory := &acp.AgentFactory{
    // ... 同样字段 ...
    HarnessID:  "",            // 留空 → 运行时 SelectHarness
    ConfigRef:  cfg,           // 传 cfg 用于 selection
}

// 注册 builtin harness。harness 包不持有 agent 引用,能力通过闭包注入
// (spec 01 §7),闭包由 wiring 层持有 factory / sessionmgr。
harness.MustRegister(harness.NewEmbedded(harness.EmbeddedConfig{
    Run: func(ctx context.Context, p harness.RunAttemptParams) (*harness.AttemptResult, error) {
        // 把 p 转成 acp.PromptRequest,驱动对应 session 的 Loop
    },
}), "")
// 未来: harness.MustRegister(harness.NewCliHarness(...), "cli")

sessions := gateway.NewSessionManager(gateway.WithAgentFactory(factory), ...)
```

关闭路径同样要接上 —— spec 07 C8 之前 `Harness.Dispose` 没有任何调用方,进程退出时 harness 不会被拆:

```go
// gateway 关闭 / 进程退出时
defer func() {
    if err := harness.DisposeAll(context.Background()); err != nil {
        log.Warn("harness dispose failed", "err", err)
    }
}()
```

`DisposeAll` 对每个 harness 单独收敛错误(`errors.Join`),一个失败不阻断其余。

embedded harness **不设** `harness.SetObserver`(spec 07 C10):它内部的 `Agent.Run` 已经在发 `RunStartEvent` / `RunEndEvent`,再设一个 Observer 会让订阅者收到重复语义。Observer 留给未来的 CLI / plugin harness。

## 6. 不动的东西

- 所有 WebSocket RPC 协议(`agent.prompt` / `agent.abort` / `agent.subscribe_events` / `agent.steer` / `agent.compact_context` / `agent.list_sessions` / ...)
- EventBus 协议(所有 event 类型 + EventCommon 字段)
- 数据库 schema
- `internal/llm/` / `internal/tools/` / `internal/skills/` / `internal/mcp/` 接口
- `executor.RunConversation` 算法

## 7. 风险与缓解

| 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|
| AcpSession.Loop 改走 harness 后,RunSkillSession 没跟改 | 高 | 中 | 显式标注:`req.skill != nil` 仍直接调 agent.RunSkillSession(不走 harness,保持 skill 的 transient state 逻辑不变) |
| Selection 在 main.go 启动时 config 还没准备好,选错 harness | 中 | 高 | factory.resolveHarness 在懒建时调,不是启动时;config 一定 ready |
| event 流多了一层(Loop 调 harness 调 agent),RunID / MessageID 丢失 | 中 | 高 | Loop 显式构造 harness.RunAttemptParams 时把 msgIDSrc / runIDSrc 传进去;lifecycle 把它们绑给 harness.RunAttempt |
| Harness.RunAttempt 失败时,Loop 不知道回滚 | 中 | 中 | 失败时,RunAttempt 同步返回 error;Loop.executeTurn 现有的 emit AgentErrorEvent 逻辑保留 |
| `internal/acp/` 包名意义进一步弱化(已经是内部 turn loop) | 高 | 低 | Phase 7: 视情况把 `internal/acp/` 整体 rename 到 `internal/agentloop/`(本 spec 不做) |
| EventLedger 现在订阅的是 agent.bus,harness 改后是同一个 bus 吗 | 低 | 高 | 验证:embeddedHarness.RunAttempt 调 agent.Prompt,emit 走的是 agent.bus,EventLedger 仍订阅它。**0 改动** |

## 8. 测试要求

### 8.1 既有测试必须全过

```
$ go test -count=1 -short ./internal/gateway/...
$ go test -count=1 -short ./internal/acp/...
```

包括:
- `handlers_test.go` (handler 单测 + 集成)
- `sessionmgr_test.go` (SessionManager 各种场景)
- `handlers_skill_test.go` (skill 路径)
- `eventledger_test.go` (event fanout)
- `loop_test.go` (Loop 行为)
- `factory_test.go` (AgentFactory 构造)

### 8.2 新增测试

| 文件 | 测试 | 覆盖 |
|---|---|---|
| `gateway_integration_test.go` | `TestHandlePromptGoesThroughHarness` | 端到端:WS prompt → harness.RunAttempt → LLM mock → event fanout |
| 同上 | `TestHandlePromptFactoryResolvesHarness` | factory 选 harness 正确 |
| 同上 | `TestHandleAbortStopsHarness` | Abort 触发 harness.Abort |
| 同上 | `TestHarnessNotRegistered` | explicit HarnessID 不存在 → error |
| `acp/loop_test.go` | `TestLoopExecuteTurnCallsHarness` | Loop.executeTurn 调 harness.RunAttempt |
| 同上 | `TestLoopSkillBypassHarness` | skill 路径不走 harness |

总新增: ≥ 6 个 case。

## 9. Phase 6 提交清单

```bash
$ git add internal/acp/factory.go internal/acp/loop.go
$ git add internal/gateway/sessionmgr.go internal/gateway/handlers.go
$ git add cmd/app/main.go
$ go test -count=1 -short ./...   # 必须全 PASS
$ go test -count=1 ./internal/gateway/... ./internal/acp/...   # 集成测试
$ git commit -m "feat(gateway): route prompts through Harness abstraction

改造:
- AgentFactory 新增 HarnessID + ConfigRef 字段,resolveHarness() 选 harness
- AcpSession 持 Harness 引用,Loop.executeTurn 调 harness.RunAttempt
- SessionEntry 新增 Harness 字段
- main.go 启动时 Register(embedded)

RPC 协议不变,EventBus 协议不变,数据库 schema 不变。

Spec: specs/features/agent-harness-architecture/04-gateway-integration.md"
```

## 10. 验收标准

1. `go build ./...` 通过
2. `go vet ./...` 通过
3. 既有 `internal/gateway/*_test.go` + `internal/acp/*_test.go` 0 改动 0 失败
4. 新增 6 个集成测试全过
5. 一次端到端跑:`client.prompt → LLM first chunk` 延迟 < 100ms (smoke log 历史 baseline)
6. 内存使用增量 < 5MB (Harness struct 比 Agent 多了几个 func,影响微)
7. `make lint-agents-boundaries` 仍然通过(Phase 0 引入的 lint rule):harness 包可以 import agents,反过来不行

## 11. 与其它 spec 的接口

- **01 spec**: `Harness` interface,本 spec 是它的**第一个真实消费者**
- **02 spec**: `agent.Agent` 已瘦身,本 spec 通过 `embeddedHarness.RunAttempt` 调它
- **03 spec**: Selection 在 factory.resolveHarness 调,本 spec 是它的**第一个真实消费者**
- **05 spec**: Tool bridge 接 harness 和 agent,本 spec **不直接调** tool bridge
- **06 spec**: ctx engine 接 harness 的 Compact capability,本 spec **不调** Compact
- **07 spec**: 关闭路径调 `DisposeAll`(C8);embedded 不设 `Observer`(C10);`RunAttemptParams.ContextEngine` 由本 spec 从 session 状态填入(C5)
