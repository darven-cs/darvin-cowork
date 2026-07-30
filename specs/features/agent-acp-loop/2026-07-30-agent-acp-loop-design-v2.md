# Agent ACP Loop 设计文档 v2（S4）

> **Phase 2 / 6 — Go 阶段 spec #3**。把 ACP Loop + Queue + SteerControl 立起来，让 Gateway handler 真接 Agent.Run；EventLedger 订阅 event.Bus 推 WS notification；event.Event 增 EventCommon 嵌入；executor / dispatcher 填 SessionID/MessageID；anthropic stream 补 thinking_delta 解析；main.go shutdown 序列加 Agent.Abort + DB.Close。
>
> **v2 状态（2026-07-30）**：基于 S3 实际落地代码（`e8a8055`）+ v1 spec（`2026-07-29-agent-acp-loop-design.md`）审计后重写。**v1 作废**，仅历史参考。修订清单见 §0。
>
> **前置**：S2（数据层 SessionStore）+ S3（Gateway WS + JSON-RPC + SessionManager + EventLedger stub + signal.NotifyContext + `<port>` 单行 stdout）。
>
> **本 spec 落地后**：Go 侧可独立端到端验收（不依赖 Electron）。

---

## 0. 相对 v1 的修订清单（7 P0 + 5 P1）

### P0（硬冲突：v1 描述与 S3 实际代码直接矛盾，落地会立即报错或跑偏）

| # | v1 描述 | v2 修正 |
|---|---|---|
| **P0-1** | `AttachBus(bus *event.Bus, sessions *SessionManager)` 全篇 8+ 处 | S3 实际是 `AttachSubscription(_ *event.Subscription)`（`eventledger.go:75`），签名收 `*Subscription` 不是 `*Bus`。v2 改用 `bus.Subscribe(64) → sub` → `ledger.AttachSubscription(sub)` |
| **P0-2** | v1 §3 FR-5 给 SessionManager 加 `clients map + AttachClient/DetachClient/NotifyAll` | **整套死代码**。S3 EventLedger.`bySession` + `Subscribe`/`UnsubscribeAll`/`publishLocked` 已实装。v2 **删 FR-5** |
| **P0-3** | v1 §3 FR-4 `Handlers struct{...}` + `HandlePrompt(ctx, id, params) *Response`（3 参）+ 自动 subscribe | S3 实际 `dispatchRequest(ctx, req, c *client) *Response`（4 参）+ subscribe 走独立 RPC。v2 保留 `*client` 4 参签名，**不**自动 subscribe（避免破坏 S3 跟 S1 TS 契约）|
| **P0-4** | v1 §1.1 "main.go `select{}` 占位" + v1 §3 FR-8 整段 shutdown | S3 main.go:72-179 已实装 `signal.NotifyContext(SIGINT, SIGTERM)` + 3s `gs.Shutdown` + "graceful shutdown complete" 日志。v2 **删 FR-8 主干**，只增量加 `Agent.Abort` + `sqliteStore.Close` 两步 |
| **P0-5** | v1 `Loop.Prompt(ctx, sessionID, content) (msgID, err)` | Agent 实际 `Prompt(ctx, content) error`（`dispatcher.go:15`）—— **不收 sessionID、不返 msgID**。v2 改 `Loop.Prompt(ctx, content) (msgID, err)`：msgID 由 Loop 内部生成并保存供 Deps 读；sessionID 不在 Loop 范围内（由 gateway 决定）|
| **P0-6** | v1 `toDarwinEvent(ev) (DarvinEvent, string)` 从 event 提取 sessionID | event 包当前无 SessionID/MessageID 字段（除 RunStart/AgentEnd 有 SessionID）。v2 引入 `EventCommon{SessionID, MessageID}` 嵌入所有具体事件，`AttachSubscription` goroutine 通过 `ev.Common().SessionID` 提取 |
| **P0-7** | v1 `type SteerControl struct{...}`（具体）| v2 改 `type SteerControl interface { Steer; Redirect }`（接口，跟 CHECKLIST 对齐）+ v0 `steerControl` impl + `ErrSteerNotImplemented` sentinel |

### P1（设计选择：需要审过）

| # | v1 描述 | v2 修正 |
|---|---|---|
| **P1-1** | v1 FR-7 executor 填 EventCommon 只举 2 个例子（"其他 6 类同理"）| 当前 executor.go **9 个 emit 点** + dispatcher.go **3 个 emit 点**（RunStart/RunEnd/AgentEnd），共 12 个。v2 完整列表 + Deps 加 `CurrentMessageID() string` |
| **P1-2** | v1 §4.2.2 说 Loop 用 `context.Background()` 跑 Run | v2 明确 ctx 生命周期：Run goroutine 用 background ctx，**取消走 Agent.Abort（cancelFn）**，handler 的 req ctx 不影响 Run。WS 断开 ≠ Run 取消 |
| **P1-3** | v1 §1.1 描述 EventLedger 状态为 "EmitStub fake event" | v2 区分：EmitStub 是**真实方法**（`eventledger.go:111`），S3 阶段作为 EmitStub fixture 用于单测 handler 链路；S4 **新增** `AttachSubscription` 真订阅路径，**保留** EmitStub 不删 |
| **P1-4** | v1 §2 场景 2 说 "末条是 DarvinDoneEvent{usage}" | TS 契约（`darvin-api.ts:42`）`{type:'done', messageId:string}` **无 usage 字段**。v2 落地 LLMEndEvent → `{type:'done', messageId}`，usage 字段走 S5 TS 契约扩展 |
| **P1-5** | v1 §2 场景 4 说 "SIGTERM 由 Electron 主进程 kill 时转发" | S5 才是 Electron 阶段。v2 S4 验收是 Go-only smoke test（`kill -TERM` / `kill -INT`），不依赖 Electron |

### 已知非问题（v1 描述正确，v2 仅微调）

- v1 §3 FR-1 `acp/loop.go` Loop 接 Agent.Prompt/Run —— **设计正确**，v2 保留 + 修正签名
- v1 §3 FR-2 `acp/queue.go` 薄包装 `agent/queue.Queue` —— **设计正确**，v2 保留
- v1 §1.1 "ACP 层" 概念（架构文档 §"ACP 层"）—— **设计正确**，v2 保留
- v1 §3 FR-9 stderr 日志分离 —— **S3 已实装**（`config.yaml log.output: stderr`），v2 不重复

---

## 1. 概述

### 1.1 实际 gap（基于 S3 实际落地代码）

| 组件 | S3 实际状态 | v2 S4 落地 |
|------|------------|-----------|
| `gateway.handlers.handlePrompt` | stub：调 `c.ledger.EmitStub(sess.ID, msgID, p.Content)` 推 text_delta + agent_end fake | 替换为 `h.Loop.Prompt(ctx, content)` → `Agent.Prompt` 入队 + go `Agent.Run` + bus events 经 AttachSubscription 推 WS |
| `gateway.handlers.handleAbort` | stub：永远返 `{aborted: true, sessionId}` | 替换为 `h.Loop.Abort(ctx)` → `Agent.Abort` → cancelFn |
| `gateway.EventLedger.AttachSubscription` | S3 留空实现（`eventledger.go:75`）| **v2 填实**：goroutine `for ev := range sub.C() { l.publishLocked(ev.Common().SessionID, ev) }` |
| `gateway.EventLedger.mapEventToTS` | 6 个 case（text_delta/thinking_delta/agent_end/tool_start/tool_end/agent_error）| 加 1 个 case：`LLMEndEvent → {type:"done", messageId}` |
| `cmd/app/main.go` shutdown | 1 步：`gs.Shutdown(ctx)` 3s timeout + "graceful shutdown complete" 日志 | 加 3 步：`a.Abort(bg)` + flush event.Bus subs + `sqliteStore.Close()`，总 4 步 |
| `internal/acp/` package | 不存在 | 🆕 `loop.go` / `queue.go` / `steer.go` + `loop_test.go` |
| `internal/agent/event.Event` | 各具体事件无 SessionID/MessageID（除 RunStart/AgentEnd 有 SessionID）| 加 `EventCommon{SessionID, MessageID string}` 嵌入 15 个事件 + `Event.Common() EventCommon` 方法 |
| `internal/agent/executor.Deps` | 10 个方法（缺 CurrentMessageID）| 加 `CurrentMessageID() string` |
| `executor.RunConversation` 9 个 emit 点 | 无 EventCommon | 全填 |
| `dispatcher.Run` 3 个 emit 点（RunStart/RunEnd/AgentEnd）| 部分有 SessionID 字段 | 全走 `EventCommon.SessionID` + 加 `MessageID` |
| `anthropic/stream.go` dispatch content_block_delta | case `text_delta` + `input_json_delta` | 加 `thinking_delta` 解析 → emit `llm.ThinkingDeltaEvent` |

