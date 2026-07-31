# 多会话并发执行设计文档

> **范围**：让多个 session 各自独立并发运行 agent loop；切换 session 不中断后台 session 的执行；事件按 sessionId 路由让前端支持 unread 角标 / running 状态等可见性。

> **前置依赖**：
> - `agent-sessions-store`（S2/S4，SQLite 持久化已落地） — 仅关心 session 元数据 + messages 落库，本次不动其契约
> - `agent-acp-loop`（v2） — 已落地 `EventLedger` 按 session 路由 + `SessionManager`，但仍拒绝非 default session 路由 prompt
> - `electron-runtime-client`（v2） — `AgentClient` 已接 WS，`EventRouter` 当前无差别广播给所有 BrowserWindow

> **对齐参考**：本次设计对照了网易有道 OpenClaw (`packages/acp-core` + `packages/agent-core`) / LobsterAI Electron 端的实现（LobsterAI 仓库的 `src/main/coworkStore.ts` / `src/acp/control-plane/manager.core.ts` / `openclawChannelSessionSync.ts` 等），并发拓扑、runId 概念、LRU 驱逐、unread 角标等关键设计点与之一致。详见 §附录 A 对照表。

> **本文 vs. 已有 spec 的边界**：
> - `agent-sessions-store`：DB 持久化层，本文不重复
> - `agent-acp-loop` v2：单 session Loop 实现，本文把它升级为多 session Loops map，**复用**其 SessionManager / EventLedger
> - `electron-runtime-client` v2：本文只调整其**事件载荷**(`DarvinEvent` 加 sessionId+runId)

---

## 1. 概述

### 1.1 问题 / 背景

当前 session 是"伪多会话"：

| 层 | 现状 |
|----|------|
| renderer | `useSession` 维护 sessions[]；点 sidebar 切 session 只改 `activeSessionId` 视图 |
| main | `SessionStore` 持久化多 session + memory 中 `activeSessionId`；`darvin:prompt` handler 硬编码 `BACKEND_DEFAULT_SESSION_ID = "default"`（`src/main/index.ts:168-172`） |
| Go | `handler.handlePrompt` 拒绝非 default session（`internal/gateway/handlers.go:170-177`）；`acp.Loop` 单 `*agent.Agent` 串行（`internal/acp/loop.go:34-39`） |

**结果**：用户感觉"有多个 session"，但只有一个共享的 default agent run 上下文——切换 session 把 prompt 路由冲突，没有真并发。

### 1.2 目标

落地后：

1. **N 个 session 同时跑 N 个独立 agent run**。每个 session 自己的 `*agent.Agent` + 自己的 queue + 自己的 goroutine，互不阻塞
2. **切换 session 不影响后台 session**。renderer 切换 active 只是改 main 的 `activeSessionId`，Go 侧所有 session 的 agent run 继续推进
3. **同 session 内串行**。同一个 session 收到新 prompt 时，若 active run 未结束则 enqueue 到 `followUpQueue`（steer 类消息走 `steeringQueue`），保证同 session 状态机一致
4. **runId 精确 abort**。每次 prompt 在 main 端生成 runId（UUIDv4），下发到 Go；abort 按 sessionId + runId 定位精确停某次 turn
5. **资源有上限**。`SessionManager` 持 `maxSessions=5000` + `idleTtlMs=24h`，满则 LRU 驱逐最老 idle session，**有 active run 的 session 永不驱逐**
6. **后台 session 前端可见**。后端无差别推所有 session 事件；renderer 端按 sessionId 分发，写入 per-session message 索引；只把 active session 的内容显示到主区；后台 session 在 sidebar 列表显示 unread 红点 + running 状态；session 结束时若窗口不在前台发系统通知
7. **session ID 空间统一**。main 侧 UUID 直接作为 Go 侧 sessionID，去掉 `"default"` 特例

### 1.3 非目标

- **不做**多 Agent 优先级 / token 配额调度；N session 平等抢占 LLM，约束在 `maxSessions`
- **不做**session 暂停 / 恢复（suspend / resume）；切走即后台跑，没有暂停语义
- **不做**Go agent 重启后已激活 run 的自动恢复；用户需手动重发消息（沿用参考实现的做法）；`sessions_resume` 工具（仅播历史）由后续 spec 处理
- **不做**跨 session 记忆共享
- **不做**Agent 池大小限制（无 max-concurrent LLM 阈值）；并发约束靠 `maxSessions=5000` + provider rate limit
- **不引入**主进程 IPC channel 层的 session 级 filter（事件全量推到 renderer，session 级路由在 renderer 完成）

---

## 2. 用户场景

### 场景 1：切走不打断后台

