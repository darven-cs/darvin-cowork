# Agent ACP Loop 设计文档（S4）

> **Phase 2 / 6 — Go 阶段 spec #3**。把 ACP Loop + Queue + SteerControl 立起来，让 Gateway handler 真接 Agent.Run；EventLedger 订阅 event.Bus 推 WS notification；main.go 接 SIGTERM/SIGINT 优雅关闭。
> 前置：S2（数据层）+ S3（Gateway WS + JSON-RPC + SessionManager + EventLedger stub）。
> 本 spec 是 Go 阶段最后一块 — 落地后端到端可在 Go 侧独立验收（不依赖 Electron）。

---

## 1. 概述

### 1.1 问题 / 背景

S3 留下的 gap：

| 组件 | S3 状态 | 本 spec 落地 |
|------|--------|------------|
| Gateway handlers | stub：返回 mock sessionId/messageId | 替换为真接 ACP + Agent.Prompt/Run |
| EventLedger | EmitStub fake event | AttachBus 订阅 event.Bus + 推 WS notification |
| main.go | `select{}` 占位 | signal.NotifyContext(SIGTERM, SIGINT) + 优雅关闭 |
| ACP 包 | 不存在 | 新建 `internal/acp/{loop,queue,steer}.go` |
| SteerControl | 不存在 | 新建（v0 no-op） |

ACP 含义（架构文档 §"ACP 层"）：Go Agent 内的会话管理 + Turn 调度 + Steer（转向）控制 + Queue 管理。本 spec 实现**最薄一层**（不实现完整 QueueManager 调度算法，那是后续 spec）。

### 1.2 目标

- `internal/acp/` 新 package：loop / queue / steer
- `internal/acp/loop.go` 接 prompt handler → 调 `Agent.Prompt` + 异步 `Run`
- `internal/acp/queue.go` 用现有 `queue.Queue` 3 通道（steer / prompt / followup）
- `internal/acp/steer.go` SteerControl v0 no-op（接口留好，后续实现）
- EventLedger.AttachBus 订阅 event.Bus，按 S3 §4.2 表映射 event.Event → DarvinEvent → WS notification
- main.go 接 SIGTERM/SIGINT：收到信号 → `Agent.Abort` → flush events → close WS server → close DB → os.Exit(0)

### 1.3 非目标

- **不**实现 QueueManager 完整调度（v0 仅转发；多 prompt 排队等后续 spec）
- **不**实现 SteerControl 真实转向逻辑（v0 no-op）
- **不**实现 SubAgent 真实逻辑（仍 ErrSubAgentUnsupported）
- **不**实现 Memory / Dreaming
- **不**实现 Skills / MCP
- **不**实现 Failover / Circuit Breaker
- **不**把 messages 表写入逻辑（架构文档 §"SessionStore 表设计" 提到 messages 表；本 spec 不动 store，由 ACP 转发 Agent 状态后 store.Save 仍是 S6 才接）
- **不**改 EventLedger 接口签名（S3 已定）

---

## 2. 用户场景

### 场景 1：handler 真接 Agent.Run

**Given** WS 连接，gateway server 监听
**When** 发 `{"jsonrpc":"2.0","id":"1","method":"agent.prompt","params":{"content":"hi"}}`（content 非空）
**Then** handler 调 ACP.Loop.Prompt → Agent.Prompt(content) 入队 → 异步 Agent.Run → LLM 流式响应 → event.Bus emit TextDeltaEvent → EventLedger.AttachBus 收到 → 推 WS notification 到该 session 订阅者

### 场景 2：流式 WS notification

**Given** 场景 1 已 subscribe_events
**When** Agent.Run 中 LLM 流式返回
**Then** WS 收到多条 notification，method=`agent.event`，params 是 `DarvinTextDeltaEvent` 累积 delta，最后一条是 `DarvinDoneEvent{usage}`

### 场景 3：abort