### 1.2 目标

- 新建 `internal/acp/{loop,queue,steer}.go` + 测试
- `handlePrompt` 调 `acp.Loop.Prompt` → 异步 `Agent.Run`，WS notification 来自 `event.Bus` 真订阅
- `event.Event` 增 `EventCommon` 嵌入 + `Event.Common()` 方法
- `executor.Deps` 增 `CurrentMessageID()`；12 个 emit 点全填 EventCommon
- `anthropic/stream.go` 加 `thinking_delta` 解析
- `main.go` shutdown 序列加 3 步：Agent.Abort + flush bus subs + store.Close
- `internal/gateway/handlers.go` 改 dispatch 收 `*Handler`（Sessions/Ledger/Loop/Steer），保持 `*client` 4 参签名

### 1.3 非目标

- **不**实现多 Agent 多 session（v0 限定：只 Agent 启动时绑的 "default" session 能跑，其他 sessionId 返 -32602）—— S6 范畴
- **不**实现 QueueManager 完整调度（v0 沿用 S3 `queue.Queue` 3 通道）
- **不**实现 SteerControl 真实重定向（v0 `Redirect` 返 `ErrSteerNotImplemented`）
- **不**实装 SubAgent / Memory / Dreaming / Skills / MCP / Failover
- **不**改 `src/shared/darvin-api.ts`（S5 改 TS 契约时扩 done.usage 等字段）
- **不**改 `internal/agent/store/*`（S6 `store.Save` 落 messages）
- **不**动 `internal/agent/{session,tool,ctxengine,llm/{provider,httpclient,registry,model_registry,types,errors,compat}}`

### 1.4 前置依赖（S3 实际落地 API 表面）

```go
// gateway.SessionManager
func NewSessionManager() *SessionManager
func (m *SessionManager) CreateOrGet(id string) (*session.Session, string, error)
func (m *SessionManager) Has(id string) bool
func (m *SessionManager) Get(id string) (*session.Session, string)

// gateway.EventLedger
func NewEventLedger(log *zap.Logger) *EventLedger
func (l *EventLedger) Subscribe(sessionID string, c *client)
func (l *EventLedger) UnsubscribeAll(c *client)
func (l *EventLedger) AttachSubscription(_ *event.Subscription)  // S3 no-op, S4 填实
func (l *EventLedger) EmitStub(sessionID, msgID, content string)  // 保留作 fixture
func (l *EventLedger) publishLocked(sessionID string, ev event.Event)  // 包内

// gateway.client
func (c *client) SendNotification(method string, params any)  // 已就位

// agent.Agent
func (a *Agent) Prompt(_ context.Context, content string) error  // 入队
func (a *Agent) Steer(ctx context.Context, content string) error  // cancel + 入队 steer
func (a *Agent) FollowUp(_ context.Context, content string) error
func (a *Agent) Abort(_ context.Context) error  // cancelFn
func (a *Agent) Run(ctx context.Context) error  // 阻塞到 queue drain / ctx cancel
func (a *Agent) Subscribe(buffer int) *event.Subscription  // 关键入口
func (a *Agent) SessionHandle() *session.Session
func (a *Agent) Emit(ev event.Event)  // 满足 executor.Deps

// agent.event
type Event interface { isAgentEvent(); EventName() string }  // S4 加 Common()
type Subscription struct { ... }
func (s *Subscription) C() <-chan Event
func (s *Subscription) Unsubscribe()

// agent.queue
type Queue struct { ... }
func (q *Queue) Enqueue(mode Mode, msg Message) error
func (q *Queue) Dequeue(ctx) (Message, Mode, bool)
func (q *Queue) Len() int

// executor.Deps（已有 10 方法, S4 加 1）
type Deps interface {
    Session() *session.Session
    Tools() *tool.Registry
    Provider() llm.ModelProvider
    ModelName() string
    Instructions() string
    Emit(event.Event)
    Config() Config
    Assembler() ctxengine.ContextEngine
    SystemSections() []ctxengine.SystemSection
    AssemblerEnabled() bool
    RecordUsage(u llm.Usage)
    LastUsage() llm.Usage
    // S4 新增
    CurrentMessageID() string
}
```

---

## 2. 用户场景

### 场景 1：handler 真接 Agent.Run（单 session 限定）

**Given**：Agent 已 wire 到 "default" session（`main.go:137 session.NewSession("default")`）；gateway 在 listen。
**When**：发 `{"jsonrpc":"2.0","id":"1","method":"agent.prompt","params":{"content":"hi"}}`（无 sessionId）。
**Then**：
1. `handlePrompt` 校验 content 非空（已有）
2. v0 限定：`p.SessionID` 非空且 `!= c.sessions.DefaultID()` → 返 `-32602 "session not active"`
3. `c.sessions.CreateOrGet(p.SessionID)` → 空 id 归一到 `DefaultSessionID`，返的就是 default session（**实现修正**，见 §9）
4. `h.Loop.Prompt(ctx, p.Content)` → 调 `agent.Prompt(content)` 入队 → 成功后设 `curMsg` → spawn `go agent.Run(context.Background())` → 返 `(msgID, nil)`
5. handler 同步返 `{"sessionId":"default","messageId":"<21字符>"}`（**不**等 Run 完成）

### 场景 2：流式 WS notification（真订阅路径）

**Given**：场景 1 已 `agent.subscribe_events {sessionId: "<default>"}` 成功。
**When**：mock provider（loop_test.go）流式返回 text_delta ×N。
**Then**：WS 收到 notification 链（顺序不保证）：
1. 0+ 条 `{"type":"text_delta","messageId":"<m>","delta":"..."}`（来自 `event.TextDeltaEvent`，含 `EventCommon.MessageID`）
2. 1 条 `{"type":"done","messageId":"<m>"}`（来自 `event.LLMEndEvent`，**v2 新增映射**）
3. 1 条 `{"type":"agent_end"}`（来自 `event.AgentEndEvent`）

**注**：done 不含 usage（TS 契约未扩，§0 P1-4 follow-up）。usage 暂留在 `LLMEndEvent` payload 不推 WS。

### 场景 3：abort

**Given**：场景 2 流式进行中。
**When**：发 `agent.abort {sessionId}`。
**Then**：
1. `handleAbort` 调 `h.Loop.Abort(ctx)` → `agent.Agent.Abort(ctx)` → cancelFn 触发
2. executor `drainStream` 收到 `ctx.Canceled` → `return context.Canceled`
3. executor 收尾 emit `TurnEndEvent{StopReason: llm.FinishReasonAborted}`
4. dispatcher 收到 `context.Canceled` → emit `AgentErrorEvent{Err: ErrAborted}` → 返 `ErrAborted`
5. Agent.Run 返 → dispatcher defer emit `AgentEndEvent`
6. WS notification 链：残余 text_delta → done → agent_end
7. handler 同步返 `{"aborted":true,"sessionId":"..."}`