**Given** session A 中发了一条长任务，agent 正在生成
**When** 用户切到 session B
**Then**
- B 切到时显示历史 messages（`getMessages(B)` 拉历史）
- A 的 agent goroutine 继续在 Go 推进，A 收到 `done` 时 SQL 落库
- A 在 sidebar 列表显示"running"状态 + unread 红点（如果切走前没新消息）

### 场景 2：两 session 同时独立跑

**Given** A、B 各自空闲
**When** A 发长 prompt，紧接着 B 发短 prompt
**Then**
- A、B 各自独立 goroutine，并发请求 LLM
- 所有 session 事件全量推到 renderer；renderer 端按 sessionId 分发到 per-session 消息索引
- 当前 active session 的消息才进主区；非 active 的只更新 sidebar 状态

### 场景 3：同 session 内串行

**Given** session A 上一轮 prompt 仍在跑（activeRun 存在）
**When** 用户在 A 内再发一条新 prompt
**Then**
- 新 prompt 入 `followUpQueue`，等 activeRun 完成后自动接续
- 渲染层提示"已排队，下一条将在上一条完成后开始"

### 场景 4：abort 只停当前

**Given** session A、B 都在跑
**When** active=A 时点"停止生成"
**Then**
- main 端用 `store.getActive()=A` + 上一轮返回的 runId，调 `client.abort({ sessionId: A, runId })`
- Go 侧按 `(sessionId, runId)` 定位 AcpSession.activeRun → `AbortController.abort()` → LLM cancel
- B 不受影响
- A 被加入 `stoppedSessions`；A 后续短时间内的新 prompt 直接拒绝（防 stop 后用户立刻发新消息再起 race）

### 场景 5：后台 session 完成时通知

**Given** session A 在后台跑，窗口不在前台
**When** A 完成（收到 `done`）
**Then** main 端触发系统通知"Session A 完成"（仅在窗口失焦时，Electron `Notification` API）

### 场景 6：资源上限触发 LRU 驱逐

**Given** SessionManager 中已注册 5000 个 session（达到 `maxSessions` 上限），其中 1 个 active run，其余 idle
**When** 新 session 注册请求到来
**Then**
- **active run 的 session 不被驱逐**（"Active runs are never evicted to make cancellation ownership explicit"）
- 从 idle 集合选 LRU 最老的一个驱逐
- 驱逐时调 `Loop.cancel()` 把 goroutine 退出；session DB 行保留（可 `getMessages` 拉历史）

### 场景 7：idle TTL 清理

**Given** session 已 N 小时无活动（N > 24h）
**When** 后台定时器 `reapIdleSessions` 跑过
**Then** 该 session entry 从 in-memory map 移除（DB 行保留）；下次 prompt 到该 session 时 `GetOrCreate` 重建

---

## 3. 功能需求

### FR-1 Go 侧 SessionManager 升级（per-session 状态 + LRU 驱逐 + TTL）

`internal/gateway/sessionmgr.go` 增改：

```go
type SessionManager struct {
    mu          sync.RWMutex
    byID        map[string]*SessionEntry
    lru         *lruList                  // idle session 的 LRU 链表，按 lastTouchedMs 排序
    idleIndex   map[string]*lruNode
    maxSessions int                       // 默认 5000
    idleTtlMs   int64                     // 默认 24h
    clock       func() time.Time          // 测试可注入
}

type SessionEntry struct {
    Session       *session.Session
    Loop          *acp.Loop               // 本 session 专属 Loop
    cancel        context.CancelFunc      // 用于 abort / delete / LRU 驱逐
    activeRun     *activeRunState         // 同 session 串行保护（FR-7）；nil = 空闲
    lastTouchedMs int64                   // LRU 驱逐与 TTL 用
    stoppedUntilMs int64                  // FR-4 短窗口拒绝后续 prompt（默认 1000ms）
}

func (m *SessionManager) GetOrCreate(sessionID string, deps Deps) (*SessionEntry, error)
func (m *SessionManager) Stop(sessionID string, runId string) error  // 返回 error 表示 (session,run) 未找到
func (m *SessionManager) reapIdleSessions()                          // 后台定时器跑
```

LRU 行为：
- `GetOrCreate` 成功时把该 entry 提到 LRU 头
- `reapIdleSessions` 每次跑扫描：`now - lastTouchedMs > idleTtlMs` 的全部移除（cursor cancel、删 map）
- 插入新 entry 时若 `len(byID) >= maxSessions`：先尝试 `reapIdleSessions`，若仍超则从 LRU 尾驱逐 idle entry（**active run 的 entry 跳过不被驱逐**——若全部都是 active run，新插入返回 `ErrSessionsLimit`）

### FR-2 Go 侧 AcpSession / Loop 改造

