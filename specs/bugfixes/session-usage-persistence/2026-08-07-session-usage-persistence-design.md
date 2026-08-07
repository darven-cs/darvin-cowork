# 会话级 token usage 持久化设计文档

## 1. 概述

### 1.1 问题

用户在多 session 切换时，聊天头部的 context usage 圆环（`contextUsageBySessionId`）不能正确恢复每个 session 自己的"上下文窗口占用率"，而是显示一个陈旧/重复的数值。

复现路径：

1. 在 session A 跑一个 turn，触发了 `context_usage` 事件，renderer 写入 `contextUsageBySessionId[A] = {usedTokens: X, percent: Y, ...}`
2. 切到 session B（之前从未跑过 turn），或者切到 session C（曾跑过 turn 但 usage 已被 evict）
3. `getMessages(B)` 返回历史消息列表，但 B 没有新的 `context_usage` 事件，map 保持空（或显示上一次任何 session 的残留）
4. 用户看到"上下文圆环要么空白要么显示其他 session 的数据"

### 1.2 根因

链路探查结论：usage 数据**只活在内存里，没有持久化**。

- 生产端正常：`internal/llm/anthropic/stream.go:266-272 / 370-372` 把 SSE 流里的 `InputTokens / OutputTokens / CacheReadTokens` 拼成 `protocol.Usage`
- 落库链路**完全缺失**：
  - `sessions` 表 GORM 模型（`internal/agents/store/models.go:13-22`）无 usage 列
  - `messages` 表 GORM 模型（`internal/agents/store/models.go:31-46`）无 usage 列
  - `MessageRecord`（`internal/agents/store/message_store.go:18-30`）的 wire shape 也无 usage
  - 全仓库 grep `RecordUsage` 唯一生产调用点（`executor.go:227`）只写 `usage.Tracker`（`internal/agents/usage/tracker.go:25-29`，单槽 in-memory 缓存）
- 取回端**取不到**：handler 层无 `agent.get_session_usage` / `agent.get_usage` RPC；`agent.get_messages` 返回 `MessageRecord` 切片不带 usage
- Renderer 唯一入口是 `context_usage` 事件流（`useMessages.ts:593-595`），**不重放历史**

参考：DeepSeek-Reasonix 的 desktop 端采用 session 级旁挂 JSON 文件（`<session>.telemetry.json`）解决同类问题 —— session 维度的累计 + 最近一次 turn 快照，存盘在每次 usage 事件 + turn 边界，session rebind 时从 sidecar 读回。

### 1.3 目标

切 session 之后，渲染层 `contextUsageBySessionId[sid]` 必须显示该 session 自己最后一次 LLM turn 的真实 usage（百分比、used tokens、cache hit/miss），包括：

- 重启 Electron / 重启 Go agent 后再切换进来
- session 从内存被 evict 后再被点开
- 切到一个有历史但当前不活跃的 session

### 1.4 非目标

- 不改 per-message usage 历史审计（每条 message 自己那一次 turn 的 usage 不入 messages 表）
- 不引入 pricing / cost 维度（Reasonix 的 `SessionCost` / `Sources` 暂不在范围）
- 不改 LLM streaming 的 producer 侧
- 不动 message 落库路径上的 `toolCallsJSON` 序列化逻辑
- 不为单条 assistant message 提供回放式 cumulative 计算（renderer 不做"sum across messages"，而是直接读快照）

## 2. 用户场景

### 场景 1: 冷启动 + 切回老 session

**Given** Electron 上次关之前在 session A 跑了 5 个 turn，最近一次 usage 是 `promptTokens=4200, completionTokens=300, percent=37%`；session B 也跑了 2 个 turn，最近一次 `promptTokens=800, percent=8%`
**When** 重启 Electron，进入 app，点击 session A
**Then** A 的圆环立即显示 `percent=37%`；切到 B 显示 `percent=8%`；不再空白或互串