**Given** Agent.Run 正在进行中
**When** 发 `{"jsonrpc":"2.0","id":"x","method":"agent.abort","params":{"sessionId":"s-xxx"}}`
**Then** handler 调 Agent.Abort → ctx cancel → 流终止 → 收尾 emit DoneEvent 或 ErrorEvent → LLMEndEvent.FinishReason = aborted

### 场景 4：SIGTERM 优雅关闭

**Given** Go agent 在 listen
**When** 进程收到 SIGTERM（Electron 主进程 kill 时转发）
**Then** signal.NotifyContext 触发 ctx.Done：
  1. WS server Shutdown（拒绝新连接，等现有 conn 关闭）
  2. Agent.Abort（取消所有 in-flight Run）
  3. flush pending events（≤2s 超时）
  4. DB 连接关闭（gorm.DB.Close）
  5. stderr 日志 "graceful shutdown complete"
  6. os.Exit(0)

整体时长 ≤ 3s。

### 场景 5：SIGINT 优雅关闭（同 SIGTERM）

**Given** Go agent 在 listen
**When** Ctrl+C（开发期 `npm start` 后 devtools 里手动触发）
**Then** 行为与 SIGTERM 完全相同

---

## 3. 功能需求

### FR-1：ACP 包结构

`internal/acp/` 新 package，依赖关系：
- `acp/loop.go` 依赖 `agent.Agent`（实现 Loop interface）
- `acp/queue.go` 用现有 `darvin-cowork/backend/internal/agent/queue`（v0 已实装）
- `acp/steer.go` no-op（接口定义）

```go
// internal/acp/loop.go
package acp

import (
    "context"
    "log"

    "darvin-cowork/backend/internal/agent"
    "darvin-cowork/backend/internal/agent/event"
)

type Loop struct {
    agent *agent.Agent
}

func NewLoop(a *agent.Agent) *Loop {
    return &Loop{agent: a}
}

// Prompt enqueues a user message; returns sessionId and messageId.
// The actual agent.Run runs asynchronously in the agent's internal goroutine.
func (l *Loop) Prompt(ctx context.Context, sessionID, content string) (messageID string, err error) {
    if err := l.agent.Prompt(ctx, content); err != nil {
        return "", err
    }
    msgID, err := generateMessageID()
    if err != nil { return "", err }
    go func() {
        if err := l.agent.Run(ctx); err != nil {
            log.Printf("acp: run: %v", err)
        }
    }()
    return msgID, nil
}

// Abort signals current run to stop.
func (l *Loop) Abort(ctx context.Context, sessionID string) error {
    return l.agent.Abort(ctx)
}
```

### FR-2：Queue 包

```go
// internal/acp/queue.go
package acp

import (
    "darvin-cowork/backend/internal/agent/queue"
)

// Queue wraps queue.Queue with session-aware enqueue.
// v0: 直接转发到现有 queue.Queue（不维护 session 维度的隔离）
type Queue struct {
    inner *queue.Queue
}

func NewQueue() *Queue {
    return &Queue{inner: queue.NewQueue()}
}

func (q *Queue) Enqueue(mode queue.Mode, content string) error {
    return q.inner.Enqueue(mode, content)
}

func (q *Queue) Dequeue(ctx context.Context) (queue.Message, queue.Mode, bool) {
    return q.inner.Dequeue(ctx)
}

func (q *Queue) Len() int {
    return q.inner.Len()
}
```

**注意**：现有 `queue.Queue` 是 v0 实现的 steer/prompt/followup 三通道优先级队列。本 spec 复用之。

### FR-3：SteerControl