`internal/acp/loop.go` 重新组织（参考 OpenClaw `packages/acp-core/src/session.ts` 形态）：

- 每次 prompt 在 main 端生成 runId（UUIDv4）下发；Go 侧每次 prompt 创建 `activeRunState{ runId, cancel, startedMs }`，挂到 entry 上
- Loop 拆为：
  - `promptBox` chan：用户输入
  - `steeringQueue` / `followUpQueue`：同 session 内排队（FR-7）
  - `Run()` goroutine：从 promptBox 读一条；进入 turn 状态时通过 `entry.activeRun = ...` 注册
- `Loop.Prompt(req)` 语义：
  - 若 entry 没有 activeRun → 立刻启动一个 turn
  - 否则入 `followUpQueue`（排队等）
- `Loop.Steer(req)`（扩展点，本期 UI 不调，但 Loop 内预留接口）：入 `steeringQueue`
- `cancel()`（Loop 退出路径）：
  - 调 `entry.activeRun.cancel()` 中断当前 LLM 请求
  - 关闭 promptBox；Run() goroutine 退出

### FR-3 main 侧 prompt 路由

`src/main/index.ts` 的 `darvin:prompt` handler：

- 删除硬编码 `BACKEND_DEFAULT_SESSION_ID`
- 取 `const active = sessionStore.getActive()`，null 返回 `{ ok: false, code: 'no-active-session' }`
- 取上一轮已知的 runId（按 active session 缓存，`Map<sessionId, runId>`）；不存在则本次新生成 `runId = uuidv4()`
- `agentClient.prompt({ content, sessionId: active, runId, model })` 返回 `{ sessionId: active, messageId, runId }`
- 把返回的 runId 缓存到本地 `currentRunIdBySessionId[active]`

`src/main/runtime/client.ts`：

- 删除 `BACKEND_DEFAULT_SESSION_ID` 常量（不再需要；启动期 warm-up 改用 `INITIAL_BOOT_SESSION_ID = '__boot__'`，仅 main 启动时调一次）
- `client.prompt` 接口加 `runId: string` 字段

### FR-4 abort 用 runId 精确停止

- `darvin:abort` handler：拿 `sessionStore.getActive()` + 本地缓存的 `currentRunIdBySessionId[active]` → `client.abort({ sessionId, runId })`
- `client.abort` 同样新增 `runId`
- Go `handleAbort` 按 `(sessionId, runId)` 定位：`SessionManager.Stop(sessionId, runId)` → 调 `entry.activeRun.cancel()`；若 runId 不匹配 activeRun 或 session 不存在，返回 `error` 事件给前端
- abort 成功后将 `entry.stoppedUntilMs = now + 1000ms`；同 session 内 stop 后短窗口（≤1s）内的新 prompt 直接被 server-side 拒绝，返回 `{ code: 'session-stalled' }`，避免与 abort race

### FR-5 DarvinEvent 携带 sessionId + runId

`src/shared/darvin-api.ts`：

```ts
export type DarvinEvent =
  | { type: 'text_delta';    sessionId: string; runId: string; messageId: string; delta: string }
  | { type: 'thinking_delta'; sessionId: string; runId: string; messageId: string; delta: string }
  | { type: 'tool_start';    sessionId: string; runId: string; messageId: string; tool: string; input: unknown }
  | { type: 'tool_end';      sessionId: string; runId: string; messageId: string; tool: string; output: unknown }
  | { type: 'done';          sessionId: string; runId: string; messageId: string; usage?: DarvinUsage }
  | { type: 'error';         sessionId: string; runId: string; messageId: string; message: string }
  | { type: 'agent_end';     sessionId: string; runId: string };
```

`src/shared/darvin-api.ts` `DarvinPromptResponse`：

```ts
export interface DarvinPromptResponse {
  sessionId: string;
  messageId: string;
  runId: string;          // ← 新增
}
```

Go 侧 `internal/gateway/eventledger.go` 的 `mapEventToTS` 把 `sessionID` + `runID`（来自 event.EventCommon.RunID）注入每个事件。

### FR-6 main 侧 EventRouter 不再过滤，全量广播

`src/main/store/EventRouter.ts.handle()`：

```ts
handle(ev: SessionEvent): void {
  this.applyToStore(ev);                              // 落库（按 messageId 关联）
  for (const win of this.getWindow()) {                // 无差别广播
    if (win.isDestroyed()) continue;
    win.webContents.send(DarvinPushEvent.SessionEvent, ev);
  }
}
```

—— 与网易 OpenClaw `main.ts:2420-2530 bindCoworkRuntimeForwarder` 一致：不在主进程做 session 路由，由渲染层按 sessionId 分发。

### FR-7 renderer 端按 sessionId 分发

`src/renderer/store/<store>.ts`（renderer Redux-style / composable）结构调整：