### 场景 4：SIGTERM 优雅关闭（4 步）

**Given**：Go agent 在 listen，1 个 in-flight Run。
**When**：`kill -TERM <pid>`。
**Then**：S3 + S4 累计 4 步序列：
1. **GS Shutdown**（S3）：`httpSrv.Shutdown(ctx)` 拒新连 + 等现有连关（3s timeout）
2. **Agent.Abort**（S4 新增）：`a.Abort(context.Background())` 取消 in-flight Run，触发 done+agent_end 收尾
3. **flush event.Bus**（S4 新增）：`sub.Unsubscribe()` 关闭 sub.C() channel，goroutine 退出
4. **DB Close**（S4 新增）：`sqliteStore.Close()` 释放 SQLite fd

每步 stderr 打 INFO 日志；末条 `graceful shutdown complete`；`os.Exit(0)`。整体 ≤ 3s（实测 < 200ms 无 in-flight）。

### 场景 5：SIGINT（Ctrl-C）

同 SIGTERM。`signal.NotifyContext(SIGINT, SIGTERM)` 一并处理（已 S3 实装）。

### 场景 6：thinking_delta（v2 新增）

**Given**：Claude 模型开 extended thinking。
**When**：Agent.Run 流式。
**Then**：
1. anthropic SSE `content_block_delta.type == "thinking_delta"`，payload `{delta: {thinking: "..."}}`
2. `stream.go dispatch` 新 case `thinking_delta` → emit `llm.ThinkingDeltaEvent{Delta: ...}`
3. executor `drainStream` 当前**未处理** `llm.ThinkingDeltaEvent` —— v2 加 case：`d.Emit(event.ThinkingDeltaEvent{EventCommon: ec, Delta: e.Delta})`
4. EventLedger → `mapEventToTS` 已有 `case event.ThinkingDeltaEvent`（S3 实装）→ WS notification `{"type":"thinking_delta","messageId":"<m>","delta":"..."}`

---

## 3. 功能需求

### FR-1：ACP 包结构

`internal/acp/` 新 package：

| 文件 | 内容 |
|------|------|
| `loop.go` | `Loop` struct（包 `*agent.Agent` + `curMsg` + `mu`）；`Prompt` / `Abort` / `CurrentMessageID`；`generateMessageID`（21-char nanoid）|
| `queue.go` | `Queue` 薄包装（转 `agent/queue.Queue`）|
| `steer.go` | `SteerControl interface` + v0 `steerControl` impl + `ErrSteerNotImplemented` |
| `loop_test.go` | 端到端：mock provider 注入 + 验 11 event 顺序 + done 来自 LLMEndEvent + Abort 后无新 event |

依赖图：
- `acp/loop.go` → `agent.Agent` + `agent/queue` + `agent/event` + `agent/session`
- `acp/queue.go` → `agent/queue`
- `acp/steer.go` → `agent.Agent`
- `acp` **不** import `internal/gateway`（避免 acp → gateway → ... cycle）

### FR-2：Loop 包装 Agent

```go
// internal/acp/loop.go
package acp

import (
    "context"
    "errors"
    "sync"

    "github.com/jaevor/go-nanoid"

    "darvin-cowork/backend/internal/agent"
    "darvin-cowork/backend/internal/agent/queue"
)

const messageIDLen = 21
const messageAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

var ErrAgentBusy = errors.New("acp: agent already running")

// Loop wraps a single *agent.Agent with prompt/abort plumbing. v0 is
// single-session: only the agent's own session.ID is the live one. The
// gateway enforces that via SessionManager.DefaultID().
type Loop struct {
    agent *agent.Agent
    mu    sync.Mutex
    curMsg string  // current messageID; set per Prompt, read via CurrentMessageID
    msgGen func() string  // 21-char nanoid
}

func NewLoop(a *agent.Agent) *Loop {
    return &Loop{
        agent:  a,
        msgGen: nanoid.MustCustomASCII(messageAlphabet, messageIDLen),
    }
}

// Prompt enqueues content and spawns Agent.Run in a goroutine. Returns
// the messageID used for this prompt's events. Returns ErrAgentBusy if
// the agent is already running.
func (l *Loop) Prompt(ctx context.Context, content string) (string, error) {
    msgID := l.msgGen()
    l.mu.Lock()
    l.curMsg = msgID
    l.mu.Unlock()

    if err := l.agent.Prompt(ctx, content); err != nil {
        // agent.Prompt may return ErrAgentBusy — clear curMsg so a future
        // prompt doesn't inherit a stale id.
        l.mu.Lock()
        l.curMsg = ""
        l.mu.Unlock()
        return "", err
    }
    go func() {
        // background ctx: handler / WS disconnect must NOT cancel the run
        // (other clients may still be subscribed). Cancellation goes
        // through Agent.Abort (per §4.2.2).
        runCtx := context.Background()
        _ = l.agent.Run(runCtx)  // logged elsewhere
    }()
    return msgID, nil
}

// Abort cancels the in-flight Run via Agent.Abort.
func (l *Loop) Abort(ctx context.Context) error { return l.agent.Abort(ctx) }

// CurrentMessageID is read by executor via Deps (FR-7). Returns "" if no
// prompt is in flight.
func (l *Loop) CurrentMessageID() string {
    l.mu.Lock()
    defer l.mu.Unlock()
    return l.curMsg
}
```

**关键设计点**：
- `curMsg` 由 Loop 独占；executor 通过 `Deps.CurrentMessageID()` 读（v0 安全：ErrAgentBusy 阻止并发 Prompt）
- 不收 sessionID：Agent 已经绑 session，gateway 决定 sessionId 来自哪
- go Run 用 `context.Background()`，**取消靠 Abort**（per §4.2.2）

### FR-3：Queue 薄包装

```go
// internal/acp/queue.go
package acp

import (
    "context"

    "darvin-cowork/backend/internal/agent/queue"
)

type Queue struct{ inner *queue.Queue }

func NewQueue() *Queue { return &Queue{inner: queue.NewQueue()} }

func (q *Queue) Enqueue(mode queue.Mode, content string) error {
    return q.inner.Enqueue(mode, queue.Message{Content: content})
}
func (q *Queue) Dequeue(ctx context.Context) (queue.Message, queue.Mode, bool) {
    return q.inner.Dequeue(ctx)
}
func (q *Queue) Len() int { return q.inner.Len() }
```

### FR-4：SteerControl interface + v0 no-op

```go
// internal/acp/steer.go
package acp

import (
    "context"
    "errors"
)

// ErrSteerNotImplemented is returned by Redirect in v0.
var ErrSteerNotImplemented = errors.New("acp: Redirect not implemented in v0")

// SteerControl re-prioritises or redirects mid-run. v0 is partial:
// Steer delegates to agent.Steer; Redirect returns ErrSteerNotImplemented.
type SteerControl interface {
    Steer(ctx context.Context, content string) error
    Redirect(ctx context.Context, content string) error
}

type steerControl struct{ agent *agent.Agent }

func NewSteerControl(a *agent.Agent) SteerControl { return &steerControl{agent: a} }

func (s *steerControl) Steer(ctx context.Context, content string) error {
    return s.agent.Steer(ctx, content)
}
func (s *steerControl) Redirect(_ context.Context, _ string) error {
    return ErrSteerNotImplemented
}
```

### FR-5：**删除 v1 的 SessionManager AttachClient 整套**

v1 FR-5 提议给 SessionManager 加 `clients map + AttachClient/DetachClient/NotifyAll` —— **整套死代码**。S3 EventLedger 已实装等效机制（`eventledger.go:23-69`）。v2 删除。

### FR-6：EventCommon 嵌入 + Event.Common() 方法