```go
// internal/acp/steer.go
package acp

import (
    "context"

    "darvin-cowork/backend/internal/agent"
)

// SteerControl re-prioritises or redirects mid-run. v0 is no-op.
type SteerControl struct {
    agent *agent.Agent
}

func NewSteerControl(a *agent.Agent) *SteerControl {
    return &SteerControl{agent: a}
}

// Steer injects a new message into the steer channel (highest priority).
func (s *SteerControl) Steer(ctx context.Context, content string) error {
    return s.agent.Steer(ctx, content)
}

// Redirect cancels current turn and queues followup. v0: no-op.
func (s *SteerControl) Redirect(ctx context.Context, content string) error {
    // 远期实现：先 agent.Abort，再 agent.FollowUp
    return nil
}
```

### FR-4：handler 替换 stub

`internal/gateway/handlers.go` 替换 `handlePrompt` 和 `handleAbort`：

```go
type Handlers struct {
    Sessions *SessionManager
    Ledger   *EventLedger
    ACPLoop  *acp.Loop
}

func (h *Handlers) HandlePrompt(ctx context.Context, id json.RawMessage, params json.RawMessage) *Response {
    var p PromptParams
    if err := json.Unmarshal(params, &p); err != nil {
        return errorResp(id, CodeInvalidParams, "invalid params", err)
    }
    if p.Content == "" {
        return errorResp(id, CodeInvalidParams, "content is required", nil)
    }

    sess, _, err := h.Sessions.CreateOrGet(p.SessionID)
    if err != nil {
        return errorResp(id, CodeInternalError, "create session", err)
    }

    // subscribe 一个 notification channel，让 client 接收事件流
    h.Ledger.SubscribeSession(sess.ID, h.clientForSession(sess.ID))

    msgID, err := h.ACPLoop.Prompt(ctx, sess.ID, p.Content)
    if err != nil {
        return errorResp(id, CodeInternalError, "acp prompt", err)
    }

    return &Response{
        JSONRPC: "2.0",
        ID:      id,
        Result: PromptResult{SessionID: sess.ID, MessageID: msgID},
    }
}

func (h *Handlers) HandleAbort(ctx context.Context, id json.RawMessage, params json.RawMessage) *Response {
    var p AbortParams
    if err := json.Unmarshal(params, &p); err != nil {
        return errorResp(id, CodeInvalidParams, "invalid params", err)
    }
    if err := h.ACPLoop.Abort(ctx, p.SessionID); err != nil {
        return errorResp(id, CodeInternalError, "acp abort", err)
    }
    return &Response{
        JSONRPC: "2.0",
        ID:      id,
        Result:  AbortResult{Aborted: true, SessionID: p.SessionID},
    }
}
```

**注**：clientForSession 是 S3 client.go 持有的 sendNotification 方法；S4 需在 SessionManager 中维护 sessionId → client 映射，client 关闭时清空。

### FR-5：SessionManager 加 client 映射

`internal/gateway/sessionmgr.go` 加：

```go
type SessionManager struct {
    mu        sync.RWMutex
    sessions  map[string]*session.Session
    callbacks map[string][]SessionCallback
    clients   map[string]map[*client]struct{}  // sessionId → set of clients
    idGen     func() string
}

// AttachClient registers a client to receive events for sessionID.
// Called from handleSubscribeEvents (S3 stub) / HandlePrompt (S4 真接).
func (m *SessionManager) AttachClient(sessionID string, c *client) {
    m.mu.Lock()
    defer m.mu.Unlock()
    if m.clients[sessionID] == nil { m.clients[sessionID] = nil }
    if _, ok := m.clients[sessionID]; !ok {
        m.clients[sessionID] = map[*client]struct{}{}
    }
    m.clients[sessionID][c] = struct{}{}
}

func (m *SessionManager) DetachClient(c *client) {
    m.mu.Lock()
    defer m.mu.Unlock()
    for sid, set := range m.clients {
        delete(set, c)
        if len(set) == 0 { delete(m.clients, sid) }
    }
}

// NotifyAll forwards ev to all clients attached to sessionID.
func (m *SessionManager) NotifyAll(sessionID string, ev any) {
    m.mu.RLock()
    defer m.mu.RUnlock()
    for c := range m.clients[sessionID] {
        c.SendNotification("agent.event", ev)
    }
}
```