- 维持全局 `messagesBySessionId: Record<sessionId, Message[]>`
- 派生 `currentMessages = messagesBySessionId[currentSessionId] ?? []`（仅主区显示）
- 派生 `streamingSessionIds: Set<sessionId>`：哪些 session 正在跑（用于 sidebar `running` 状态）
- 派生 `unreadSessionIds: Set<sessionId>`：最近一次该 session 是 active 后又有新消息的事件（用于 sidebar 红点）

事件分发 reducer 逻辑（伪代码）：

```ts
function applyEvent(ev: DarvinEvent, state) {
  const isCurrent = ev.sessionId === state.currentSessionId;
  const lastSeenAt = state.lastSeenAtBySessionId[ev.sessionId] ?? 0;

  // 1. 写消息索引（所有 session）
  state.messagesBySessionId[ev.sessionId] ??= [];
  appendMessage(state.messagesBySessionId[ev.sessionId], ev);

  // 2. text/thinking delta 只更新当前 session 的 isStreaming
  if (ev.type === 'text_delta' || ev.type === 'thinking_delta') {
    if (isCurrent) state.isStreaming = true;
    state.streamingSessionIds.add(ev.sessionId);
  }
  if (ev.type === 'done' || ev.type === 'error' || ev.type === 'agent_end') {
    state.streamingSessionIds.delete(ev.sessionId);
    if (isCurrent) state.isStreaming = false;
  }

  // 3. unread 标记
  if (!isCurrent && ev.type !== 'agent_end') {
    state.unreadSessionIds.add(ev.sessionId);
  }

  // 4. 切回时清 unread
  switchSession(state, newActive) {
    state.lastSeenAtBySessionId[state.currentSessionId] = Date.now();
    state.currentSessionId = newActive;
    state.unreadSessionIds.delete(newActive);
    state.isStreaming = state.streamingSessionIds.has(newActive);
  }
}
```

sidebar 渲染：
- 每个 session item 显示 `running` 状态（来自 `streamingSessionIds.has(id)`）
- 后台 session 显示 unread 红点（来自 `unreadSessionIds`）
- 不显示后台 session 的具体流式 token

### FR-8 后台 session 完成通知

当 EventRouter 收到 `done` / `error` 且窗口不在前台时，main 端触发系统通知：

- `src/main/libs/agentTaskNotifier.ts`（新增）
- 仅在 `mainWindow.isFocused() === false` 触发
- 复用参考实现 `src/main/libs/taskCompletionNotifier.ts:94-105` 的语义

### FR-9 session ID 统一

- main 端 `SessionStore.create()` 生成 `uuid v4`（已实现）
- 该 UUID 不经变换作为 `client.prompt(...)` 的 `sessionId` 字段传给 Go
- Go `SessionManager` 接收任意字符串 `sessionID`
- 旧 DB 记录里如存在 `"default"` 字面 ID：作为遗留 session 行保留可用；不再自动路由到该 ID 的新 prompt

### FR-10 main 端 SessionStore activeSessionId 持久化？

本期沿用现状（仅内存保持）；重启后重置为最近一个有 messages 的 session（best-effort）。**不做**跨重启完整恢复（已写入非目标）。

---

## 4. 实现方案

### 4.1 Go 侧 SessionManager 结构