### 场景 2: 活跃 session 切到不活跃 session

**Given** session A 正在 streaming（圆环显示 A 的实时 usage）；session B、C 都在磁盘上有历史快照
**When** 切到 B
**Then** B 显示自己最后一次 turn 的快照；A 切回去时仍显示实时 streaming 中的数字

### 场景 3: 全新 session

**Given** 新建一个 session，未发任何 prompt
**When** 切到这个新 session
**Then** 圆环不显示（或显示 `status='normal', usedTokens=0`），符合现有空态语义；不发任何 RPC 调用去拉快照（避免无效 RPC）

### 场景 4: turn 完成后落盘

**Given** 当前活跃 session 在 `executor.go:227` 收到 LLM `ChunkUsage`
**When** turn 完成
**Then** 该 session 的快照在 turn 边界**异步**落盘（不阻塞 turn 完成）；下次同 session 切换时直接命中

### 场景 5: session 删除

**Given** session B 存在快照行
**When** 删除 B
**Then** 快照行跟随 session 一起被删（外键或显式删）；不残留

## 3. 功能需求

### FR-1: SessionUsage 快照表

新增一张 GORM 表 `session_usages`：

| 列 | 类型 | 约束 | 说明 |
|----|------|------|------|
| `session_id` | `string` | PRIMARY KEY | 与 `sessions.id` 对齐 |
| `last_used_tokens` | `int` | NOT NULL DEFAULT 0 | 最近一次 turn 的 `prompt + completion` |
| `last_prompt_tokens` | `int` | NOT NULL DEFAULT 0 | |
| `last_completion_tokens` | `int` | NOT NULL DEFAULT 0 | |
| `last_cache_read_tokens` | `int` | NOT NULL DEFAULT 0 | |
| `last_cache_write_tokens` | `int` | NOT NULL DEFAULT 0 | |
| `last_cache_write_1h_tokens` | `int` | NOT NULL DEFAULT 0 | |
| `last_finish_reason` | `string` | nullable | 透传 `protocol.Usage.FinishReason` |
| `last_model` | `string` | nullable | 当前 session 用的 model id，给 percent 计算用 |
| `request_count` | `int` | NOT NULL DEFAULT 0 | 该 session 累计调 LLM 次数 |
| `total_prompt_tokens` | `int` | NOT NULL DEFAULT 0 | session 累计 |
| `total_completion_tokens` | `int` | NOT NULL DEFAULT 0 | |
| `total_cache_read_tokens` | `int` | NOT NULL DEFAULT 0 | |
| `updated_at` | `int64` | NOT NULL | unix ms，每次落盘更新 |

PRIMARY KEY 让 per-session upsert 是天然单行；写入用 GORM `Save` (INSERT ... ON CONFLICT REPLACE)，读用 `First WHERE session_id = ?`。

### FR-2: Tracker 升级

`internal/agents/usage/tracker.go` 当前是单槽 `Last *Usage`，扩成"累计 + 最近一次 turn 快照"：

```go
type Snapshot struct {
    Last *protocol.Usage  // 最近一次 turn
    Total *protocol.Usage // 累计
    LastUsedTokens int    // Last.PromptTokens + Last.CompletionTokens
    LastFinishReason string
    LastModel string
    RequestCount int
    UpdatedAt int64
}

func (t *Tracker) Record(u *protocol.Usage, model string)  // 增量累加 + 覆盖 Last
func (t *Tracker) Snapshot() Snapshot
func (t *Tracker) Reset()  // session rebind 时清空累计
```

并发约束不变：仍是 `sync.RWMutex` 包单实例（每个 `Agent` 持有一个）。

### FR-3: 落库时机

`Agent.Run` 末尾 / 每次 `RecordUsage` 之后调一次 `usageStore.Save(snapshot)`：