### FR-6：EventLedger.AttachBus 真接 event.Bus

`internal/gateway/eventledger.go` 替换 EmitStub：

```go
// AttachBus subscribes EventLedger to event.Bus. Each event from the bus
// is mapped per S3 §4.2 table and forwarded to all attached clients via
// SessionManager.NotifyAll.
func (l *EventLedger) AttachBus(bus *event.Bus, sessions *SessionManager) {
    sub := bus.Subscribe(64)
    go func() {
        for ev := range sub.C() {
            l.dispatch(ev, sessions)
        }
    }()
}

// dispatch maps event.Event → DarvinEvent and forwards.
func (l *EventLedger) dispatch(ev event.Event, sessions *SessionManager) {
    de, sessionID := l.toDarwinEvent(ev)
    if de == nil { return }
    sessions.NotifyAll(sessionID, de)
}

// toDarwinEvent maps event.Event types to the DarvinEvent schema
// documented in S3 §4.2.
func (l *EventLedger) toDarwinEvent(ev event.Event) (DarvinEvent, string) {
    switch e := ev.(type) {
    case event.TextDeltaEvent:
        return DarvinTextDeltaEvent{
            Type: "text_delta", Delta: e.Delta,
            SessionID: "", MessageID: "",  // 由事件携带（v0 event.Event 没这两字段，留空）
        }, ""
    // ... 其他类型同 S3 §4.2 表
    default:
        return nil, ""
    }
}
```

**实现细节**：`event.Event` 子类型当前可能不带 sessionId / messageId。本 spec 加 `event` 包**配套小改**：

```go
// internal/agent/event/event.go 加共享辅助
type EventCommon struct {
    SessionID string
    MessageID string
}

// 各具体事件嵌入 EventCommon
type TextDeltaEvent struct {
    EventCommon
    Delta string
}
```

这是对 event 包的小侵入（每个具体事件加 EventCommon 嵌入）。executor 在 emit 时填充 SessionID/MessageID。

### FR-7：executor 加 SessionID/MessageID 传递

`internal/agent/executor/executor.go` 的 emit 调用点：

```go
// LLMStartEvent
d.Emit(event.LLMStartEvent{
    EventCommon: event.EventCommon{SessionID: d.Session().ID, MessageID: msgID},
    Model: req.Model,
})

// TextDeltaEvent
d.Emit(event.TextDeltaEvent{
    EventCommon: event.EventCommon{SessionID: d.Session().ID, MessageID: msgID},
    Delta: delta.Delta,
})
// ... 其他 6 类同理
```

### FR-8：main.go SIGTERM/SIGINT 优雅关闭

`cmd/app/main.go` 替换 S3 的 `select{}`：

```go
// --- Graceful shutdown ---
ctx, cancel := signal.NotifyContext(context.Background(),
    syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)
defer cancel()

// GS 是 gateway.Server
go func() {
    <-ctx.Done()
    log.Info("shutdown signal received")

    // 1. WS server 拒新连接 + 等现有连接关闭
    shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
    defer shutdownCancel()
    if err := gs.Shutdown(shutdownCtx); err != nil {
        log.Error("gateway shutdown", zap.Error(err))
    }

    // 2. Agent abort
    if err := a.Abort(context.Background()); err != nil {
        log.Error("agent abort", zap.Error(err))
    }

    // 3. DB close
    sqlDB, _ := database.Get().DB()
    if sqlDB != nil {
        if err := sqlDB.Close(); err != nil {
            log.Error("db close", zap.Error(err))
        }
    }

    log.Info("graceful shutdown complete")
    os.Exit(0)
}()

// 阻塞到信号
<-ctx.Done()
```

⚠️ **OS Exit 之前 defer 不一定执行**：上面的 `os.Exit(0)` 跳过 defer chain，所以 logger.Sync 等放在 main 顶层的 defer 是必要的。