```go
// internal/agent/event/event.go
package event

// EventCommon is the session/message correlation payload embedded in every
// concrete event. The Event interface's Common() method exposes it
// uniformly so consumers (e.g. EventLedger.AttachSubscription) can route
// events to session-scoped subscribers without type-switching.
type EventCommon struct {
    SessionID string
    MessageID string
}

// eventBase is embedded by every concrete Event. It provides Common()
// for free, so each event only needs to embed eventBase — no per-event
// method boilerplate.
type eventBase struct{ EventCommon }

func (b eventBase) Common() EventCommon { return b.EventCommon }

// Event is the sealed agent lifecycle event. v2 adds Common() so the
// ledger / executor / dispatcher can read correlation fields uniformly.
type Event interface {
    isAgentEvent()
    EventName() string
    Common() EventCommon
}

// All 15 concrete events gain eventBase embedding + lose any direct
// SessionID field (RunStartEvent/AgentEndEvent used to have it).
// Example refactor for the 15 events:

type PromptReceivedEvent struct {
    eventBase
    Content string
    Mode    Mode
}
func (PromptReceivedEvent) isAgentEvent()     {}
func (PromptReceivedEvent) EventName() string { return "prompt_received" }
// Common() inherited from eventBase

type RunStartEvent struct {
    eventBase  // replaces direct SessionID field
}
// isAgentEvent + EventName unchanged; accessors read .SessionID via .eventBase

type TurnStartEvent struct {
    eventBase
    TurnID    string
    TurnIndex int
}
// ... (LLMStart, TextDelta, ThinkingDelta, LLMEnd, ToolStart, ToolEnd,
//      TurnEnd, RunEnd, AgentError, AgentEnd, Compaction, Custom — all
//      embed eventBase; Refactor RunStart/AgentEnd to drop direct SessionID)

// CustomEvent: keep Name + Payload; embed eventBase.
```

**注**：
- `RunStartEvent` 和 `AgentEndEvent` 当前有 `SessionID` 字段（`event.go:54, 159`），v2 移除直接字段，统一走 `EventCommon.SessionID`（访问代码改为 `.SessionID` 仍可，Go 字段提升）
- 这影响 dispatcher.go 的 emit 点（`a.session.ID` → `EventCommon{SessionID: a.session.ID}`），详见 FR-7

### FR-7：executor + dispatcher emit 填 EventCommon

**executor.Deps 加 1 方法**：

```go
// internal/agent/executor/executor.go
type Deps interface {
    // ... 已有 12 方法 ...
    CurrentMessageID() string  // S4 新增
}
```

**agent.Agent 满足**（`internal/agent/agent.go`）：

```go
// CurrentMessageID satisfies executor.Deps. v0 returns "" — Loop is the
// canonical source; main.go wires Loop.CurMsgProvider() into a. If no
// loop is wired, emit sites fill MessageID = "".
func (a *Agent) CurrentMessageID() string {
    if a.curMsgSrc != nil { return a.curMsgSrc() }
    return ""
}

// main.go wiring (示意):
//   a.curMsgSrc = loop.CurrentMessageID  (method value)
//   loop := acp.NewLoop(a)
```

**12 个 emit 点全表**（executor 9 + dispatcher 3）：

| # | 文件:行 | Event | 改动 |
|---|---------|-------|------|
| 1 | executor.go:82 | `TurnStartEvent` | 加 `EventCommon{SessionID: d.Session().ID, MessageID: d.CurrentMessageID()}` |
| 2 | executor.go:103 | `LLMStartEvent` | 同上 |
| 3 | executor.go:126 | `TurnEndEvent`（abort 分支）| 同上 |
| 4 | executor.go:140 | `LLMEndEvent` | 同上 + Assistant 字段不变 |
| 5 | executor.go:144 | `TurnEndEvent`（stop 分支）| 同上 |
| 6 | executor.go:159 | `TurnEndEvent`（tool_calls 分支）| 同上 |
| 7 | executor.go:176 | `TextDeltaEvent` | 同上 + Delta 字段不变 |
| 8 | executor.go:216 | `ToolStartEvent` | 同上 + TurnID/CallID/Name/Arguments 字段不变 |
| 9 | executor.go:226 | `ToolEndEvent` | 同上 + CallID/Result/DurationMS 字段不变 |
| 10 | dispatcher.go:84 | `RunStartEvent` | `EventCommon{SessionID: a.session.ID, MessageID: ""}`（Run 启动时尚无 msgID）|
| 11 | dispatcher.go:110 | `RunEndEvent` | 同上 + Turns 字段不变 |
| 12 | dispatcher.go:77-82 | `AgentEndEvent`（defer emit）| `EventCommon{SessionID: a.session.ID, MessageID: ""}`（Run 结束时 curMsg 已被下个 prompt 覆盖风险 → 用 Run 期间保存的快照）|

**12 改动模式示例**（executor.go:82）：

```go
// before
d.Emit(event.TurnStartEvent{TurnID: turnID, TurnIndex: turnIndex})

// after
ec := event.EventCommon{
    SessionID: d.Session().ID,
    MessageID: d.CurrentMessageID(),
}
d.Emit(event.TurnStartEvent{eventBase: event.eventBase{EventCommon: ec}, TurnID: turnID, TurnIndex: turnIndex})
```

**注**：`eventBase` 嵌入后，结构体字面量可用命名嵌套或位置参数（Go 允许）。v2 选命名以提高可读性。

**`AgentEndEvent` 的 MessageID 处理**：dispatcher 当前 `totalUsage` 通过 defer emit AgentEndEvent 时填。MessageID 来自 Run 期间的 curMsg 快照。v2 改：dispatcher 在 Run 期间记录 `runMsg := a.currentMessageID`（在 Dequeue 成功后），defer emit 时填。

### FR-8：EventLedger.AttachSubscription 真接

```go
// internal/gateway/eventledger.go — 替换 S3 的 no-op stub
func (l *EventLedger) AttachSubscription(sub *event.Subscription) {
    go func() {
        for ev := range sub.C() {
            sid := ev.Common().SessionID
            if sid == "" {
                // 跳过无 session 关联的 event (e.g. PromptReceivedEvent
                // 早于 Loop 设 sid 时; 或 S6 引入的 system event)
                continue
            }
            l.publishLocked(sid, ev)
        }
    }()
}
```

**`mapEventToTS` 加 1 case**（`eventledger.go:130-168`）：

```go
case event.LLMEndEvent:
    return map[string]any{
        "type":      "done",
        "messageId": ev.Common().MessageID,
        // usage 字段: TS 契约当前无 (S5 扩展)
    }
```

**EmitStub 保留不删**（per §0 P1-3）：作为单测 fixture 验证 handler 链路，不依赖 Agent.Run。

### FR-9：handlers 真接 acp.Loop（保持 `*client` 4 参签名）