```go
// internal/gateway/sessionmgr.go

type SessionManager struct {
    mu          sync.Mutex
    byID        map[string]*SessionEntry
    idleOrder   *list.List                  // LRU：front = 最新；back = 最老
    idleIndex   map[string]*list.Element    // sessionID → list elem
    maxSessions int
    idleTtlMs   int64
    now         func() int64                // 测试可注入；默认 time.Now().UnixMilli
}

type SessionEntry struct {
    Session        *session.Session
    Loop           *acp.Loop
    cancel         context.CancelFunc
    activeRun      *activeRunState
    lastTouchedMs  int64
    stoppedUntilMs int64
}

type activeRunState struct {
    runId       string
    cancelRun   context.CancelFunc
    startedMs   int64
    msgId       string
}

func (m *SessionManager) GetOrCreate(
    sessionID string, deps Deps,
) (*SessionEntry, error) {
    m.mu.Lock()
    defer m.mu.Unlock()

    if e, ok := m.byID[sessionID]; ok {
        e.lastTouchedMs = m.now()
        m.touchLRU(sessionID)
        return e, nil
    }

    // 限额检查
    if len(m.byID) >= m.maxSessions {
        m.reapIdleLocked()  // 先尝试 idle 驱逐
        if len(m.byID) >= m.maxSessions {
            // 仍超：所有 entry 都是 active run → 拒绝
            return nil, ErrSessionsLimit
        }
    }

    sess := m.createSessionLocked(sessionID)
    ctx, cancel := context.WithCancel(context.Background())
    loop := acp.NewLoop(ctx, sess, deps)

    entry := &SessionEntry{
        Session:       sess,
        Loop:          loop,
        cancel:        cancel,
        lastTouchedMs: m.now(),
    }
    m.byID[sessionID] = entry
    m.idleOrder.PushFront(sessionID)
    m.idleIndex[sessionID] = m.idleOrder.Front()
    return entry, nil
}

func (m *SessionManager) Stop(sessionID, runId string) error {
    m.mu.Lock()
    defer m.mu.Unlock()
    e, ok := m.byID[sessionID]
    if !ok { return ErrSessionNotFound }
    if e.activeRun == nil || e.activeRun.runId != runId {
        return ErrRunMismatch
    }
    e.activeRun.cancelRun()
    e.stoppedUntilMs = m.now() + 1000
    return nil
}

func (m *SessionManager) reapIdleSessions() {
    m.mu.Lock()
    defer m.mu.Unlock()
    m.reapIdleLocked()
}

func (m *SessionManager) reapIdleLocked() {
    nowMs := m.now()
    // TTL
    for {
        back := m.idleOrder.Back()
        if back == nil { break }
        sid := back.Value.(string)
        e := m.byID[sid]
        if e.activeRun != nil { break }  // active run 不动
        if nowMs - e.lastTouchedMs <= m.idleTtlMs { break }
        m.evictLocked(sid)
    }
}

func (m *SessionManager) evictLocked(sessionID string) {
    e, ok := m.byID[sessionID]
    if !ok { return }
    if e.activeRun != nil { return }      // 永远不驱逐 active
    e.cancel()                            // 关 Loop goroutine
    delete(m.byID, sessionID)
    if elem, ok := m.idleIndex[sessionID]; ok {
        m.idleOrder.Remove(elem)
        delete(m.idleIndex, sessionID)
    }
}

func (m *SessionManager) touchLRU(sessionID string) {
    if elem, ok := m.idleIndex[sessionID]; ok {
        m.idleOrder.MoveToFront(elem)
    }
}
```

### 4.2 AcpSession.Loop 改造

```go
// internal/acp/loop.go

type Loop struct {
    ctx            context.Context
    session        *session.Session
    deps           Deps

    promptBox      chan promptReq      // 主入口
    steerQueue     []promptReq         // 当前 turn 内的插入指令（steer）；本次 UI 不发，Loop 内部预留
    followUpQueue  []promptReq         // 等 active run 完成后接续

    mu             sync.Mutex
    activeRun      *activeRunState
    runIdGen       func() string       // main 传入
    stoppedUntil   int64
}

type promptReq struct {
    runId    string
    content  string
    model    string
    msgId    string
}

func (l *Loop) Prompt(req promptReq) {
    l.mu.Lock()
    if l.activeRun != nil {
        l.followUpQueue = append(l.followUpQueue, req)
        l.mu.Unlock()
        return
    }
    l.mu.Unlock()
    l.promptBox <- req
}

func (l *Loop) Stop(runId string) {
    l.mu.Lock()
    defer l.mu.Unlock()
    if l.activeRun != nil && l.activeRun.runId == runId {
        l.activeRun.cancelRun()
    }
}

func (l *Loop) Run() {
    for {
        select {
        case <-l.ctx.Done():
            return
        case req := <-l.promptBox:
            l.executeTurn(req)
        }
    }
}

func (l *Loop) executeTurn(req promptReq) {
    runCtx, cancel := context.WithCancel(l.ctx)
    state := &activeRunState{
        runId: req.runId, cancelRun: cancel, startedMs: nowMs(), msgId: req.msgId,
    }
    l.mu.Lock()
    l.activeRun = state
    l.mu.Unlock()

    defer func() {
        l.mu.Lock()
        l.activeRun = nil
        l.mu.Unlock()
        cancel()
    }()

    // 调 LLM 流式；runCtx.Done 触发 cancel → LLM provider 中断
    l.deps.Executor.RunConversation(runCtx, l.session, req)

    // 完成后挑 followUpQueue 下一条
    l.mu.Lock()
    if len(l.followUpQueue) > 0 {
        next := l.followUpQueue[0]
        l.followUpQueue = l.followUpQueue[1:]
        l.mu.Unlock()
        l.promptBox <- next
    } else {
        l.mu.Unlock()
    }
}
```

### 4.3 main 侧 prompt / abort