- **同步路径**：turn 末尾 `agent.go` 的"已完成的 Run"路径里（紧邻 `emitContextUsage` 之后），同步调用 Save。失败打 warn 日志，不重试，不影响 turn 完成。
- **异步路径**：可选。考虑到 turn 节奏（秒级），同步路径阻塞可忽略；本 spec 不引入异步落盘队列（YAGNI）。
- **不落库**：compaction / `Session.Rebind` / abort 路径上 — Reasonix 的 telemetry 也只在 usage 事件触发写，compaction 不重置；保持一致。

### FR-4: 取回 RPC

新增 IPC handler `agent.get_session_usage`：

```ts
// src/shared/darvin-api.ts
interface GetSessionUsageRequest { sessionId: string }
interface GetSessionUsageResponse {
  usage: DarvinSessionUsage  // null 表示该 session 没有快照
}

interface DarvinSessionUsage {
  sessionId: string
  lastUsedTokens: number
  lastPromptTokens: number
  lastCompletionTokens: number
  lastCacheReadTokens: number
  lastCacheWriteTokens: number
  lastFinishReason?: string
  lastModel?: string
  requestCount: number
  totalPromptTokens: number
  totalCompletionTokens: number
  totalCacheReadTokens: number
  updatedAt: number
}
```

handler 落点 `internal/gateway/handlers.go`，与现有 `agent.get_messages` 同级；走 JSON-RPC 2.0。

### FR-5: Renderer hydration

`src/renderer/composables/useMessages.ts` 在 session 切换路径（`getMessages` 成功回调之后）追加一次 `getSessionUsage(sid)`，把返回的 `DarvinSessionUsage` 转换成现有的 `DarvinContextUsage` 形状，写入 `contextUsageBySessionId[sid]`：

- 若 `lastUsedTokens === 0 && totalPromptTokens === 0` → 不写 map（保留现有空态语义）
- 否则按现有 `DarvinContextUsage` 字段映射：`usedTokens=lastUsedTokens`，`percent` 需要 `contextTokens`（model 上下文窗口）— `lastModel` 给出 model id，前端再用现有的 model registry 查窗口；查不到时 `percent=0`，只展示绝对值

切换调用方为现有 `useSidebar` / `useChatActions` 中触发 `getMessages` 的位置（已在 1.1 列出的探测里有），**不新增 composable**，只把 hydration 步骤接在已有 async 流程末尾。

### FR-6: Session 删除联动

`MessageStore.DeleteBySession` 已有；新增 `UsageStore.DeleteBySession(sid)`，在 session 删除 handler 里同步调用，保证快照不残留。

### FR-7: 不在范围内

- 不为 `Message` 表加 `usage` 列（避免每条 message 拖 JSON）
- 不引入 per-source（executor / planner / subagent）分桶（Reasonix 的 `Sources map`）
- 不持久化 `SessionCost`
- 不改 `internal/llm/anthropic/stream.go` 任何代码

## 4. 实现方案

### 4.1 数据层：`internal/agents/store/models.go`

新增 GORM 模型（与 `Message` 同文件）：

```go
type SessionUsage struct {
    SessionID              string `gorm:"primaryKey;column:session_id"`
    LastUsedTokens         int    `gorm:"column:last_used_tokens"`
    LastPromptTokens       int    `gorm:"column:last_prompt_tokens"`
    LastCompletionTokens   int    `gorm:"column:last_completion_tokens"`
    LastCacheReadTokens    int    `gorm:"column:last_cache_read_tokens"`
    LastCacheWriteTokens   int    `gorm:"column:last_cache_write_tokens"`
    LastCacheWrite1hTokens int    `gorm:"column:last_cache_write_1h_tokens"`
    LastFinishReason       string `gorm:"column:last_finish_reason"`
    LastModel              string `gorm:"column:last_model"`
    RequestCount           int    `gorm:"column:request_count"`
    TotalPromptTokens      int    `gorm:"column:total_prompt_tokens"`
    TotalCompletionTokens  int    `gorm:"column:total_completion_tokens"`
    TotalCacheReadTokens   int    `gorm:"column:total_cache_read_tokens"`
    UpdatedAt              int64  `gorm:"column:updated_at"`
}

func (SessionUsage) TableName() string { return "session_usages" }
```