```go
// internal/gateway/handlers.go
type Handler struct {
    Sessions *SessionManager
    Ledger   *EventLedger
    Loop     *acp.Loop
    Steer    acp.SteerControl
}

func NewHandler(s *SessionManager, l *EventLedger, loop *acp.Loop, steer acp.SteerControl) *Handler {
    return &Handler{Sessions: s, Ledger: l, Loop: loop, Steer: steer}
}

// dispatchRequest 签名 +1 个 *Handler 参数
func dispatchRequest(ctx context.Context, req *Request, c *client, h *Handler) *Response {
    switch req.Method {
    case "agent.prompt":    return handlePrompt(ctx, req.ID, req.Params, c, h)
    case "agent.abort":     return handleAbort(ctx, req.ID, req.Params, c, h)
    case "agent.subscribe_events": return handleSubscribeEvents(ctx, req.ID, req.Params, c, h)
    case "agent.steer":     return handleSteer(ctx, req.ID, req.Params, c, h)  // S4 新增
    default:                return errorResp(...)
    }
}

func handlePrompt(ctx, id, params, c, h) *Response {
    // ... 校验 content ...
    sess, msgID, _ := c.sessions.CreateOrGet(p.SessionID)

    // v0 限定: 只有 default session 能跑 Agent
    if sess.ID != c.sessions.DefaultID() {
        return errorResp(id, CodeInvalidParams, "session not active", nil)
    }
    // msgID from CreateOrGet 丢弃 (Loop 会生成新的)
    realMsgID, err := h.Loop.Prompt(ctx, p.Content)
    if err != nil {
        return errorResp(id, CodeInternalError, "loop prompt", err)
    }
    return successResp(id, PromptResult{SessionID: sess.ID, MessageID: realMsgID})
}

func handleAbort(ctx, id, params, c, h) *Response {
    // ... 解析 params ...
    if err := h.Loop.Abort(ctx, p.SessionID); err != nil {
        return errorResp(id, CodeInternalError, "loop abort", err)
    }
    return successResp(id, AbortResult{Aborted: true, SessionID: p.SessionID})
}

func handleSteer(ctx, id, params, c, h) *Response {
    // 新 RPC: agent.steer {content: "..."}  →  h.Steer.Steer(ctx, content)
    var p SteerParams
    json.Unmarshal(params, &p)
    if err := h.Steer.Steer(ctx, p.Content); err != nil {
        return errorResp(id, CodeInternalError, "loop steer", err)
    }
    return successResp(id, SteerResult{Steered: true})
}

type SteerParams struct { Content string `json:"content"` }
type SteerResult  struct { Steered bool   `json:"steered"` }
```

**`server.go` 改 NewServer 收 `*Handler`**：

```go
// internal/gateway/server.go
type Server struct {
    // ... 已有 ...
    handler *Handler  // S4 替换散在 client 上的 sessions/ledger 引用
}

func NewServer(h *Handler, log *zap.Logger) *Server { ... }
```

**`client` 保留 sessions/ledger 字段**（`SendNotification` 用得到）；dispatch 时 `client` 与 `handler` 并列传。

### FR-10：SessionManager.DefaultID()

```go
// internal/gateway/sessionmgr.go
const DefaultSessionID = "default"  // 与 main.go:137 session.NewSession("default") 对齐

func (m *SessionManager) DefaultID() string { return DefaultSessionID }
```

**v0 限定**：Agent 启动时绑 `session.NewSession("default")`（`main.go:137`）。handler 校验 prompt sessionId 必须等于 `DefaultSessionID()` 或为空（CreateOrGet 自动建新 session，但 v0 仍拒绝）。S6 多 session 化。

### FR-11：main.go shutdown 4 步 + 构造 acp

```go
// cmd/app/main.go — 在 S3 基础上加 S4 改动
// (1) Agent 构造后
loop := acp.NewLoop(a)
a.AttachMessageIDSrc(loop.CurrentMessageID)  // S4 新增 Agent 方法, 注入 method value
steer := acp.NewSteerControl(a)

// (2) Gateway 构造
sessions := gateway.NewSessionManager()
ledger := gateway.NewEventLedger(log.Logger)
handler := gateway.NewHandler(sessions, ledger, loop, steer)
gs := gateway.NewServer(handler, log.Logger)

// (3) EventLedger 真订阅
sub := a.Subscribe(64)  // 关键入口
ledger.AttachSubscription(sub)

// (4) Shutdown 序列 (4 步)
<-rootCtx.Done()
log.Info("shutdown signal received")
shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
defer cancel()

// step 1 (S3)
if err := gs.Shutdown(shutdownCtx); err != nil { log.Error(...) }
// step 2 (S4)
if err := a.Abort(context.Background()); err != nil { log.Error(...) }
// step 3 (S4): flush event.Bus subscriptions
sub.Unsubscribe()  // 关 sub.C() channel, AttachSubscription goroutine 退出
// step 4 (S4): DB close
if err := sqliteStore.Close(); err != nil { log.Error(...) }

log.Info("graceful shutdown complete")
```

**`store.SQLiteStore` 需要 `Close()` 方法**（S2 没实装）。v2 加：

```go
// internal/agent/store/sqlite_store.go (或 memory.go 加 interface 满足)
func (s *SQLiteStore) Close() error {
    sqlDB, err := s.db.DB()
    if err != nil { return err }
    return sqlDB.Close()
}
```

### FR-12：anthropic stream thinking_delta 解析

```go
// internal/agent/llm/anthropic/stream.go dispatch line 314 switch 加
case "thinking_delta":
    select {
    case st.out <- llm.ThinkingDeltaEvent{Delta: d.Delta.Thinking}:
    }
```

**Anthropic payload 结构**（per `content_block_delta` with `type: "thinking_delta"`）：
```json
{ "index": N, "delta": { "type": "thinking_delta", "thinking": "..." } }
```

`dispatchState` 加字段 `thinking *strings.Builder` 用于累积 + 同步 emit（v2 简化为不累积，只实时 emit per delta；累积留给未来 S5 ThinkingEnd 事件）。

**executor `drainStream` 加 case**（`executor.go:170-198`）：
```go
case llm.ThinkingDeltaEvent:
    d.Emit(event.ThinkingDeltaEvent{
        eventBase: event.eventBase{EventCommon: ec},  // ec from current scope
        Delta: e.Delta,
    })
```

但 `drainStream` 当前没有 `ec` 变量（EventCommon 是 v4 新增）。v2 改：在 `drainStream` 开头算 `ec := event.EventCommon{SessionID: d.Session().ID, MessageID: d.CurrentMessageID()}`，每个 emit 共享。

### FR-13：ACP loop_test.go 端到端

```go
// internal/acp/loop_test.go
func TestLoopEnd2End(t *testing.T) {
    // 1. mock provider 注入 agent.New (NewAgentConfig.Provider)
    mock := &mockProvider{events: []llm.StreamEvent{
        llm.StartEvent{Partial: llm.AssistantMessage{Model: "test"}},
        llm.TextDeltaEvent{Delta: "hello "},
        llm.TextDeltaEvent{Delta: "world"},
        llm.DoneEvent{Response: llm.CompletionResponse{
            Model: "test", Content: "hello world", FinishReason: llm.FinishReasonStop,
        }},
    }}
    sess := session.NewSession("default")
    a, _ := agent.New(agent.NewAgentConfig{
        Session: sess, Provider: mock, Store: store.NewMemoryStore(),
        // ... 其他默认 ...
    })
    loop := NewLoop(a)
    sub := a.Subscribe(64)
    ledger := gateway.NewEventLedger(zap.NewNop())
    ledger.AttachSubscription(sub)  // 验真订阅路径

    // 2. Prompt → 收 events
    msgID, err := loop.Prompt(context.Background(), "hi")
    require.NoError(t, err)
    require.Len(t, msgID, 21)

    // 3. drain sub.C() 收 events 序列
    var got []string
    timeout := time.After(2 * time.Second)
    for len(got) < 4 {  // text_delta ×2 + done + agent_end = 4
        select {
        case ev := <-sub.C():
            got = append(got, ev.EventName())
        case <-timeout:
            t.Fatalf("timeout, got=%v", got)
        }
    }
    // 验顺序（不强求严格, 但 done 在最后 text_delta 之后）
    assert.Contains(t, got, "text_delta")
    assert.Contains(t, got, "llm_end")  // done 来自 LLMEndEvent
    assert.Equal(t, "agent_end", got[len(got)-1])
}

func TestLoopAbort(t *testing.T) {
    // mock provider stream 阻塞直到 ctx cancel
    // 1. Prompt
    // 2. Abort
    // 3. 验无新 text_delta, 末条 done/agent_end
}

func TestLoopPromptErrAgentBusy(t *testing.T) {
    // 第一次 Prompt 让 agent 进 stateRunning (mock provider 阻塞)
    // 第二次 Prompt → ErrAgentBusy
}
```