```ts
// src/main/index.ts

const currentRunIdBySessionId = new Map<string, string>();

ipcMain.handle(DarvinChannel.Prompt, async (_evt, req: DarvinPromptRequest) => {
  const active = sessionStore.getActive();
  if (active === null) {
    return { ok: false, code: 'no-active-session' };
  }
  const runId = currentRunIdBySessionId.get(active) ?? randomUUID();
  try {
    const r = await agentClient.prompt({
      content: req.content,
      sessionId: active,
      runId,
      model: req.model,
    });
    currentRunIdBySessionId.set(active, r.runId);
    return { ok: true, sessionId: active, messageId: r.messageId, runId: r.runId };
  } catch (e) {
    if (isCode(e, 'session-stalled')) {
      return { ok: false, code: 'session-stalled' };
    }
    throw e;
  }
});

ipcMain.handle(DarvinChannel.Abort, async () => {
  const active = sessionStore.getActive();
  if (active === null) return { aborted: false, sessionId: null };
  const runId = currentRunIdBySessionId.get(active);
  if (!runId) return { aborted: false, sessionId: active };
  const r = await agentClient.abort({ sessionId: active, runId });
  return { aborted: r.aborted, sessionId: active };
});
```

### 4.4 EventRouter 不再过滤

```ts
// src/main/store/EventRouter.ts  简化为
handle(ev: SessionEvent): void {
  this.applyToStore(ev);
  for (const win of this.getWindow()) {
    if (win.isDestroyed()) continue;
    win.webContents.send(DarvinPushEvent.SessionEvent, ev);
  }
}
```

> 注意：删掉 `activeSessionId` 读分支；本组件不再依赖 `SessionStore.activeSessionId`。

### 4.5 系统通知

```ts
// src/main/libs/agentTaskNotifier.ts  新增

export function notifyIfHidden(win: BrowserWindow, sessionId: string, title: string) {
  if (win.isFocused() || win.isMinimized()) return;  // 前台时不通知
  const t = new Notification({ title, body: t('agentTaskNotifier.completed', { sessionId }) });
  t.show();
}
```

EventRouter 收到 `done` 时调一次：传当前 best-effort 解析的 session 标题。

---

## 5. 边界情况

| 场景 | 处理方式 |
|------|----------|
| **active session 为 null 时发消息** | IPC 返回 `{ ok: false, code: 'no-active-session' }`；renderer 提示 |
| **Go 侧首次见到的 sessionId 收到 prompt** | `GetOrCreate` 创建新 entry，正常处理 |
| **Go 重启后内存 entry 丢失但 DB 有历史 session** | main 调 `getMessages(sessionId)` 正常用；下次 prompt 时 `GetOrCreate` 重建 entry（DB 数据已在 S2 落库） |
| **删 active session 时该 session 正在跑** | `darvin:delete-session` handler：先 `client.abort(sessionId, runId)` 优雅停 → `client.deleteSession(sessionId)` 清 Go 侧 → `store.delete()` 删 main → 推 ActiveSessionChanged |
| **maxSessions 已达上限 + 全部是 active** | Go 返回 `ErrSessionsLimit` → IPC `error` 事件给前端，提示用户关掉几个后台 session |
| **LRU 驱逐时刚好 GetOrCreate 新 session** | 互斥：`GetOrCreate` 内 mutex 串行化；驱逐路径也走 mutex |
| **同时切换 + abort** | 串行 IPC 处理；Go 侧 mutex 保护 |
| **abort 后短窗口内同 session 又来 prompt** | Go 端 `stoppedUntilMs` 拒绝，返回 `session-stalled`；前端提示"上一轮刚停止，请稍候" |
| **Go 侧 panic / OOM** | `defer recover()`；panic 时 main 收 `error` 事件；不重启 Go 进程 |
| **系统通知权限缺失（macOS / Linux）** | `Notification.isSupported()` 检查；不支持则静默失败 |
| **历史 events 不重放** | 切回时 `getMessages(sessionId)` 拉历史完整消息；不重放流式 token |

---

## 6. 涉及文件

### 新增

| 文件 | 说明 |
|------|------|
| `src/darvin-agent/internal/gateway/lru.go` | LRU 链表 helper |
| `src/darvin-agent/internal/gateway/sessionmgr_internal.go` | GetOrCreate / Stop / reap 的实现细节 |
| `src/main/libs/agentTaskNotifier.ts` | 后台 session 完成时系统通知 |

### 修改