`internal/database/` 的 AutoMigrate 加上 `&SessionUsage{}`；新装无迁移负担，老装由 GORM `AutoMigrate` 自动 `CREATE TABLE`。

### 4.2 UsageStore：`internal/agents/store/usage_store.go`

新增文件，参考 `message_store.go` 的形态：

```go
type UsageRecord struct {
    SessionID string
    Last      *protocol.Usage
    Total     *protocol.Usage
    LastModel string
    RequestCount int
    UpdatedAt int64
}

type UsageStore interface {
    Save(ctx context.Context, rec *UsageRecord) error
    Get(ctx context.Context, sessionID string) (*UsageRecord, error)
    DeleteBySession(ctx context.Context, sessionID string) error
}

type SQLiteUsageStore struct{ db *gorm.DB }

func NewSQLiteUsageStore(db *gorm.DB) *SQLiteUsageStore { ... }
```

`Save` 用 `s.db.Save(&row)`（PK 冲突替换）；`Get` 走 `Where("session_id = ?", ...).First(&row)`。

### 4.3 Tracker：`internal/agents/usage/tracker.go`

```go
type Snapshot struct {
    Last            *protocol.Usage
    Total           *protocol.Usage
    LastUsedTokens  int
    LastFinishReason string
    LastModel       string
    RequestCount    int
    UpdatedAt       int64
}

type Tracker struct {
    mu    sync.RWMutex
    last  *protocol.Usage
    total protocol.Usage   // value, not pointer — 累加不需要共享状态
    lastModel string
    requests int
}

func NewTracker() *Tracker { return &Tracker{} }

func (t *Tracker) Record(u *protocol.Usage, model string) {
    t.mu.Lock()
    defer t.mu.Unlock()
    cp := *u
    t.last = &cp
    t.total.PromptTokens += u.PromptTokens
    t.total.CompletionTokens += u.CompletionTokens
    t.total.TotalTokens += u.TotalTokens
    t.total.CacheReadTokens += u.CacheReadTokens
    t.total.CacheWriteTokens += u.CacheWriteTokens
    t.total.CacheWrite1hTokens += u.CacheWrite1hTokens
    t.lastModel = model
    t.requests++
}

func (t *Tracker) Snapshot() Snapshot {
    t.mu.RLock()
    defer t.mu.RUnlock()
    if t.last == nil { return Snapshot{} }
    snap := Snapshot{
        Last:             t.last,
        Total:            &t.total,
        LastUsedTokens:   t.last.PromptTokens + t.last.CompletionTokens,
        LastFinishReason: t.last.FinishReason,
        LastModel:        t.lastModel,
        RequestCount:     t.requests,
        UpdatedAt:        time.Now().UnixMilli(),
    }
    return snap
}

func (t *Tracker) Reset() {
    t.mu.Lock()
    defer t.mu.Unlock()
    t.last = nil
    t.total = protocol.Usage{}
    t.lastModel = ""
    t.requests = 0
}

func (t *Tracker) Last() *protocol.Usage {
    t.mu.RLock()
    defer t.mu.RUnlock()
    if t.last == nil { return nil }
    cp := *t.last
    return &cp
}
```

保留 `Last()` 兼容现有 `agent.go:225-230` 的 `emitContextUsage`。

### 4.4 Agent 集成：`internal/agents/agent.go`

`New(cfg)` 构造时 wire `usageStore`：

```go
type Deps struct {  // 已有，扩字段
    ...
    UsageStore store.UsageStore
}

func New(cfg Config, deps Deps) *Agent {
    ...
    a.usageStore = deps.UsageStore
}
```

`Agent.Run` 末尾（紧邻 `emitContextUsage`）：