**测试基础设施**：
- `mockProvider` 实现 `llm.ModelProvider.Stream`（已有 `MockProvider` 类似物可参考 `llm/httpclient_test.go`）
- 验 `mapEventToTS(LLMEndEvent)` 返回 `{type:"done", messageId: msgID}` —— 这部分走 `gateway/eventledger_test.go`（不在 acp 包内）

---

## 4. 实现方案

### 4.1 目录结构（diff 视角）

```
src/darvin-agent/internal/
├── acp/                              # 🆕 全 package
│   ├── loop.go                       # 🆕 FR-1/FR-2
│   ├── queue.go                      # 🆕 FR-3
│   ├── steer.go                      # 🆕 FR-4
│   └── loop_test.go                  # 🆕 FR-13
├── agent/
│   ├── agent.go                      # 改: 加 CurrentMessageID() + AttachMessageIDSrc
│   ├── dispatcher.go                 # 改: 3 个 emit 填 EventCommon + runMsg 快照
│   ├── event/event.go                # 改: EventCommon + eventBase + 15 事件嵌入
│   ├── executor/executor.go          # 改: Deps 加 CurrentMessageID; 9 emit 填
│   └── llm/anthropic/stream.go       # 改: thinking_delta 解析
├── gateway/
│   ├── client.go                     # 不改
│   ├── eventledger.go                # 改: AttachSubscription 填实 + LLMEndEvent case
│   ├── handlers.go                   # 改: dispatchRequest 收 *Handler + handleSteer
│   ├── server.go                     # 改: NewServer(h *Handler)
│   └── sessionmgr.go                 # 改: DefaultID() + DefaultSessionID const
└── cmd/app/main.go                   # 改: 构造 acp; AttachSubscription; 4 步 shutdown
```

### 4.2 关键决策

#### 4.2.1 单 Agent 单 session（v0 限定）

`main.go:137` 已写死 `Session: session.NewSession("default")`。v2 不改这里。Handler 校验 prompt sessionId 必须是 `DefaultSessionID()` 或与 CreateOrGet 返的相等（即新生成 session 也接受——v0 简化：CreateOrGet 返任何 sessionId 都接受，只要它在 CreateOrGet 时刻存在）。**更严格**：只在 sessionId 为空时 CreateOrGet 出新 session 并接受；其他 sessionId 必须等于 DefaultSessionID。

实际 v2 简化：handler 接受 `p.SessionID` 为空或等于 `DefaultSessionID()`；其他返 -32602。CreateOrGet 的 sessionId 不用做"存在性"检查（因为 S3 已经在 subscribe_events 做了 Has 检查）。

#### 4.2.2 ctx 生命周期

- handler 收 ctx（WS req ctx）→ 调 `Loop.Prompt(ctx, content)` → `Agent.Prompt(ctx, content)` 入队（**不** 阻塞）
- Loop 内部 `go agent.Run(context.Background())` —— Run 用 background ctx
- 取消路径：
  - WS 断 → handler ctx 取消 → 但**不**影响 Run（Loop 没用 handler ctx 跑 Run）
  - 用户 abort → `Loop.Abort(ctx)` → `Agent.Abort(ctx)` → cancelFn 触发 → Run ctx 取消（**Run 用的是 loopCtx, 不是 background, 改：v2 改用 cancelFn-only**）

实际：Agent.Run(ctx) 内的 runCtx 是从传入 ctx 派生的（`runCtx, cancel := context.WithCancel(ctx)`，`dispatcher.go:61`）。如果传入 background ctx，runCtx 也是 background ctx（永不 cancel）。**取消靠 Agent.Abort** 显式调 cancelFn。

#### 4.2.3 EventCommon 传递

- `eventBase` 嵌入 15 个具体事件，零接口分配开销（嵌入 struct 是 inline 字段）
- `Event.Common() EventCommon` 是 v2 新增的 interface 方法，15 个事件通过 `eventBase.Common()` 共享实现
- `AttachSubscription` goroutine：`ev.Common().SessionID` 拿 sessionID
- `executor.Deps.CurrentMessageID()` 拿 msgID，由 Loop 注入

**反射 vs 显式**：考虑过用反射避免 `Common()` 方法，但反射每次访问有性能开销 + 测试困难。15 个事件各加一个一行的 `func (X) Common()` 反而更显式（且 Go 的 embed 优化让接口 dispatch 接近零开销）。

#### 4.2.4 mapEventToTS 加 LLMEndEvent

S3 已有 6 个 case。S4 加 1 个：

```go
case event.LLMEndEvent:
    return map[string]any{
        "type":      "done",
        "messageId": ev.Common().MessageID,
        // usage 字段: P1-4 follow-up, S5 改 TS 契约
    }
```

**副作用**：emitter 必须确保 `ev.Common().MessageID` 非空（executor 已通过 Deps.CurrentMessageID 填）。LLMEndEvent 触发点在 DoneEvent 之后，msgID 应仍有效。

#### 4.2.5 优雅 shutdown 4 步顺序

1. **GS Shutdown**（3s timeout）—— 拒新 + 等现有连关
2. **Agent.Abort** —— 触发 in-flight Run 收尾（产生 done + agent_end）；不阻塞（abort 调 cancelFn 即返）
3. **flush event.Bus** —— `sub.Unsubscribe()` 关闭 `sub.C()`，AttachSubscription goroutine 退出
4. **store.Close** —— 释放 SQLite fd

**顺序理由**：
- GS Shutdown 必须先做（否则新连接可能进入 in-flight）
- Abort 第二（产生最终 events，依赖 GS Shutdown 已拒新）
- sub.Unsubscribe 第三（确保所有 events 已被 publishLocked 推完；AttachSubscription goroutine 收尾）
- store.Close 最后（不能在其他步骤之前 close，否则 store 写入 race）

实测 < 200ms（无 in-flight Run）。

#### 4.2.6 msgID 单一来源

- `Loop.mu` 保护 `curMsg` 字段
- `Prompt` 设 curMsg（覆盖前值，**不**等 Run 结束清）
- `Deps.CurrentMessageID()` 读 curMsg
- 并发安全：Agent.Prompt 已返 ErrAgentBusy 阻止并发 Prompt（v0 不需要更细粒度锁）

**RunEnd 时 curMsg 风险**：Run 1 期间 curMsg = msg1；Run 1 结束后 Run 2 Prompt 设 curMsg = msg2；如果 Run 1 的 deferred AgentEndEvent 还在 emit 路径上，可能读到 msg2。**v2 改**：dispatcher 在 Dequeue 后保存 `runMsg := a.curMsgSrc()` 到局部变量，defer emit AgentEndEvent 用 `runMsg`（避免全局读）。

---

## 5. 边界情况