### FR-9：stderr 日志不污染 stdout

main.go 顶层：

```go
logCfg := &logger.Config{
    Level:    cfg.Log.Level,
    Encoding: cfg.Log.Encoding,
    Output:   cfg.Log.Output,  // 保持现有 stdout 输出（日志库决定编码）
    // 但任何 fmt.Fprintf(os.Stdout, ...) 必须只用于 <port>...</port> 一行
}
```

fmt.Fprintf(os.Stdout, "<port>...</port>") 保持唯一一行。其他任何 stdout 输出（包括 log.Info 的 JSON encoder）都改走 stderr（如果当前 logger 库默认走 stdout，本 spec 改 Output: stderr 或新增 logger.OutputTarget: "stderr"）。

**Electron RuntimeMgr 解析 stdout 时严格匹配 `<port>\d+</port>`**，其他内容忽略。

---

## 4. 实现方案

### 4.1 目录结构

```
src/darvin-agent/internal/
├── acp/
│   ├── loop.go        # 🆕 ACP Loop
│   ├── queue.go       # 🆕 Queue wrapper
│   ├── steer.go       # 🆕 SteerControl (no-op)
│   └── loop_test.go   # 🆕
├── gateway/
│   ├── server.go      # 改：增加 Client 池 + graceful Shutdown 路径
│   ├── client.go      # 改：DetachClient + SendNotification
│   ├── handlers.go    # 改：替换 stub 为 ACP 真接
│   ├── sessionmgr.go  # 改：加 AttachClient / DetachClient / NotifyAll
│   └── eventledger.go # 改：AttachBus + 替换 EmitStub
├── agent/
│   ├── event/event.go # 改：加 EventCommon 嵌入
│   └── executor/executor.go # 改：emit 时填 SessionID/MessageID
└── cmd/app/main.go    # 改：signal.NotifyContext + 优雅关闭
```

### 4.2 关键决策

#### 4.2.1 同步返回 vs 异步流式

`HandlePrompt` 同步返回 `{ sessionId, messageId }`（含 msgID 是 ACP 给的临时 ID，最终由 DoneEvent 覆盖）。

`Agent.Prompt` + `Agent.Run` 在 goroutine 内异步跑；事件通过 event.Bus → EventLedger → SessionManager.NotifyAll → client.SendNotification 推回 WS。

**重要**：handler 不能等 Agent.Run 完成才返回，否则 WS 阻塞，Electron 侧 prompt 调用 hang。

#### 4.2.2 ctx 生命周期

Handler 的 ctx 是 WS 连接 ctx（HTTP req.Context），连接断开时 ctx cancel。

Agent.Run 用独立的 background ctx（**不**用 handler 的 req ctx），否则连接断开就杀掉 Agent.Run —— 即使其他 client 还在订阅。S4 实装：

```go
// acp/loop.go
func (l *Loop) Prompt(ctx context.Context, sessionID, content string) (string, error) {
    if err := l.agent.Prompt(ctx, content); err != nil { return "", err }
    msgID := generateID()
    // 用 background ctx，让 Agent.Run 不被 WS 断开影响
    go func() {
        runCtx := context.Background()  // 🆕
        if err := l.agent.Run(runCtx); err != nil {
            log.Printf("acp: run: %v", err)
        }
    }()
    return msgID, nil
}
```

⚠️ **后续 spec 引入 per-session ctx**（按 session 维度管理生命周期）。本 spec 简化。

#### 4.2.3 SessionID 传递到 event.Event

当前 `event.Event` 子类型不带 SessionID。S4 加 `EventCommon` 嵌入 + executor 填充。这是 event 包的小侵入，向后兼容（旧 fixture 仅 emit 时不填 EventCommon 仍 OK）。

#### 4.2.4 SIGTERM/SIGINT 双触发