```go
func (a *Agent) Run(...) error {
    defer func() {
        if a.usageStore != nil {
            snap := a.tracker.Snapshot()
            if snap.Last != nil {
                _ = a.usageStore.Save(context.Background(), &store.UsageRecord{
                    SessionID:    a.session.ID,
                    Last:         snap.Last,
                    Total:        snap.Total,
                    LastModel:    snap.LastModel,
                    RequestCount: snap.RequestCount,
                    UpdatedAt:    snap.UpdatedAt,
                })
            }
        }
    }()
    ...
}
```

`RecordUsage(u)` 当前调用点 `executor.go:227` 改成 `a.tracker.Record(u, a.currentModel())`，增加 model 参数。

`currentModel()` 从 `a.cfg.Model` 或 `a.session.AgentID` 派生（已有对应 helper）。

### 4.5 Handler：`internal/gateway/handlers.go`

```go
case "agent.get_session_usage":
    sid, _ := params["sessionId"].(string)
    if sid == "" { return nil, errMissingSessionID }
    rec, err := h.usageStore.Get(ctx, sid)
    if errors.Is(err, gorm.ErrRecordNotFound) {
        return &GetSessionUsageResponse{Usage: nil}, nil
    }
    if err != nil { return nil, err }
    return &GetSessionUsageResponse{Usage: toWireUsage(rec)}, nil
```

`toWireUsage` 把 `store.UsageRecord` 映射成 `darvin-api.ts` 里的 `DarvinSessionUsage`（不含 `protocol.Usage` 自身，避免循环依赖）。

### 4.6 Renderer：`src/renderer/composables/useMessages.ts`

现有 `getMessages` 之后接 hydration：

```ts
async function loadSession(sessionId: string): Promise<void> {
    await getMessages(sessionId);
    const snap = await window.darvin.getSessionUsage(sessionId);
    if (snap && (snap.lastUsedTokens > 0 || snap.totalPromptTokens > 0)) {
        const ctxWindow = getContextWindow(snap.lastModel);
        const percent = ctxWindow > 0
            ? Math.min(100, (snap.lastUsedTokens / ctxWindow) * 100)
            : 0;
        contextUsageBySessionId.value = {
            ...contextUsageBySessionId.value,
            [sessionId]: {
                sessionId,
                status: 'normal',
                usedTokens: snap.lastUsedTokens,
                contextTokens: ctxWindow,
                percent,
                compactionCount: 0,
                model: snap.lastModel,
                updatedAt: snap.updatedAt,
            },
        };
    }
}
```

`getContextWindow(model)` 复用现有 model registry helper（`src/renderer/services/llm-models.ts` 之类），与 `emitContextUsage` 在 Go 侧计算的来源一致（Anthropic 200k / DeepSeek 128k 等）。

### 4.7 Preload + 类型：`src/preload/index.ts` + `src/shared/darvin-api.ts`