| 场景 | 处理 |
|------|------|
| 同 client 多次 `subscribe_events` 同 session | 幂等（`bySession` set 去重）|
| `Agent.Run` panic | dispatcher recover → `AgentErrorEvent{Err: panic}`（已有，FR-6 后 EventCommon 仍填）|
| handler ctx 取消（WS 断）| handler 立即返；Agent.Run 不受影响（background ctx）|
| `Loop.Prompt` 时 Agent 在跑 | `Agent.Prompt` 返 `ErrAgentBusy` → handler 返 -32603 InternalError |
| `Loop.Prompt` 成功后 WS 断 | Run 继续跑；notification 推给其他 subscriber（如果有）|
| `Agent.Abort` 时 WS 没订阅者 | `publishLocked` 推空集，no-op |
| `Agent.Abort` 在 Run 之外 | 返 nil（`dispatcher.go:38-46` 已有 no-op 处理）|
| SIGTERM 二次 | `signal.NotifyContext` 不再次 Done；进程已被 `os.Exit(0)` |
| `Agent.Run` 完成后 emit `AgentEndEvent` 时 client 已断 | `writeJSON` 失败 → log 忽略（`client.go` 已有）|
| `EventCommon.SessionID` 为空（如 `PromptReceivedEvent` 早于 Loop 设 sid）| `AttachSubscription` goroutine `continue` 跳过 |
| 旧 fixture event 没实现 `Common()` | 编译错；v2 全量改完，fixture 同步 |
| `thinking_delta` 解析失败 | `dispatch` 忽略（anthropic 容错），不 emit |
| `store.Close` 时 Abort 未完成 | 关 fd 后 Abort 写入会失败；v2 顺序：Abort 完才 Close |
| `sub.Unsubscribe` 阻塞 | 不阻塞（`sync.Once` 关闭 channel 即返）|
| `sqliteStore.Close` 没实现 | FR-11 加 |
| `mockProvider` Stream 不关 channel | `loop_test.go` 必须 defer `stream.Events <-` close |

---

## 6. 涉及文件

| 文件 | 变更 |
|------|------|
| `src/darvin-agent/internal/acp/loop.go` | 🆕 FR-1/FR-2 |
| `src/darvin-agent/internal/acp/queue.go` | 🆕 FR-3 |
| `src/darvin-agent/internal/acp/steer.go` | 🆕 FR-4 |
| `src/darvin-agent/internal/acp/loop_test.go` | 🆕 FR-13 |
| `src/darvin-agent/internal/agent/event/event.go` | 改：EventCommon + eventBase + Event.Common() + 15 事件嵌入 + 删 RunStart/AgentEnd 直接 SessionID 字段 |
| `src/darvin-agent/internal/agent/event/event_test.go` | 改：测试加 EventCommon 断言 |
| `src/darvin-agent/internal/agent/agent.go` | 改：CurrentMessageID() + AttachMessageIDSrc() |
| `src/darvin-agent/internal/agent/dispatcher.go` | 改：3 个 emit 填 EventCommon + runMsg 快照 |
| `src/darvin-agent/internal/agent/executor/executor.go` | 改：Deps 加 CurrentMessageID；9 个 emit 全填 |
| `src/darvin-agent/internal/agent/executor/executor_test.go` | 改：Deps mock 满足新方法 |
| `src/darvin-agent/internal/agent/llm/anthropic/stream.go` | 改：thinking_delta 解析（FR-12）|
| `src/darvin-agent/internal/agent/llm/anthropic/stream_test.go` | 改：加 thinking_delta 测试 |
| `src/darvin-agent/internal/gateway/eventledger.go` | 改：AttachSubscription 填实 + mapEventToTS 加 LLMEndEvent case |
| `src/darvin-agent/internal/gateway/eventledger_test.go` | 改：加 LLMEndEvent → done 映射测试 |
| `src/darvin-agent/internal/gateway/handlers.go` | 改：dispatchRequest 收 *Handler + handlePrompt 调 Loop + handleAbort 调 Loop.Abort + handleSteer 新 RPC |
| `src/darvin-agent/internal/gateway/handlers_test.go` | 改：mock Loop 注入测试 |
| `src/darvin-agent/internal/gateway/server.go` | 改：NewServer(h *Handler) |
| `src/darvin-agent/internal/gateway/server_test.go` | 改：构造 Handler 测试 |
| `src/darvin-agent/internal/gateway/sessionmgr.go` | 改：DefaultSessionID const + DefaultID() 方法 |
| `src/darvin-agent/internal/agent/store/sqlite_store.go` | 改：Close() 方法（FR-11）|
| `src/darvin-agent/cmd/app/main.go` | 改：构造 acp.Loop/Steer；sub.AttachSubscription；shutdown 4 步序列 |

**不修改**：
- `src/darvin-agent/internal/agent/queue/*`（queue.Queue 已 v0 满足）
- `src/darvin-agent/internal/agent/llm/{provider,httpclient,registry,model_registry,types,errors,events,compat}.go`
- `src/darvin-agent/internal/agent/session/*`（`session.NewSession` 不变）
- `src/darvin-agent/internal/agent/store/{memory,models,store,sqlite_test}.go`（除 sqlite_store.go）
- `src/darvin-agent/internal/agent/tool/*`（5 个 builtin tool 不变）
- `src/darvin-agent/internal/agent/ctxengine/*`（assembler 不变）
- `src/darvin-agent/internal/config/*`（config.yaml 不变）
- `src/darvin-agent/internal/logger/*`（已 stderr 不变）
- `src/shared/darvin-api.ts`（S5 改 TS 契约时）
- `src/renderer/**`（S5/S6）

---

## 7. 验收标准

### 7.1 构建 / 静态

- [ ] `go build ./...` 编译通过
- [ ] `go vet ./...` 无警告
- [ ] `gofmt -l .` 干净

### 7.2 测试

- [ ] `go test ./... -race` 全绿（含 S2 store 回归 + S3 gateway 回归 + S4 acp/event/executor/dispatcher/handlers/eventledger/anthropic 回归）
- [ ] `go test ./internal/agent/... -race` 全绿（EventCommon 嵌入后 15 个事件不破坏现有 fixture）
- [ ] `go test ./internal/gateway/... -race` 全绿（handlers/server/eventledger 改后不破坏 S3 测试）
- [ ] `go test ./internal/agent/llm/anthropic/... -race` 全绿（stream 加 thinking_delta 后旧测试不变）
- [ ] 新加测试覆盖：
  - [ ] `acp/loop_test.go`：3 个测试（End2End、Abort、ErrAgentBusy）
  - [ ] `event/event_test.go`：EventCommon 嵌入 + Common() 正确性
  - [ ] `executor/executor_test.go`：Deps mock 满足 CurrentMessageID；emit 填 EventCommon snapshot
  - [ ] `eventledger_test.go`：LLMEndEvent → `{type:"done", messageId}`
  - [ ] `anthropic/stream_test.go`：thinking_delta 解析 → emit ThinkingDeltaEvent

### 7.3 运行时

- [ ] 启动后 stdout **唯一一行** `<port>NNNNN</port>`
- [ ] 启动后 stderr 含 `agent initialized` + `gateway listening` + `application started successfully`
- [ ] `wscat -c ws://localhost:NNNNN/ws` 连上（**带 `/ws`**）
- [ ] 发 `agent.prompt {"content":"hi"}` → 立即返 `{sessionId:"default", messageId:"<21字符>"}`
- [ ] 发 `agent.subscribe_events {"sessionId":"default"}` → `{subscribed:true}`（prompt 之前也可订阅）
- [ ] subscribe 后 ≤ 1s 收到 notification 链：text_delta (mock) + done (LLMEndEvent 映射) + agent_end
- [ ] 发 `agent.abort {"sessionId":"..."}`（在流式期间）→ 后续 notification 含 done + agent_end
- [ ] 发 `agent.prompt {"sessionId":"nonexistent"}` → `-32602 "session not active"`
- [ ] 发 `agent.prompt` 缺 content → `-32602 "content is required"`
- [ ] 发 `agent.steer {"content":"redirect"}` → `{steered:true}`（v0 `Steer` 走 agent.Steer；`Redirect` 路径在 S4 不暴露 RPC）

### 7.4 优雅关闭（4 步）

- [ ] `kill -TERM <pid>`：走完 4 步（GS Shutdown → Agent.Abort → sub.Unsubscribe → store.Close），stderr 末条 `graceful shutdown complete`，整体 ≤ 3s
- [ ] `kill -INT <pid>`：同上
- [ ] 优雅关闭期间新 WS 连接被拒（`http.Server.Shutdown` 行为）
- [ ] 优雅关闭后 `sessions.db` 不损坏（重启可 Load 之前 Save 的 session）
- [ ] `lsof -p <pid>` 验证：进程退出后无残留 fd