`signal.NotifyContext` 对同一信号多次接收时只触发一次 ctx.Done。OS 第二次发 SIGKILL 才会硬杀。v0 行为：第一次 SIGTERM 优雅关；第二次 SIGKILL 由 OS 强杀。

#### 4.2.5 日志输出分离

S3 已要求 stdout 只输出 `<port>` 一行。本 spec 进一步保证：
- logger 库输出走 stderr（避免污染）
- fmt.Fprintf(os.Stdout, ...) 仅 server.Start 用一次

如果当前 logger 库不支持 stdout/stderr 分离，main.go 加：

```go
// 在 logger.Init 之前
if cfg.Log.Output == "stdout" {
    cfg.Log.Output = "stderr"
}
```

或 logger.Config 加 `StderrOnly: true` 字段。本 spec 选最小侵入：加 stderr 字符串配置。

#### 4.2.6 graceful shutdown 超时

3 秒硬超时。期间 abort Agent.Run（流终止，可能产生 ErrorEvent）。超时则硬退出（不等待）。

### 4.3 关键代码骨架

```go
// internal/gateway/client.go SendNotification (S4 续)
func (c *client) SendNotification(method string, params any) {
    if c.closed { return }
    c.writeJSON(Notification{
        JSONRPC: "2.0",
        Method:  method,
        Params:  params,
    })
}

// client.run() 在 defer 内 DetachClient
func (c *client) run(ctx context.Context) {
    defer c.conn.Close()
    defer func() {
        c.closed = true
        if c.sessions != nil {
            c.sessions.DetachClient(c)
        }
    }()
    // ... 已有 loop ...
}
```

```go
// internal/gateway/handlers.go 完整 Handlers struct
type Handlers struct {
    Sessions *SessionManager
    Ledger   *EventLedger
    ACPLoop  *acp.Loop
    Steer    *acp.SteerControl
}

// NewHandlers wires deps
func NewHandlers(sessions *SessionManager, ledger *EventLedger, loop *acp.Loop, steer *acp.SteerControl) *Handlers {
    return &Handlers{Sessions: sessions, Ledger: ledger, ACPLoop: loop, Steer: steer}
}

// dispatch updated to use Handlers
func dispatch(ctx context.Context, req *Request, h *Handlers) *Response {
    switch req.Method {
    case "agent.prompt":
        return h.HandlePrompt(ctx, req.ID, req.Params)
    case "agent.abort":
        return h.HandleAbort(ctx, req.ID, req.Params)
    case "agent.steer":
        return h.HandleSteer(ctx, req.ID, req.Params)  // 🆕
    default:
        // ... existing
    }
}
```

---

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| 同一 client 多次 agent.subscribe_events 同一 session | 幂等（已 AttachClient 过则跳过） |
| Agent.Run panic | recover → stderr log + emit AgentErrorEvent |
| ctx 取消（WS 断开） | handler 立即返回；Agent.Run 用 background ctx 不受影响 |
| ACP.Prompt 失败（queue full） | 返回 CodeInternalError |
| Agent.Abort 在 Run 之外调用 | 返回 nil（无 in-flight run） |
| SIGTERM 时 WS 还有 client 在飞 message | Shutdown ctx 3s 超时；超时硬关 |
| SIGTERM 时 Agent.Run 还在 LLM 流 | Agent.Abort 立即触发 → ctx.Done 传播 → SSE 关闭 → DoneEvent |
| 第二次 SIGTERM | signal.NotifyContext 不会再次 Done；进程已被 os.Exit(0) |
| SIGQUIT (Ctrl+\\) | 同 SIGTERM 处理 |
| Agent.Run 完成后 emit DoneEvent 时 client 已断开 | writeJSON 失败 → log 忽略 |
| EventLedger.NotifyAll 在 client 已 close 时 | client.closed 短路 |
| event.Event 子类型未嵌入 EventCommon（遗留 fixture） | toDarwinEvent 返回 nil，跳过 |