`DarvinApi` 接口加 `getSessionUsage(req: GetSessionUsageRequest): Promise<GetSessionUsageResponse>`；preload 暴露同名 wrapper。`DarvinPushEvent` 不变。

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| 用户中断操作（abort） | abort 不调用 `RecordUsage`，turn 不落库；下次恢复时按下一 turn 的真实 usage 覆盖 |
| 网络断开 / LLM 错误 | `executor.go` 错误路径不发 `ChunkUsage`，tracker 不更新，不落库；显示老的快照或空白 |
| Go agent 重启后切 session | `getSessionUsage` 直接读 SQLite 快照，不依赖内存 |
| 同 session 多个并发 turn（理论可能） | tracker mutex 已经串行化；handler 层也单条 in-flight，不冲突 |
| 老装 SQLite 库无 `session_usages` 表 | GORM `AutoMigrate` 启动时自动 `CREATE TABLE`；不需手写迁移 |
| session 删除 | `DeleteBySession` 同步删 `session_usages` 行；不残留 |
| `lastModel` 不在 registry 里 | `getContextWindow` 返回 0，`percent=0`，仅显示绝对值；不抛错 |
| Tracker.Last() 在新 session 未发 turn 时返回 nil | `emitContextUsage` 已有 `if last == nil { 走 estimator fallback }` 逻辑，不动 |
| 同 session 跑了多轮后部分 turn 失败 | tracker 累加只算成功的 turn（错误路径不进 `RecordUsage`），符合实际 API 消耗 |

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/darvin-agent/internal/agents/store/models.go` | 新增 `SessionUsage` GORM 模型 |
| `src/darvin-agent/internal/agents/store/usage_store.go` | 新增 `UsageStore` 接口 + `SQLiteUsageStore` 实现 |
| `src/darvin-agent/internal/database/` | AutoMigrate 注册 `&SessionUsage{}` |
| `src/darvin-agent/internal/agents/usage/tracker.go` | 升级为"累计 + 最近一次"模型；新增 `Snapshot` / `Record(u, model)` |
| `src/darvin-agent/internal/agents/agent.go` | `New` wire `usageStore`；`Run` 末尾落库；`RecordUsage` 增 model 参数 |
| `src/darvin-agent/internal/agents/executor/executor.go` | `d.RecordUsage(turnUsage)` 改成 `d.RecordUsage(turnUsage, model)` |
| `src/darvin-agent/internal/gateway/handlers.go` | 新增 `agent.get_session_usage` handler；session 删除 handler 加 `usageStore.DeleteBySession` |
| `src/shared/darvin-api.ts` | 新增 `GetSessionUsageRequest/Response` + `DarvinSessionUsage` 类型；`DarvinApi.getSessionUsage` 接口 |
| `src/main/runtime/client.ts` | `AgentClient` 加 `getSessionUsage` 方法 |
| `src/preload/index.ts` | contextBridge 暴露 `getSessionUsage` |
| `src/renderer/composables/useMessages.ts` | `loadSession` 末尾追加 hydration 写 `contextUsageBySessionId` |
| `src/renderer/services/llm-models.ts`（或同类） | 暴露 `getContextWindow(model)` helper（若已有则复用） |

不涉及：

- `src/darvin-agent/internal/llm/anthropic/stream.go`（producer 不动）
- `src/darvin-agent/internal/llm/anthropic/anthropic.go`
- `src/main/index.ts`（已注册 handler 自动路由）

## 7. 验收标准

- [ ] Go 测试：`cd src/darvin-agent && go test ./internal/agents/store/... ./internal/agents/usage/...` 通过；新增测试覆盖 `Tracker.Record → Snapshot`、`UsageStore.Save → Get → DeleteBySession`
- [ ] GORM 迁移：启动一次 Go agent，`session_usages` 表存在；`sqlite3 ~/.darvin/sessions.db ".schema session_usages"` 输出符合 FR-1
- [ ] IPC：`window.darvin.getSessionUsage(sid)` 返回符合 `DarvinSessionUsage` shape；不存在的 sid 返回 `usage: null`
- [ ] 场景 1（冷启动恢复）：手动测 — 在 A 跑一个 turn → 重启 Electron → 点 A，header 圆环显示正确 percent
- [ ] 场景 2（活跃 → 不活跃切）：A streaming 时切 B，A 切回仍显示实时值，B 显示自己最近 turn 的快照
- [ ] 场景 3（空 session）：新建 session 不发 RPC，圆环空态符合现状
- [ ] 场景 4（删除联动）：删除带快照的 session，对应 `session_usages` 行被删
- [ ] `npm run lint` 通过
- [ ] `npm run test` 通过
- [ ] 手动用 `npm start` 跑通：
  - 多个 session 反复切换，每个 session 的 usage 数字稳定不互相污染
  - 关闭并重启 app 后再切回，usage 数字仍是上次最近 turn 的快照
- [ ] `go vet ./...` 与 `go build ./...` 在 `src/darvin-agent/` 通过