| 文件 | 变更 |
|------|------|
| `src/darvin-agent/internal/gateway/sessionmgr.go` | 重写：`SessionManager` + `SessionEntry` + `activeRunState`；LRU；TTL；maxSessions |
| `src/darvin-agent/internal/gateway/handlers.go` | `handlePrompt` 移除单 session guard + 接受 runId；`handleAbort` 按 runId 定位；新增 `handleDeleteSession` |
| `src/darvin-agent/internal/acp/loop.go` | 拆 `promptBox` / `followUpQueue` / `steeringQueue`；`Run()` / `Prompt()` / `Stop(runId)` |
| `src/darvin-agent/internal/gateway/eventledger.go` | `mapEventToTS` 把 sessionId + runId 注入 TS 事件 |
| `src/main/index.ts` | prompt/abort/delete-session handler 用 active + runId |
| `src/main/runtime/client.ts` | `prompt` / `abort` 接口加 runId；删 `BACKEND_DEFAULT_SESSION_ID` |
| `src/main/store/EventRouter.ts` | 移除 active 过滤；保留落库 + 全量广播；调 `agentTaskNotifier` |
| `src/main/store/SessionStore.ts` | 无合约变更；维持现状 |
| `src/shared/darvin-api.ts` | `DarvinEvent` 各分支加 sessionId + runId；`DarvinPromptResponse` 加 runId |
| `src/renderer/store/<messages>.ts` | 拆 `messagesBySessionId` + `currentMessages` 派生 |
| `src/renderer/store/<streaming>.ts` | 用 `streamingSessionIds: Set<sessionId>` 替换全局 isStreaming |
| `src/renderer/store/<unread>.ts` | 维护 `unreadSessionIds: Set<sessionId>`，切 session 时清 |
| `src/renderer/components/sidebar/SessionItem.vue` | 显示 running 状态 + unread 红点 |

### 不动的文件

- `src/preload/index.ts`：contextBridge 接口形状不变（DarvinApi.prompt/abort 仍只收 renderer 主动参；sessionId/runId 由 main 注入）
- `src/darvin-agent/internal/agent/session/session.go`：session 内部表示不变
- 数据库 schema（S2 已落地）：不变

---

## 7. 验收标准

### 7.1 手工验证（用 playwright-cli + DevTools）

- [ ] **场景 1**：A 发长 prompt；中途切到 B；切回 A：A 继续收到 `text_delta`（DevTools console 看到带 `runId` 的事件）；B 不在主区出现 A 的流式内容
- [ ] **场景 2**：A、B 各发短 prompt；DevTools Network 面板确认两条 prompt 的 WS 帧**并发**（不是串行）
- [ ] **场景 3**：A 已发一轮在跑，再发一条新 prompt：第二条 prompt 应在第一条 `done` 之后自动续跑（看 DevTools console 的事件时间戳）；UI 提示"已排队"
- [ ] **场景 4**：active=A 时点"停止生成"，console 看 A 的 `error`/`agent_end` 事件带 `sessionId=A + runId`；B 状态不变
- [ ] **场景 5**：A 后台跑，浏览器窗口失焦；A 完成后系统通知弹出
- [ ] **场景 6**：在 DB 状态写脚本模拟 5000 个 idle session，再起新 session：调 `GoSessionManager.GetOrCreate` 触发 LRU 驱逐，有 active 的不动
- [ ] **场景 7**：session idle 超 24h 后 `reapIdleSessions` 移除 entry（用 fake clock 测试）
- [ ] **sidebar 视觉**：后台 session 显示 running 状态点 + unread 红点；切换时红点清除

### 7.2 自动化 / 静态检查

- [ ] `cd src/darvin-agent && go vet ./... && go build ./...` 通过
- [ ] `cd src/darvin-agent && go test ./...` 全包通过
- [ ] `npm run lint` 全量通过
- [ ] `npm run build:agent` 在本机平台产出 `bin/darvin-agent-<platform>-<arch>`

### 7.3 新增单元测试

| 测试 | 覆盖 |
|------|------|
| `gateway/sessionmgr_test.go::TestSessionManager_GetOrCreate_Serializes` | 并发 GetOrCreate 同一 id 不重复建 |
| `gateway/sessionmgr_test.go::TestSessionManager_LRUEviction` | 满 5000 后新 entry 触发 LRU 驱逐最老 idle |
| `gateway/sessionmgr_test.go::TestSessionManager_ActiveRunNotEvicted` | active run 的 session 永不被驱逐 |
| `gateway/sessionmgr_test.go::TestSessionManager_TTLReap` | 超 idleTtl 的 entry 被 reapIdleSessions 移除 |
| `gateway/sessionmgr_test.go::TestSessionManager_StopByRunId` | 按 (sessionId, runId) 精确停；runId 不匹配返回 ErrRunMismatch |
| `gateway/sessionmgr_test.go::TestSessionManager_StoppedUntilBlocksPrompt` | stop 后 1s 内同 session 的 prompt 被拒 |
| `acp/loop_test.go::TestLoop_QueuedFollowUp` | active run 期间新 prompt 入 followUpQueue，完成后接续 |
| `acp/loop_test.go::TestLoop_StopHaltsLLM` | Stop(runId) 触发 runCtx.Done → LLM cancel |