---

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/darvin-agent/internal/acp/loop.go` | 🆕 |
| `src/darvin-agent/internal/acp/queue.go` | 🆕 |
| `src/darvin-agent/internal/acp/steer.go` | 🆕 |
| `src/darvin-agent/internal/acp/loop_test.go` | 🆕 |
| `src/darvin-agent/internal/gateway/handlers.go` | 改：Handlers struct + 替换 stub |
| `src/darvin-agent/internal/gateway/sessionmgr.go` | 改：AttachClient / DetachClient / NotifyAll |
| `src/darvin-agent/internal/gateway/client.go` | 改：SendNotification + defer DetachClient |
| `src/darvin-agent/internal/gateway/eventledger.go` | 改：AttachBus + 替换 EmitStub |
| `src/darvin-agent/internal/gateway/server.go` | 改：dispatch(ctx, req, *Handlers) |
| `src/darvin-agent/internal/gateway/server_test.go` | 改：mock ACP Loop 注入测试 |
| `src/darvin-agent/internal/agent/event/event.go` | 改：加 EventCommon 嵌入各具体事件 |
| `src/darvin-agent/internal/agent/executor/executor.go` | 改：emit 填 SessionID/MessageID |
| `src/darvin-agent/cmd/app/main.go` | 改：signal.NotifyContext + graceful shutdown + stderr 日志 |

**不修改**：
- llm / ctxengine / session / store / tool / queue
- S2 落地的 SessionStore / models
- S3 的 server.go 主体（仅 dispatch 签名改）

---

## 7. 验收标准

- [ ] `go build ./...` 编译通过
- [ ] `go vet ./...` 无警告
- [ ] `gofmt -l .` 干净
- [ ] `go test ./internal/... -race` 全绿
- [ ] 启动后 stdout 仅输出 `<port>NNNNN</port>` 一行；stderr 含 "gateway listening port=..." 日志
- [ ] `wscat -c ws://localhost:NNNNN` 连上
- [ ] 发 `agent.prompt{content:"hi"}` → 立即收到 `{sessionId, messageId}`
- [ ] **关键**：发完 prompt 后 1s 内 WS 收到多条 notification（method=agent.event），含 `text_delta` 累积 delta（Anthropic 流式响应）
- [ ] 末条 notification 是 `done` 类型，含 usage
- [ ] 末条之后是 `agent_end`
- [ ] 发 `agent.abort`（在 prompt 流式期间）→ 后续 notification 含 `error` 或 `done` with `stopReason=aborted`
- [ ] `kill -TERM <pid>` → 进程在 3s 内退出，stderr 含 "graceful shutdown complete"
- [ ] `kill -INT <pid>` 同上
- [ ] 优雅关闭期间新 WS 连接被拒（http.Server.Shutdown 行为）
- [ ] 优雅关闭后 sessions.db 不损坏（重启可 Load 之前 Save 的 session）
- [ ] event.Event 各具体事件类型都嵌入 EventCommon（go vet 无 field alignment 警告）
- [ ] executor emit 调用填 SessionID/MessageID，event.Bus 订阅者能读到

---

## 8. 后续 spec 候选（不在本 spec 范围）

| Spec | 内容 |
|------|------|
| **S5** electron-runtime-client | Electron spawn Go agent + WS client + preload 真接 |
| **S6** agent-e2e-integration | 三层联调 + session 持久化 + 重启可见 |
| ACP QueueManager 完整调度 | 多 prompt 排队 + 优先级动态调整 |
| SteerControl 真实转向 | 当前 no-op；接入 queue.Queue 的 steer 通道 |
| SubAgent 真实实现 | 替换 ErrSubAgentUnsupported |
| event.Event 子类型继续扩展 | memory.* / skill.* / mcp.* 等 |
| store.Save 接 ACP.Prompt 完成 | 把 messages 写入 sessions.db |