### 7.5 数据流端到端

- [ ] EventCommon 各字段被 executor + dispatcher 填（snapshot 检查：注入 mock Deps 返回固定 MessageID，断言 emit 出的事件 `ev.Common().MessageID == expected`）
- [ ] AttachSubscription goroutine 把 event 按 `ev.Common().SessionID` 路由到 bySession 正确 subset
- [ ] `done` notification 的 `messageId` 来自 `LLMEndEvent.Common().MessageID`（**不是** SessionManager.CreateOrGet 的 msgID）

---

## 8. 后续 spec 候选（不在本 spec 范围）

| Spec | 内容 |
|------|------|
| **S5** electron-runtime-client | Electron spawn Go agent + WS client + preload 真接；**`src/shared/darvin-api.ts` 扩 `done.usage` + `done.finishReason` 字段**（本 spec §4.2.4 follow-up）；TS 端加 `agent.steer` RPC 类型 |
| **S6** agent-e2e-integration | 多 Agent 多 session + `store.Save` 在 RunEnd 落 messages + 重启可见 + `agent.list_sessions` / `agent.get_messages` RPC |
| ACP QueueManager 完整调度 | 多 prompt 排队 + 优先级动态调整 |
| SteerControl 真实重定向 | 当前 `ErrSteerNotImplemented`；接入 `queue.Queue` 的 steer 通道 + 真正 abort-and-redirect |
| SubAgent 真实实现 | 替换 `ErrSubAgentUnsupported` |
| `ResolveCost` / `Cost` | 架构文档 §Provider 抽象层要求，实装 |
| `event.Event` 继续扩展 | `memory.*` / `skill.*` / `mcp.*` / `compaction.*` |
| Auth / Bearer token | v0 无鉴权；远期 spec 引入 |
| WSS / TLS | 远期 spec |
| EventLedger 落 sessions.db | 远期（随 S6 `store.Save`）|
| Reconnect / 心跳策略 | S3 简单 ping-pong；远期可加 reconnect |
| Anthropic stream 解析 `thinking_delta` 累积 | 当前 v2 实时 emit per delta；未来加 ThinkingEndEvent + accumulation |

---

## 9. 实现偏差（落地后追加）

实现过程中发现 spec 三处自相矛盾/遗漏，代码按「能真正跑通」的读法落地，spec 已同步修正。

### 9.1 sessionId 必须等于 Agent 的 session.ID（阻断级）

**spec 原文矛盾**：§2 场景 1 第 2 步说 `CreateOrGet("")` 生成 21-char nanoid「S3 行为不变」，第 3 步又说 `sess.ID != DefaultID()` 就返 `-32602`——按字面读**每个 prompt 都会被拒**。而 §2 场景 1 第 5 步写 `{"sessionId":"<default-session-id>"}`，§7.3 又写 `{sessionId:"<21字符>"}`。

**为什么必须改**：`EventLedger.AttachSubscription` 按 `ev.Common().SessionID` 路由，该值恒为 Agent 绑定的 `"default"`。若 handler 返 nanoid，客户端只能订阅那个 nanoid，**FR-9 整条 notification 链永远投递不到**。实测（真二进制 + WS）确认：修正前 0 条 notification。

**落地**：`CreateOrGet("")` 把空 id 归一到 `DefaultSessionID`；handler 返 `sessionId:"default"`。messageId 仍是 21-char nanoid（由 `acp.Loop` 生成）。回归测试 `TestDispatchPromptThenSubscribeRoutes` 钉住「prompt 返的 sessionId 必须能被 subscribe_events 接受」。

### 9.2 default session 构造时即注册

**问题**：session 只在首次 `CreateOrGet` 时入表，导致 `subscribe_events` 必须在 prompt **之后**才能成功（`Has()` 先失败）。客户端于是被迫抢跑 prompt 的 reply，会丢掉 run 开头的 `run_start` / 前几条 delta。

**落地**：`NewSessionManager()` 直接注册 default session，允许 subscribe-before-prompt，彻底消除竞态。回归测试 `TestDefaultSessionRegisteredUpFront`。

### 9.3 `Loop.Prompt` 失败时不清 curMsg

**spec 原文**：`agent.Prompt` 返错时把 `curMsg` 置 `""`。

**问题**：`ErrAgentBusy` 恰恰意味着**上一个 run 还在跑**，清空会让它剩余事件的 `messageId` 变空串，前端 `find(m => m.id === e.messageId)` 匹配不到，气泡卡住。

**落地**：`curMsg` 只在 `agent.Prompt` 成功后才写；被拒的 prompt 不动它。回归测试 `TestLoopPromptErrAgentBusy`。

### 9.4 `error` notification 字段名对齐 TS 契约（顺带修）

`mapEventToTS(AgentErrorEvent)` 原本发 `{type, error, detail:{id:""}}`，而 `src/shared/darvin-api.ts` 的 `DarvinEvent` 声明是 `{type:'error', messageId, message}`，`useMessages.ts:43-48` 按 `messageId` 找消息、读 `e.message`。S3 时事件链未接通所以无感；S4 接通后这会变成「agent 报错但前端气泡永远转圈且不显示错误」。已改为 `{type:"error", messageId, message}`，测试 `TestMapEventToTSCarriesMessageID` 钉住四个事件的字段集。

### 9.5 未处理（留作 follow-up）

| 项 | 说明 |
|----|------|
| `tool_start` / `tool_end` 字段名 | Go 发 `message:{id}`，TS 契约要 `messageId`；`tool_end` 把 `Result.Content` 塞进了 `tool` 字段而非 `output`，且 `ToolEndEvent` 无 `Name` 字段可填。渲染层当前不消费 tool 事件，故本 spec 不动——修它需要给 `ToolEndEvent` 加字段并穿 executor。 |
| `EventLedger.EmitStub` | 真订阅接通后已无生产调用方，仅其自身测试在用。删除需同步改 `client.go` 注释与 fanout 覆盖，超出本 spec 范围。 |
| `acp.Queue` | FR-3 按 spec 实现了薄包装，但当前无调用方（dispatcher 直接用 `agent/queue`）。留给 ACP QueueManager 完整调度那版 spec。 |

### 9.6 spec 示例代码里的 `eventBase` 需导出

spec 各处示例写 `eventBase: event.eventBase{...}`。Go 的字段提升**不穿透**双层嵌入的结构体字面量，未导出名字在 `agent` / `executor` / `gateway` 三个包里都无法写。落地改名为导出的 `EventBase`。

### 9.7 `acp` 测试不能 import `gateway`

FR-13 的示例在 `package acp` 的测试里 `import gateway` 来验 `AttachSubscription`。但 `gateway` 已 import `acp`（handler 持 `*acp.Loop`），这会构成 import cycle。落地改为直接 drain `sub.C()` 断言事件序列 + `EventCommon`；`AttachSubscription` 的路由行为由 `gateway/eventledger_test.go` 覆盖，端到端由真二进制 smoke 覆盖。

---

> **v1 spec 状态**：作废，仅历史参考。差异详见 §0。v1 文件保留为 `specs/features/agent-acp-loop/2026-07-29-agent-acp-loop-design.md`。
>
> **完成说明**：v2 已落地。15 个 FR 全部实现；`go build` / `go vet` / `gofmt` / `go test -race ./...` 全绿；`npm run lint` 全绿。真二进制端到端验证（fake Anthropic SSE server）：stdout 唯一一行 `<port>N</port>`、notification 链 `thinking_delta → text_delta ×2 → done → agent_end` 且各条 `messageId` 与 `agent.prompt` 返的一致、`SIGTERM` 走完 4 步优雅关闭耗时 ~12ms。实现偏差见 §9。