### 7.4 兼容性回归

- [ ] DB schema 不变
- [ ] 旧 session 行启动后 list 正常
- [ ] 历史 messages 切回不丢（`getMessages` 路径不变）
- [ ] preload API 形状不变（renderer 调用点零改动除 store reducer）

### 7.5 非目标确认

- [ ] 不引入"suspend / resume session" API
- [ ] 不引入 `sessions_resume` 工具（单独 spec 处理）
- [ ] main 进程 EventRouter 不做 session 级 filter
- [ ] 不暴露跨 session prompt API
- [ ] 不做 max-concurrent LLM 限制

---

## 附录 A：与网易有道 OpenClaw / LobsterAI 的对照

| 维度 | 网易实现 | 本 spec | 一致性 |
|------|----------|---------|--------|
| session-keyed Agent | `Map<sessionId, AcpSession>` | `map[sessionID]*SessionEntry` | ✅ 同形态 |
| 同 session 串行 | steeringQueue + followUpQueue + activeRun 检查 | 同样三件套 | ✅ |
| 跨 session 真实并发 | 独立 AbortController + 独立 LLM 请求，无全局锁 | 独立 ctx + 独立 goroutine | ✅ |
| runId | UUIDv4，main 构造 | UUIDv4，main 构造 | ✅ |
| session ID 格式 | `agent:<agentId>:lobsterai:<uuid>` 复合 key | LobsterAI 端就是 `uuidv4`，未引入 OpenClaw 复合 key | 🟡 简化 |
| maxSessions + idleTtlMs | 5000 + 24h | 5000 + 24h | ✅ |
| LRU 驱逐 | 有 | 有 | ✅ |
| Active runs never evicted | 注释明示 | 同机制 | ✅ |
| Gateway 重启不自动 resume | 是 | 是 | ✅ |
| 事件分发 | main 全量广播 + 前端按 sessionId 过滤 | 同 | ✅ |
| unread 角标 | `markSessionUnread` reducer | `unreadSessionIds: Set<sessionId>` | ✅ |
| 系统通知 | `taskCompletionNotifier` 仅窗口失焦时 | 同 | ✅ |
| AbortController 给 LLM | agent.ts:531 | 同（Go 侧 `context.WithCancel` 作 cancel token） | ✅ |
| 三段式 abort | `stopRequested` + chat.abort RPC + stoppedSessions | main 端缓 runId + Go 端 `stoppedUntilMs` | ✅ |
| runId 不匹配主动拒绝 | 是 | 是（`ErrRunMismatch`） | ✅ |
| 历史 reload from SQLite | 切回时 `loadSession` | 同 | ✅ |

**差异点**：

1. **session key 复合形态**：网易 OpenClaw 用 `agent:<agentId>:lobsterai:<uuid>` 复合 key（LobsterAI 端），目的是让多 agent 共享同一 OpenClaw 网关时 session 不冲突；darvin-cowork 当前只有一个 agent 端点，**暂不引入复合 key**，只传 `uuidv4`；后续如需多 agent 再加
2. **im_session_mappings 表**：网易为了 IM 通道（飞书 / 钉钉等）与 UI session 双向映射独立建表；darvin-cowork 本期不接 IM 通道，**不做该表**

---

## 附录 B：实施拆分（建议 PR 顺序）

为方便 review 拆分：

1. **PR 1：shared 类型升级**
   - `DarvinEvent` 各分支加 sessionId + runId
   - `DarvinPromptResponse` 加 runId
   - 仅合约变更，main/renderer/Go 都先不消化；为兼容先全部兼容带 optional，后续步骤转强类型

2. **PR 2：Go 侧 SessionManager 升级**
   - 加 LRU / TTL / maxSessions
   - `GetOrCreate` 取代旧的入口
   - 测试 `gateway/sessionmgr_test.go`

3. **PR 3：Go 侧 AcpSession.Loop 改造**
   - 加 promptBox / followUpQueue / activeRunState
   - 加 runId 概念
   - 测试 `acp/loop_test.go`

4. **PR 4：handlers.go + handlers 路由改造**
   - 移除单 session guard
   - `handlePrompt` 接受 runId
   - `handleAbort` 走 `(sessionId, runId)`
   - mapEventToTS 注入 sessionId + runId

5. **PR 5：main 侧 prompt / abort handler + EventRouter 简化**
   - 用 `store.getActive()` 替代 default constant
   - 缓 runId
   - EventRouter 移除过滤
   - 加 `agentTaskNotifier`

6. **PR 6：renderer 端 per-session store + sidebar UI**
   - messagesBySessionId / streamingSessionIds / unreadSessionIds
   - SessionItem 显示 running + 红点
   - 切 session 时清 unread
