# 统一会话/消息数据库（去掉 Electron 端 SessionStore）

> **范围**：把现在 Electron 主进程里的 `SessionStore`（`darvin-cowork.sqlite`）和 Go agent 里的 GORM `sessions.db` 合并成单一数据源 —— 由 Go 持有权威 schema，Electron 端的所有 session / message 读写通过现有 JSON-RPC 走 Go 侧。`EventRouter` 不再写本地库，只做纯转发。
>
> **本次不迁历史数据**：仓库仍处 dev 阶段，尚未公开发布；落地后直接删 `darvin-cowork.sqlite`，由 GORM AutoMigrate 在 `sessions.db` 上建新 schema。renderer 的 stale list / active session / message cache 都按"空"处理，重启即干净状态。**不**写迁移工具、不读老 schema、不留底。
>
> **本次不动**：渲染层 `DarvinApi` 契约、`DarvinEvent` 形状、renderer 端 store、IPC channel 名、preload 形状。
>
> **仓库约定**：AGENTS.md:79-83 明确仓库尚未配置 `npm run test` / `npm run check`，CI 入口只有 `npm run lint`。本 spec 的验收命令只用 Go 原生命令（`go vet` / `go test` / `go build`）和 `npm run lint`。UI / Electron 行为验证走 `playwright-cli attach`（不入 CI），与 AGENTS.md:85 一致。
>
> **前置 spec 链**：
> - `specs/refactors/per-session-acp-agent/2026-07-31-...` 已经把"per-session AcpSession"和"EventCommon.SessionID 来自该 session 的 Agent"落到位；
> - `specs/features/multi-concurrent-session-runs/...` 已经把 main 端 `prompt / abort / EventRouter / DarvinEvent 载荷`全部改造完成。
> - 本 spec 接力，**只动**数据落库这一段。

---

## 1. 概述

### 1.1 问题 / 背景

仓库现在同时存在两份 session / message 数据：

| 数据源 | 文件 | 进程 | 表 | 关键差异 |
|--------|------|------|-----|----------|
| Electron `SessionStore` | `<userData>/darvin-agent/darvin-cowork.sqlite` | main 进程 | `sessions` (id, title, claude_session_id, status, created_at, updated_at) + `messages` (id, session_id, role, content, done, error, tool_label, created_at) | 持有 title / done / error / tool_label；`search_sessions` / `search_messages` 只在这边 |
| Go `SQLiteStore` + `SQLiteMessageStore` | `<userData>/darvin-agent/sessions.db` | darvin-agent 子进程 | `sessions` (id, key, agent_id, status, created_at, updated_at) + `messages` (id, session_id, role, content, tool_calls, timestamp, stop_reason, parent_id) + `compaction_checkpoints` + `skill_snapshots` | 没有 title / done / error / tool_label；只有 Go 侧需要 `tool_calls` / `parent_id` / `stop_reason` |

具体每条不一致如下：

- **schema 错位**：
  - Go `sessions` 缺 `title` / `claude_session_id`；这是 renderer sidebar 排序、显示、重命名的依据，缺一不可。
  - Go `messages` 缺 `done` / `error` / `tool_label`；renderer 依赖 `done` 切 streaming→done 状态、`error` 画错误泡、`tool_label` 画工具标签。
  - Go 多了 `tool_calls` / `parent_id` / `stop_reason` —— 这些只对 Go 内部 LLM 链路有意义，renderer 看不到。
- **行为错位**：
  - 同一个 session 在两份库里各有一行，删除 / 重命名时只能操作其中一份；Go 删了 `sessions` 行但 Electron 的 `darvin-cowork.sqlite` 还在，sidebar 列表里出现"幽灵 session"。
  - streaming delta 只在 Electron 累加（`SessionStore.appendMessageDelta`），Go 端只持久化整轮结束后的 content —— 中途进程崩了，Go 的 messages 表是空壳，Electron 的有部分内容。
  - `search_sessions` / `search_messages` 只在 Electron 端；Go 没暴露搜索方法。
  - active session id 是 Electron 进程内 state（`SessionStore.activeSessionId`）；Go 完全不知道用户当前在哪个 session。
  - `claude_session_id` 只在 Electron 表里存，Go 侧完全没这个概念 → 跨库 join 永远是断的。
- **维护负担**：
  - 写一个新功能要在两个 ORM / 两套 schema / 两个进程的 IPC 边界上同时想清楚；上一轮 per-session AcpSession spec 在这一段就因为"两边都有状态"而被迫保留镜像字段（§1.1 activeRun）。
  - 测试要 mock 两边；mock 不能复用。
  - 文档要解释两套（AGENTS.md 已经只提一份了）。

**根因**：AGENTS.md:18 说"业务逻辑...全部下放到 Go 运行时"，但 session 元数据（title / status / active）属于"业务逻辑 + 渲染层交互"两用，被两边各做了一份。

### 1.2 目标

落地后：

1. **一份 SQLite 文件、一份 schema、一个 owner**：`<userData>/darvin-agent/sessions.db`，由 Go 通过 GORM 管理；Electron 端彻底删除 `SessionStore.ts` / `darvin-cowork.sqlite`。
2. **Electron 端所有 session / message 读写经 JSON-RPC 走 Go**：`darvin:list_sessions` / `darvin:get_messages` / `darvin:create_session` / `darvin:rename_session` / `darvin:delete_session` / `darvin:search_sessions` / `darvin:get_active_session` / `darvin:switch_session` 全部改透到新增的 RPC 方法。
3. **streaming delta 在 Go 端落库**：`text_delta` 事件在 Go 内经 bus hook 立刻 `UPDATE messages SET content = content || ? WHERE id = ?`，不再依赖事件回到 Electron 那一段。
4. **active session 持久化**：用一张 Go 内的 `app_state` 表存 `active_session_id`，启动时从该表读出、设置 `SessionManager.active`；`switch_session` 走 `agent.set_active_session` 写回。
5. **`EventRouter` 变纯转发**：不再持有 `SessionStore` 引用，删除 `applyToStore` / `appendMessageDelta` / `markMessageDone` / `markMessageError` / `appendMessage` 等所有落库路径，只保留"Go event → renderer webContents.send"。
6. **renderer 行为零变化**：`DarvinApi` 形状、IPC channel 名、push 事件流（`SessionsChanged` / `ActiveSessionChanged` / `SessionEvent`）全部保持现有。renderer 仍然在 `prompt` 之前不知道后端真相（Electron 还是 Go 都行）。
7. **Go 进程离线时 UX 不崩**：main 进程保留一份 in-memory 缓存（最近一次 `list_sessions` 结果 + 每个 session 的最近一次 `get_messages` 结果），Go 离线时回退到缓存而不是直接 reject —— 这与"Go 死了就什么都看不到"比，至少保留最近一次的视图。

### 1.3 非目标

- **不改**渲染层 `DarvinApi` 契约 / `DarvinEvent` schema / IPC channel 名 / preload 形状。
- **不重写**agent loop / dispatcher 的 3 个 persistence hook（`persistUserMessage` / `persistAssistantMessages` / `persistSession`）的主逻辑；只把"asssitant content 完整落库"扩成"streaming 期间持续落库 + 完整落库"。
- **不引入**多 DB / 多实例（一个 userData 一个 db；不考虑云端 / 远端）。
- **不迁**Go 端 `tool_calls` / `parent_id` / `stop_reason` 字段到 renderer —— 这些继续只在 Go 侧维护。
- **不拆**per-session AcpSession 的现有结构（这是上一轮 spec 的产物，不动）。
- **不引入**新 ORM / 新迁移框架 —— GORM 仍在 Go 侧；不写迁移工具、不读老 schema（dev 阶段直接干净重启）。
- **不迁**EventLedger 的 fanout 语义（按 sessionId 分桶；上一轮 spec 已经落地）。
- **不**给 Electron 端加 persistent cache —— 缓存只在 main 进程的内存里，重启清空。冷启动时 Go 离线视为"空状态"。
- **不**做 OAuth / 多账号 / 多设备同步（与"单库"无直接关系，单独 spec 考虑）。

---

## 2. 用户场景

### 场景 1：冷启动 → 列出所有 session

**Given** 用户首次启动新版本（无任何历史数据）
**When** 进程启动后 renderer 调 `window.darvin.listSessions()`
**Then**
- Electron main 调 `client.listSessions()`（经 JSON-RPC `agent.list_sessions`）
- Go 端 `sessions.db` 由 AutoMigrate 建空 schema，返 `{ sessions: [] }`
- renderer 显示空 list；用户主动 `createSession` 后才开始有数据
- 老 `darvin-cowork.sqlite` 在 dev 阶段被忽略 / 删除（不需要保留历史）

### 场景 2：发 prompt

**Given** renderer 在 active session 上点发送
**When** renderer 调 `window.darvin.prompt({ content: 'hi' })`
**Then**
- Electron main 调 `client.prompt({ content, sessionId: active, runId, model })`
- Go 端 `persistUserMessage` hook 把 user message 写入 `sessions.db`（hook 1，不变）
- Go 端 `Loop.Submit` 把 prompt 放进 per-session queue
- LLM 开始 streaming 时，bus 上的 `text_delta` 事件触发新的 `textDeltaPersist` hook（FR-4），把 delta 累加到 `messages.content`
- `done` / `error` 事件再发一次"封口"的 `UPDATE`（`done=1` / `error=<msg>`）走 `markMessageDone` / `markMessageError`（FR-4）
- `persistAssistantMessages`（hook 2）把整轮 assistant 落库（已有，不动）
- `persistSession`（hook 3）刷 session 的 `updated_at`（已有，不动）

### 场景 3：Go 进程崩了再重启

**Given** Go 端 LLM streaming 中 Go 进程被 kill
**When** 用户重启 Electron
**Then**
- main 端重连 Go（`RuntimeMgr.start` + `client.connect` + `subscribeAllSessions`）
- renderer 调 `getMessages(active)` → 拿到 `sessions.db` 中已有的部分内容（user 完整、assistant 截至崩溃那一瞬时的部分 + `done=false`）
- renderer 把 assistant bubble 标为"未完成"（`done=false`），用户可见
- 后续 prompt 重启后从这条半截 assistant 之后继续（agent.Loop 启动时从 store 拉历史，per-session AcpSession FR-1 已有该路径）

### 场景 4：跨进程并发写

**Given** 旧实现下 Electron 的 `darvin-cowork.sqlite` 和 Go 的 `sessions.db` 是两个 writer
**When** 同一秒内 Electron 在写 `updated_at`、Go 也在写 `updated_at`
**Then**（旧）两边都写自己的，互不感知，可能不一致
**Then**（新）只有 Go 写，Electron 全走 RPC → 不存在并发写

### 场景 5：Go 离线

**Given** Go 子进程没起来 / WS 断
**When** renderer 调 `listSessions` / `getMessages` / `searchSessions` / `prompt` / `abort` / `switchSession` / `renameSession` / `deleteSession` / `getActiveSession`
**Then**
- `listSessions` / `getMessages` / `searchSessions` / `getActiveSession`：main 端查 in-memory 缓存（最后一次成功响应），命中就返回；冷启动缓存空就返 `[]` / `null`，不抛错
- `prompt`：抛 `agent offline`（语义不变）
- `abort`：返回 `{ aborted: false, sessionId: '' }`（与现状一致）
- 写类（`switchSession` / `renameSession` / `deleteSession` / `createSession`）：抛 `agent offline`

### 场景 6：active session 跨重启

**Given** 用户上一次退出时 active session 是 A
**When** 重新启动
**Then**
- Go 端从 `app_state` 表读出 `active_session_id == A`
- Go 端在 WS ready 时回 push 一次 `active_session_id`（如果上一轮还没推过）；主进程 `broadcastActiveSession` 把 A 推给 renderer
- renderer 拿到 A 当 active

### 场景 7：搜索

**Given** 用户在 sidebar 搜索框输入 "kubernetes"
**When** renderer 调 `searchSessions('kubernetes')`
**Then**
- Electron main 调 `client.request('agent.search_sessions', { query: 'kubernetes' })`
- Go 端在 `sessions` 表按 title 子串 + `messages` 表按 content 子串匹配，返前 100 条
- Electron 透回 renderer

---

## 3. 功能需求

### FR-1 Go 侧 `SessionStore` schema 扩展

在 `src/darvin-agent/internal/agent/store/models.go` 给 `Session` 增字段：

```go
type Session struct {
    ID              string    `gorm:"primaryKey"`
    Key             string    `gorm:"index"`
    AgentID         string    `gorm:"index"`
    Title           string    `gorm:"default:'新建会话'"`  // ← 新增
    ClaudeSessionID *string   //                            // ← 新增；nullable，给未来的 Claude backend bridge 留
    Status          string    `gorm:"default:'active'"`
    CreatedAt       time.Time `gorm:"autoCreateTime"`
    UpdatedAt       time.Time `gorm:"autoUpdateTime"`
}
```

在 `Message` 增字段：

```go
type Message struct {
    ID         string `gorm:"primaryKey"`
    SessionID  string `gorm:"index"`
    Role       string `gorm:"index"`
    Content    string `gorm:"type:text"`
    ToolCalls  string `gorm:"type:text"`
    Timestamp  int64  `gorm:"index"`
    StopReason string `gorm:"default:'stop'"`
    ParentID   string `gorm:"index"`
    Done       bool   `gorm:"default:false"`  // ← 新增
    Error      *string                          // ← 新增；nullable
    ToolLabel  *string                          // ← 新增；nullable
}
```

`SQLiteMessageStore.Save` 现在 INSERT-or-replace 走 PK，但本 refactor 还要支持"按 id 追加 content"和"按 id 标 done / error"，新加 3 个方法（FR-4 / FR-5 用）。

新建 `app_state` 表（在 `models.go` 同 SCHEMA / AutoMigrate 区域）：

```go
type AppState struct {
    Key   string `gorm:"primaryKey"`
    Value string `gorm:"type:text"`
}
func (AppState) TableName() string { return "app_state" }
```

当前只存 `active_session_id` 一行；schema 留出扩展能力。

### FR-2 Go 侧 `SessionStore` 接口扩展

在 `src/darvin-agent/internal/agent/store/store.go` 加方法（保持 interface 不变，老方法签名不动）：

```go
type SessionStore interface {
    // 已有
    Save(ctx context.Context, s *session.Session) error
    Load(ctx context.Context, id string) (*session.Session, error)
    List(ctx context.Context) ([]session.SessionMeta, error)
    Delete(ctx context.Context, id string) error

    // 新增
    ListAll(ctx context.Context) ([]Session, error)               // 给 RPC handler 用，返带 Title 的完整行
    GetByID(ctx context.Context, id string) (Session, error)     // 同上，单条
    UpdateTitle(ctx context.Context, id, title string) error
    UpdateStatus(ctx context.Context, id, status string) error
    SetClaudeSessionID(ctx context.Context, id string, claudeID *string) error
    Touch(ctx context.Context, id string, ts int64) error         // 刷 updated_at
    SearchByTitle(ctx context.Context, query string) ([]Session, error)
    SearchByContent(ctx context.Context, query string, limit int) ([]SearchHitRow, error)
}
```

`Session` 在 store package 内重导出（已经在 `models.go` 里的 `type Session struct{...}`）；handler 用它做 SQL 直接查询。`SessionMeta` 不带 Title 是给 agent 内部用的，对外 API 用完整的 `Session` 行。

`SearchHitRow` 简单结构体：

```go
type SearchHitRow struct {
    Message      Message
    SessionTitle string
}
```

`MessageStore` 加 3 个方法：

```go
type MessageStore interface {
    Save(ctx context.Context, m *MessageRecord) error
    List(ctx context.Context, sessionID string, limit, offset int) ([]MessageRecord, error)
    Count(ctx context.Context, sessionID string) (int, error)

    // 新增
    AppendContent(ctx context.Context, messageID, delta string) error  // streaming 累加
    MarkDone(ctx context.Context, messageID string) error               // done=1
    MarkError(ctx context.Context, messageID, errMsg string) error       // done=1, error=errMsg
}
```

`AppendContent` 在 SQLiteMessageStore 里走 `UPDATE messages SET content = content || ? WHERE id = ?`（与 Electron 现状一致）。

### FR-3 Go 侧 `app_state` 帮手

新建 `src/darvin-agent/internal/agent/store/app_state.go`：

```go
type AppStateStore struct {
    db *gorm.DB
}

func NewAppStateStore(db *gorm.DB) *AppStateStore { ... }

func (s *AppStateStore) GetActiveSession(ctx context.Context) (string, error) {
    var row AppState
    err := s.db.WithContext(ctx).First(&row, "key = ?", "active_session_id").Error
    if errors.Is(err, gorm.ErrRecordNotFound) { return "", nil }  // 空状态，caller 走 fallback
    if err != nil { return "", err }
    return row.Value, nil
}

func (s *AppStateStore) SetActiveSession(ctx context.Context, id string) error {
    return s.db.WithContext(ctx).
        Save(&AppState{Key: "active_session_id", Value: id}).Error
}
```

启动期 main.go 调 `appState.GetActiveSession` → 若非空，记到 `SessionManager` / `factory` 里（具体给谁记见 FR-9）。这样 Go 端知道用户上次停在哪个 session。

### FR-4 Go 侧 streaming delta 落库 hook

在 `src/darvin-agent/internal/agent/agent.go`（或新文件 `text_delta_hook.go`）加一个 bus 订阅：

```go
// TextDeltaHook 订阅 bus 上的 text_delta 事件，按 messageID 把 delta
// 追加到 messages.content。Session 维度的过滤：EventCommon.SessionID
// 必须等于本 Agent 的 session.ID，避免多 session 串扰。
type TextDeltaHook struct {
    msgStore store.MessageStore
    logger   *zap.Logger
}

func NewTextDeltaHook(ms store.MessageStore, log *zap.Logger) *TextDeltaHook { ... }

func (h *TextDeltaHook) Attach(a *Agent) {
    sub := a.Subscribe(64)
    go func() {
        for ev := range sub {
            td, ok := ev.(event.TextDeltaEvent)
            if !ok { continue }
            if td.SessionID != a.Session().ID { continue }
            if td.MessageID == "" { continue }
            if err := h.msgStore.AppendContent(context.Background(), td.MessageID, td.Delta); err != nil {
                h.logger.Warn("text delta persist failed",
                    zap.String("message_id", td.MessageID),
                    zap.Error(err))
            }
        }
    }()
}
```

`cmd/app/main.go` 在构造 Agent 时挂上（每 session 一个，per-session AcpSession 的 factory.Build 里挂）。

`MarkDone` / `MarkError` 不走 bus hook：现状是 `EventRouter` 收到 `done` / `error` 事件后写库。本 refactor 把"封口"的语义移到 dispatch 路径上：把 `dispatcher.go` 里 `a.bus.Emit(RunEndEvent{...})` 之后 / `AgentErrorEvent` 之前调 `msgStore.MarkDone(runMsgID)` / `MarkError(runMsgID, err.Error())`（持久化封口）。Renderer 端 `done` / `error` 事件通知流走 `EventRouter` 转发（FR-7），不再写库。

具体插入点（`src/darvin-agent/internal/agent/dispatcher.go`）：

```go
// 在 RunConversation 成功后，RunEndEvent emit 之前：
if a.msgStore != nil && runMsgID != "" {
    if err := a.msgStore.MarkDone(runCtx, runMsgID); err != nil {
        a.logger.Warn("mark message done failed", zap.String("message_id", runMsgID), zap.Error(err))
    }
}

// 在 err != nil 分支 emit AgentErrorEvent 之前：
if a.msgStore != nil && runMsgID != "" {
    errMsg := err.Error()
    if markErr := a.msgStore.MarkError(runCtx, runMsgID, errMsg); markErr != nil {
        a.logger.Warn("mark message error failed", zap.String("message_id", runMsgID), zap.Error(markErr))
    }
}
```

注意：封口时刻必须是"Run 整体结束"，而不是每个 sub-turn；保留 hook 2 `persistAssistantMessages` 走的多轮 tool_calls 路径（多次 sub-turn 之间 `done=false`），仅在最终 `RunEndEvent` 之前 `MarkDone` 一次。

### FR-5 Go 侧 RPC handler 新增

在 `src/darvin-agent/internal/gateway/handlers.go` 加新方法（沿用现有 JSON-RPC 框架）：

| RPC method | request | result | 行为 |
|------------|---------|--------|------|
| `agent.create_session` | `{ title?: string }` | `{ session: Session }` | 调 `factory.NewAcpSession(id)` + `store.UpdateTitle` + `appState.SetActiveSession`；返回新建 session 的完整字段（含 title） |
| `agent.list_sessions` | `{}` | `{ sessions: Session[] }` | 调 `store.ListAll` |
| `agent.get_active_session` | `{}` | `{ sessionId: string \| null }` | 调 `appState.GetActiveSession` |
| `agent.set_active_session` | `{ sessionId: string }` | `{ sessionId: string }` | `appState.SetActiveSession` + `store.Touch(id, now)` |
| `agent.delete_session` | `{ sessionId: string }` | `{ deleted: boolean, nextActiveSessionId: string \| null }` | 调 `SessionManager.Stop(id, runId)`（若 in-flight）+ `store.Delete`；nextActiveSessionId 从 `ListAll` 拿首条 |
| `agent.rename_session` | `{ sessionId, title }` | `{ session: Session }` | 调 `store.UpdateTitle` |
| `agent.search_sessions` | `{ query }` | `{ sessions: Session[], messages: SearchHit[] }` | 调 `store.SearchByTitle` + `store.SearchByContent` |
| `agent.get_messages` | `{ sessionId, limit?, offset? }` | `{ messages: MessageRecord[] }` | 已有；**调整为返 MessageRecord 全部字段**（含 Done / Error / ToolLabel，映射成 renderer DarvinMessage） |

`agent.prompt` / `agent.abort` / `agent.subscribe_events` / `agent.list_sessions`（**已有**，但需扩展返 Title）**不动签名**：`prompt` 返 `PromptResult{SessionID, RunID, MessageID, Queued}`；`abort` 返 `AbortResult`。

`Session` / `MessageRecord` 在 handler 层做一次 JSON 字段名映射（Go 默认 CamelCase → 与 darvin-api 一致），避免 renderer 端在 `getMessages` 后再改字段名。

新增 `Session` JSON shape（go side）：

```go
type SessionWire struct {
    ID              string  `json:"id"`
    Title           string  `json:"title"`
    UpdatedAt       int64   `json:"updatedAt"`
    Status          string  `json:"status"`
    ClaudeSessionID *string `json:"claudeSessionId"`
}
```

`MessageRecord` 已经是 wire shape；加 JSON tag：

```go
type MessageRecord struct {
    ID         string  `json:"id"`
    SessionID  string  `json:"sessionId"`
    Role       string  `json:"role"`
    Content    string  `json:"content"`
    ToolCalls  string  `json:"toolCalls,omitempty"`
    Timestamp  int64   `json:"createdAt"`     // ← 注意：对外叫 createdAt，与 DarvinMessage 对齐
    StopReason string  `json:"stopReason,omitempty"`
    ParentID   string  `json:"parentId,omitempty"`
    Done       bool    `json:"done"`           // ← 新增
    Error      *string `json:"error,omitempty"` // ← 新增
    ToolLabel  *string `json:"toolLabel,omitempty"` // ← 新增
}
```

> **注**：`Timestamp` 内部仍叫 `Timestamp`（避免改 dispatcher / Agent 其它引用点），仅 JSON tag 改 `createdAt`。这是 wire-shape 与 store-row 的标准分离（已存在于 `MessageRecord` vs `Message`）。

### FR-6 Electron 端 IPC handler 全部透传

`src/main/index.ts` 删 `SessionStore` 引用，IPC handler 改为 `client.request(...)`：

| IPC | 现 | 改后 |
|-----|----|------|
| `darvin:create_session` | `store.createSession` + `store.setActive` | `client.request('agent.create_session', { title })` |
| `darvin:list_sessions` | `store.listSessions` | `client.request('agent.list_sessions', {})` |
| `darvin:switch_session` | `store.setActive` | `client.request('agent.set_active_session', { sessionId })` |
| `darvin:delete_session` | abort + `store.deleteSession` | 调 abort（per-session FR）+ `client.request('agent.delete_session', { sessionId })` |
| `darvin:rename_session` | `store.updateTitle` | `client.request('agent.rename_session', { sessionId, title })` |
| `darvin:search_sessions` | `store.searchSessions` + `store.searchMessages` | `client.request('agent.search_sessions', { query })` |
| `darvin:get_active_session` | `store.getActive` | `client.request('agent.get_active_session', {})` |
| `darvin:get_messages` | `store.listMessages` | `client.getMessages(sessionId)`（已存在，扩 `MessageRecord` JSON tag 后字段自动对齐） |
| `darvin:prompt` | appendMessage(user) + `client.prompt` | 仅 `client.prompt` —— user message 落库由 Go 端 `persistUserMessage` hook 做（FR-4） |
| `darvin:abort` | `client.abort` | 不动 |
| `darvin:status` | 检查 binary + client.isConnected | 不动 |
| `darvin:get_llm_config` / `set_llm_config` | yaml + restartGoSubprocess | 不动 |
| `darvin:get_locale` / `set_locale` | yaml | 不动 |

broadcast 路径：`client.list_sessions` / `client.get_active_session` 成功 → 同步更新 in-memory 缓存（FR-8）→ 触发 `broadcastSessions` / `broadcastActiveSession`（与现状一致，但驱动来自 RPC 结果而非本地 store mutation）。

### FR-7 EventRouter 改为纯转发

`src/main/store/EventRouter.ts` 删 `store` 形参 / `applyToStore` 引用 / `appendMessageDelta` / `markMessageDone` / `markMessageError` 调用。

新形态：

```ts
import type { BrowserWindow } from 'electron';
import type { DarvinEvent } from '../../shared/darvin-api';
import { DarvinPushEvent } from '../../shared/darvin-api';
import type { AgentClient } from '../runtime/client';
import { notifyIfHidden } from '../libs/agentTaskNotifier';

interface Logger { warn(msg: string, ...args: unknown[]): void }

export class EventRouter {
  private client: AgentClient;
  private getWindow: () => BrowserWindow[];
  private logger: Logger;
  private unsubscribe: (() => void) | null = null;

  constructor(opts: { client: AgentClient; getWindows: () => BrowserWindow[]; logger?: Logger }) {
    this.client = opts.client;
    this.getWindow = opts.getWindows;
    this.logger = opts.logger ?? console;
  }

  start(): void {
    if (this.unsubscribe !== null) return;
    this.unsubscribe = this.client.onEvent((ev) => this.handle(ev));
  }

  stop(): void {
    if (this.unsubscribe === null) return;
    this.unsubscribe();
    this.unsubscribe = null;
  }

  handle(ev: DarvinEvent): void {
    if (ev.type === 'done') {
      const sessionId = ev.sessionId;
      if (sessionId) {
        for (const win of this.getWindow()) {
          notifyIfHidden({ win, sessionId, title: undefined });  // title 缓存 hit 时再补；本期传 undefined
        }
      }
    }
    for (const win of this.getWindow()) {
      if (win.isDestroyed()) continue;
      try { win.webContents.send(DarvinPushEvent.SessionEvent, ev); }
      catch (e) { this.logger.warn(`[eventrouter] send 失败: ${(e as Error).message}`); }
    }
  }
}
```

`src/main/index.ts` 同步删 `new SessionStore(sessionStorePath())` 一行，删 `store.bootstrapActiveSession` / `store.close` / `store.appendMessage` / `store.markMessageError` / `store.markMessageDone` 等所有调用点。

### FR-8 Electron 端 in-memory 缓存（Go 离线回退）

`src/main/index.ts` 新增模块级缓存：

```ts
interface CacheState {
  sessions: DarvinSession[] | null;            // 最近一次 list_sessions
  activeSessionId: string | null | undefined;  // 最近一次 get_active_session；undefined = 还没查过
  messagesBySession: Map<string, DarvinMessage[]>; // 最近一次 get_messages(sid)
  // 缓存为只读 fallback，写操作必须等 RPC 成功后才更新
}

const cache: CacheState = {
  sessions: null,
  activeSessionId: undefined,
  messagesBySession: new Map(),
};

function updateCacheFromListSessions(sessions: DarvinSession[]): void {
  cache.sessions = sessions;
  for (const s of sessions) {
    if (!cache.messagesBySession.has(s.id)) {
      // 旧 cache 不动，避免覆盖更新的 get_messages 结果
    }
  }
}

function updateCacheFromGetMessages(sid: string, msgs: DarvinMessage[]): void {
  cache.messagesBySession.set(sid, msgs);
}
```

`darvin:list_sessions` / `darvin:get_messages` / `darvin:get_active_session` 的 handler 在 `client.isConnected() === false` 时回退到 cache（无值返空 / null）；否则照常走 RPC，成功后写回 cache。

写类操作（`create` / `rename` / `delete` / `switch`）不读 cache（语义错位就废了），离线时直接抛 `agent offline`。

### FR-9 Go 启动期 active session 同步

`cmd/app/main.go` 启动顺序追加：

```go
// 1. 现有：建 factory / sessions / ledger / handler
// 2. 启动后做一次 bootstrap：
appState := store.NewAppStateStore(database.Get())
if id, err := appState.GetActiveSession(ctx); err == nil && id != "" {
    // 命中 → 把 id 灌进 SessionManager（仅 SessionEntry，不建 AcpSession）
    if _, _, err := sessions.CreateOrGet(id); err != nil {
        log.Warn("bootstrap active session failed", zap.Error(err))
    }
}
// 3. 启 WS server
```

`CreateOrGet` 是 per-session spec 里的两阶段入口（subscribe 走该路径），创建轻量 SessionEntry，不建 AcpSession。active session 的事件流由 subscribe 路径在 main 端 connect → `subscribeAllSessions` 时已经覆盖。

### FR-10 EventRouter 内部 title 缓存

FR-7 删了 EventRouter 内部对 `store.getSession` 的引用，`notifyIfHidden` 拿不到 title。补一个 main 进程级 `Map<sessionId, string>`，在 `client.list_sessions` 成功响应时填；`notifyIfHidden` 调用时从该 Map 读 title。Map 在 RPC handler 里跟 cache 同步更新（同一次 `updateCacheFromListSessions` 内填）。

`src/main/libs/agentTaskNotifier.ts` 现有签名不变（`title?: string`），main 端在调用前 lookup。

---

## 4. 实现方案

### 4.1 Go 端 schema 迁移（`models.go` + `migrate.go`）

- `models.go` 增字段（FR-1）。
- 新建 `app_state.go`（FR-3）。
- 启动期 AutoMigrate 已覆盖所有改动；不写手写 SQL 迁移（`gorm.io/gorm/auto_migrate` 已在用）。

### 4.2 Go 端 store 扩展（`store.go` / `sqlite_store.go` / `message_store.go`）

- `store.go` 加 interface 方法（FR-2）。
- `sqlite_store.go` 实现新方法：用 `s.db.WithContext(ctx)` + 简单 GORM 查询；`SearchByTitle` / `SearchByContent` 走 `Where("title LIKE ?", "%"+q+"%")` / `Where("content LIKE ?", ...)`。
- `message_store.go` 加 `AppendContent` / `MarkDone` / `MarkError`（FR-2）。
- 新建 `app_state.go`（FR-3）。

### 4.3 Go 端 streaming 落库 hook（`agent/text_delta_hook.go` 新增）

- 新建 `text_delta_hook.go`，实现 `TextDeltaHook`（FR-4）。
- `cmd/app/main.go` 在 `factory.NewAcpSession` 内部挂上 hook（与 `AttachMessageIDSrc` / `AttachRunIDSrc` 同位置）。
- `dispatcher.go` 的 `RunConversation` 成功路径插 `MarkDone`、error 路径插 `MarkError`（FR-4）。

### 4.4 Go 端 RPC handler（`gateway/handlers.go`）

- 加 7 个新 RPC（FR-5）：`agent.create_session` / `agent.list_sessions` / `agent.get_active_session` / `agent.set_active_session` / `agent.delete_session` / `agent.rename_session` / `agent.search_sessions`。
- 已有 `agent.get_messages` 调整：handler 层 `mapToDarvinMessage(MessageRecord)` 字段映射（FR-5 wire shape）。
- 已有 `agent.list_sessions` 调整：从返 `[]SessionMeta` 改为返 `[]SessionWire`（带 title）。

### 4.5 Electron 端 `main/index.ts` 重写 IPC handler（FR-6 / FR-8 / FR-10）

- 删 `import { SessionStore }` + 全部 `store.*` 调用。
- 加 `cache` 模块级 state。
- 改 8 个 IPC handler（FR-6 表）。
- 加 `darvin:list_sessions` / `darvin:get_messages` / `darvin:get_active_session` 离线回退分支。

### 4.6 Electron 端 `EventRouter` 简化（FR-7）

- 删 `applyToStore` 函数。
- 删 `store` 字段 / 形参。
- 删 `notifyIfHidden` 的 `sess?.title` 读，改由 main 端 lookup。
- main 端 `EventRouter` 构造点 `new EventRouter({ store, client, ... })` 改为 `new EventRouter({ client, ... })`。

### 4.7 Electron 端 `user-paths.ts` 收尾

- `sessionStorePath()` 删除（Electron 端不再需要；`user-paths.ts` 不再 export 这个函数）。
- `agentSessionsDsnPath()` 不变。
- 落地 PR 期间同时人工 `rm <userData>/darvin-agent/darvin-cowork.sqlite` 清掉旧文件（dev 阶段无迁移，下一次启动以空 db 开始）。

### 4.8 旧 `SessionStore.ts` 删除

- 删 `src/main/store/SessionStore.ts`。
- 删 `src/main/store/EventRouter.ts` 里的 `applyToStore` 引用。
- 删 `src/main/index.ts` 里的 `SessionStore` import + `new SessionStore(...)` + `store.bootstrapActiveSession` / `store.close` / `store.*` 调用。
- 删 `src/main/index.ts` 里的 `currentRunIdBySessionId` —— 不再需要，abort runId 直接由 prompt 时 main 端透传给 client.abort。

### 4.9 测试

#### 4.9.1 Go 端

| 测试 | 覆盖 |
|------|------|
| `store/sqlite_test.go::TestSessionStore_NewFieldsRoundTrip` | Title / ClaudeSessionID / Status 写入并读回 |
| `store/sqlite_test.go::TestSessionStore_TouchUpdatesOnlyUpdatedAt` | Touch 不改 Title |
| `store/sqlite_test.go::TestSessionStore_SearchByTitleAndContent` | 含 SQL 注入字符的 query 不报错；空 query 返空 |
| `store/message_store_test.go::TestMessageStore_AppendContent` | 同 id 多次 append 顺序追加；空 delta 不报错 |
| `store/message_store_test.go::TestMessageStore_MarkDoneAndError` | done=true；error 字段写入并读回 |
| `store/app_state_test.go::TestAppStateStore_ActiveSessionRoundTrip` | 写入后 GetActiveSession 命中；不存在的 key 返空 |
| `agent/text_delta_hook_test.go::TestTextDeltaHook_AppendsToMatchingSession` | bus 发 `text_delta(SessionID=A, msgID=M, delta="hi")` → message M 的 content 含 "hi"；另一 session 的 text_delta 不影响 |
| `agent/text_delta_hook_test.go::TestTextDeltaHook_IgnoresEmptyMessageID` | 不会 panic / 不会写空行 |
| `gateway/handlers_test.go::TestHandler_CreateSession` | 走 factory，返带 title 的 session；SetActiveSession 持久化 |
| `gateway/handlers_test.go::TestHandler_ListSessionsReturnsTitle` | mock store 返空时 handler 返空 |
| `gateway/handlers_test.go::TestHandler_DeleteSessionAdvancesActive` | 删 active 时 nextActiveSessionId 是 list 里的次条；最后一条删完返 null |
| `gateway/handlers_test.go::TestHandler_RenameUpdatesTitle` | 空 title fallback 到 '新建会话' |
| `gateway/handlers_test.go::TestHandler_SearchReturnsBothBuckets` | 标题命中 + 内容命中合并 |

#### 4.9.2 Electron 端

仓库未配置 vitest（AGENTS.md:79-83），本 spec 不强加 vitest，但 main 端的关键逻辑（cache 写入 / 离线回退）建议加一个手测 checklist（见 §7.3）。

#### 4.9.3 手工 playwright-cli

见 §7.3 验证计划。

### 4.10 文件清单汇总

见 §6。

---

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| **Go 进程未启动时 renderer 调 `listSessions`** | main 端 cache 为空 → 返 `{ sessions: [] }`；status banner 标"未连接" |
| **Go 进程未启动时 renderer 调 `getMessages(sid)`** | main 端 cache 命中 → 返缓存；不命中返 `{ messages: [] }` |
| **Go 进程未启动时 renderer 调 `prompt`** | 直接抛 `agent offline`（`client.prompt` 内部已 throw） |
| **Go 进程未启动时 renderer 调 `switchSession` / `renameSession` / `deleteSession` / `createSession`** | 同 prompt，抛 `agent offline` |
| **`sessions.db` 写锁冲突** | GORM 自动重试；连续失败 5 次后 LogError，但**不**影响用户请求（write hook 是 fire-and-forget） |
| **text_delta 落库时 msgStore 临时不可用** | `TextDeltaHook` 内 `AppendContent` 失败只 Warn，不影响 event 推送；下次同 messageId 的 delta 仍会触发 Append（累加是幂等的，delta 之间是累加语义）；最终 `MarkDone` 也会再走一次保证封口 |
| **user 在 prompt 还没收到第一个 `text_delta` 时切到别处** | 不变：切 session 只切 active；当前 session 仍在跑；事件通过 EventLedger 路由到该 session 的订阅者，per-session spec 已有该语义 |
| **user 在 prompt 中途崩 Electron** | sessions.db 中 user 完整、assistant 截至崩溃时已累加的 content、`done=false`；下次启动 `getMessages` 返这些，renderer 把 assistant 标"未完成"；下一条 prompt 续上 |
| **user 装老版本后回到新版本** | 老版本不识别 `sessions.db` 的新字段（Title / Done / Error / ToolLabel / app_state），GORM 老 schema 不动；新版本启动发现旧 db 缺字段 → AutoMigrate 加列，**不**丢数据 |
| **user 从更新版回到老版本** | 老版本不识别新字段；Better-sqlite3 老 schema 缺列，读 Title 字段会拿到默认值；不至于崩；老版本能继续用但会丢新数据 |
| **dev 阶段残留的旧 `darvin-cowork.sqlite`** | 本 spec 不读不迁不删；落地 PR 期间人工 `rm <userData>/darvin-agent/darvin-cowork.sqlite` 清掉；不写自动清理逻辑（避免误删用户数据，dev 阶段全权交给开发者） |
| **active session 在 Go 端 `app_state` 里指到一个不存在的 session** | 启动期 CreateOrGet 仍成功（创建轻量 SessionEntry）；`switchSession` 时 main 端 `agent.set_active_session` 写回；GetMessages 拿空 list（store 查不到）；不视为 fatal |
| **tool_call 落库** | Go 端现有 `persistAssistantMessages` 已 JSON-marshal 进 `ToolCalls`；renderer 不消费；保留 |
| **跨 session 串扰 text_delta** | `TextDeltaHook` 内按 `EventCommon.SessionID == a.Session().ID` 过滤 |
| **`markMessageDone` 落到错的 message** | runMsgID 是本 run 的 id；dispatcher.go:111 之后用本地变量；与 persistAssistantMessages 共享 runMsgID 不会错 |
| **bus 事件 + msgStore 写的竞态** | bus 是 `event.Bus`（已线程安全）；msgStore 是 SQLiteMessageStore（同一 *gorm.DB，SQLite 自身串行）；AppendContent 走 `UPDATE ... SET content = content || ?` 是原子的，delta 不会丢 |
| **Eslint / Go vet 不通过** | 落地前 `cd src/darvin-agent && go vet ./...` + `npm run lint` 必须全绿 |

---

## 6. 涉及文件

### 新增

| 文件 | 说明 |
|------|------|
| `src/darvin-agent/internal/agent/store/app_state.go` | `AppStateStore` + `GetActiveSession` / `SetActiveSession` |
| `src/darvin-agent/internal/agent/store/app_state_test.go` | app_state 单测 |
| `src/darvin-agent/internal/agent/text_delta_hook.go` | `TextDeltaHook`（订阅 bus → msgStore.AppendContent） |
| `src/darvin-agent/internal/agent/text_delta_hook_test.go` | hook 单测 |

### 修改

| 文件 | 变更 |
|------|------|
| `src/darvin-agent/internal/agent/store/models.go` | `Session` 加 `Title` / `ClaudeSessionID`；`Message` 加 `Done` / `Error` / `ToolLabel`；新增 `AppState` struct + TableName |
| `src/darvin-agent/internal/agent/store/store.go` | `SessionStore` interface 加 `ListAll` / `GetByID` / `UpdateTitle` / `UpdateStatus` / `SetClaudeSessionID` / `Touch` / `SearchByTitle` / `SearchByContent` |
| `src/darvin-agent/internal/agent/store/sqlite_store.go` | 实现上面 8 个新方法 |
| `src/darvin-agent/internal/agent/store/sqlite_test.go` | 加新方法单测 |
| `src/darvin-agent/internal/agent/store/message_store.go` | `MessageRecord` 加 JSON tag（含 `done` / `error` / `toolLabel` / `createdAt`）；interface 加 `AppendContent` / `MarkDone` / `MarkError` |
| `src/darvin-agent/internal/agent/store/message_store_test.go` | 加新方法单测 |
| `src/darvin-agent/internal/agent/dispatcher.go` | `RunConversation` 成功路径加 `MarkDone`；error 路径加 `MarkError` |
| `src/darvin-agent/internal/agent/agent.go` | `TextDeltaHook.Attach` 在 factory.Build 末尾调 |
| `src/darvin-agent/internal/gateway/handlers.go` | 加 7 个 RPC；调整 `agent.list_sessions` 返 `[]SessionWire`；`agent.get_messages` 返 `MessageRecord` JSON |
| `src/darvin-agent/internal/gateway/handlers_test.go` | 新增 5 组单测 |
| `src/darvin-agent/cmd/app/main.go` | 删单例 Agent；构造 `AppStateStore`；bootstrap active session；给 factory.NewAcpSession 加 `TextDeltaHook.Attach` |
| `src/main/index.ts` | 删 `SessionStore` import / 引用；改 8 个 IPC handler；加 cache 模块；加 `Map<sessionId, string>` 缓存 title；`currentRunIdBySessionId` 仍保留（per-session spec 已经从 main 端拿，refactor 期间不破坏现有逻辑；但其实 client.prompt 已经把 runId 透给 main 端了，**保留**作为 prompt 期间 abort 的 buffer 即可） |
| `src/main/store/EventRouter.ts` | 删 `store` 形参；删 `applyToStore`；`notifyIfHidden` 不再读 `sess?.title` |
| `src/main/libs/user-paths.ts` | 删 `sessionStorePath()` export（Electron 端不再用） |
| `src/shared/darvin-api.ts` | 不动（FR-5 wire shape 已经在现有 shape 内） |
| `src/preload/index.ts` | 不动 |
| renderer 全部 | 不动 |

### 删除

| 文件 | 说明 |
|------|------|
| `src/main/store/SessionStore.ts` | 整体删除；行为迁到 Go 侧 + main 端 cache |

---

## 7. 验收标准

### 7.1 自动化 / 静态检查

- [ ] `cd src/darvin-agent && go vet ./...` 通过
- [ ] `cd src/darvin-agent && go build ./...` 通过
- [ ] `cd src/darvin-agent && go test ./...` 全包通过（包含新增 / 修改的 5 个测试文件）
- [ ] `npm run lint` 通过（AGENTS.md:82 CI 入口）
- [ ] `npm run build:agent` 在本机平台产出 `bin/darvin-agent-<platform>-<arch>`

### 7.2 新增单测（FR 对应）

- [ ] `store/sqlite_test.go::TestSessionStore_NewFieldsRoundTrip`
- [ ] `store/sqlite_test.go::TestSessionStore_TouchUpdatesOnlyUpdatedAt`
- [ ] `store/sqlite_test.go::TestSessionStore_SearchByTitleAndContent`
- [ ] `store/message_store_test.go::TestMessageStore_AppendContent`
- [ ] `store/message_store_test.go::TestMessageStore_MarkDoneAndError`
- [ ] `store/app_state_test.go::TestAppStateStore_ActiveSessionRoundTrip`
- [ ] `agent/text_delta_hook_test.go::TestTextDeltaHook_AppendsToMatchingSession`
- [ ] `agent/text_delta_hook_test.go::TestTextDeltaHook_IgnoresEmptyMessageID`
- [ ] `gateway/handlers_test.go::TestHandler_CreateSession`
- [ ] `gateway/handlers_test.go::TestHandler_ListSessionsReturnsTitle`
- [ ] `gateway/handlers_test.go::TestHandler_DeleteSessionAdvancesActive`
- [ ] `gateway/handlers_test.go::TestHandler_RenameUpdatesTitle`
- [ ] `gateway/handlers_test.go::TestHandler_SearchReturnsBothBuckets`

### 7.3 手工验证（playwright-cli attach 到 Electron DevTools）

按场景跑：

- [ ] **冷启动**（干净 userData，无 `darvin-cowork.sqlite`）→ renderer `window.darvin.listSessions()` 返 `[]`；创建 session 后 list 返 1 条，title 为输入值；active 为该 session。
- [ ] **冷启动**（带旧 `darvin-cowork.sqlite`）→ spec **不**读不迁不删旧文件；新 db 由 GORM AutoMigrate 建空 schema，renderer list 返 `[]`；dev 阶段由开发者人工 `rm` 清掉旧文件即可。
- [ ] **发 prompt**（在线）→ console 看 `text_delta` 事件实时累加；mid-stream kill Go 进程 → 重启后 getMessages 返部分内容 + `done=false`；renderer 把 assistant 标未完成。
- [ ] **发 prompt** → 完成后 list 刷新（updated_at 推到顶）；`getMessages` 拿到 `done=true`。
- [ ] **rename** → 立即在 sidebar 看到新 title；刷新后保持。
- [ ] **delete**（非 active）→ list 少一条；active 不变。
- [ ] **delete**（active）→ list 少一条；active 自动切到次条；最后一条删完 active 变 `null`。
- [ ] **search** → 输入 "kubernetes" 返 title 命中 + content 命中分组；空 query 返空。
- [ ] **active session 持久化** → 发完 prompt 后退出；重启后 active 仍为最后那个 session。
- [ ] **Go 离线**（手工 kill Go 子进程）→ renderer list / getMessages / search 走 cache 不报错；status banner 标 "offline"；prompt 抛 `agent offline`。
- [ ] **set_llm_config** 触发 restartGoSubprocess → 重启后 list / getMessages 仍能正常工作（不丢 cache，subscription 重新建立）。
- [ ] **多 session 并发**（沿用上一轮 spec §7.3 场景 1-7 全部）→ 与 per-session spec 行为一致；本 refactor 不破坏 per-session 行为。
- [ ] **EventRouter 不再写库** → 在 main 进程的 stderr 找不到 `applyToStore` / `appendMessageDelta` 等调用；新加 `text_delta` 事件流是 EventRouter → renderer 一条线，没有 main 端落库再 query 的回路。
- [ ] **数据库文件收敛** → `<userData>/darvin-agent/` 下只有 `sessions.db`（AutoMigrate 新 schema、空内容）；`darvin-cowork.sqlite` 已由开发者人工删除（dev 阶段无自动清理）。

### 7.4 兼容性回归

- [ ] renderer 端 `DarvinApi` 全部 method 签名不变
- [ ] `DarvinEvent` schema 不变
- [ ] IPC channel 名不变（`darvin:create_session` / `darvin:list_sessions` / ...）
- [ ] push 事件 channel 名不变（`darvin:push:sessions-changed` / `darvin:push:active-session-changed` / `darvin:push:session-event`）
- [ ] preload 形状不变
- [ ] `DarvinSession` 字段不变（id / title / updatedAt / status / claudeSessionId）
- [ ] `DarvinMessage` 字段不变（id / sessionId / role / content / done / error / toolLabel / createdAt）
- [ ] 旧 session 行（迁移前）由 dev 阶段人工删除；不验证迁移正确性

### 7.5 非目标确认

- [ ] 不引入多 DB / 远端 DB
- [ ] 不改 renderer 行为
- [ ] 不迁 `tool_calls` / `parent_id` / `stop_reason` 到 renderer
- [ ] 不重写 dispatcher 3 个 hook 主逻辑
- [ ] 不迁 EventLedger fanout 语义
- [ ] 不加 vitest 也不手写 Electron 端单测（仓库未配置；AGENTS.md:79-83；行为靠 §7.3 手工 + Go 单测 + lint 覆盖）

---

## 附录 A：与上一轮 spec 的关系

| 上一轮 spec 落地 | 本 spec 接力 |
|------------------|--------------|
| per-session AcpSession（每 session 自己的 Agent + Loop）| 沿用，factory 注入 `TextDeltaHook` 不破坏 per-session 模型 |
| `EventCommon.SessionID` 来自 session 自己的 Agent | 沿用，hook 按 `EventCommon.SessionID == a.Session().ID` 过滤 |
| SessionManager LRU/TTL/stoppedUntilMs | 沿用，`agent.delete_session` 仍走 `SessionManager.Stop` 后 `store.Delete` |
| PromptResult.Queued | 沿用 |
| `SessionEntry.activeRun` 字段已删 | 沿用 |

不重做上一轮已落地的任何内容；本 spec 唯一新增的是"把 `EventRouter` 的 store 写入路径迁到 Go 端"。

## 附录 B：实施拆分（建议 PR 顺序）

按依赖顺序拆 3 个 PR，便于 review / 回滚 / 分批跑 playwright-cli 实测：

### PR 1：Go 侧 schema + store 扩展（无 IPC 行为变化）

- `models.go` 加字段
- `store.go` / `sqlite_store.go` / `message_store.go` 加新方法
- `app_state.go` 新建
- `text_delta_hook.go` 新建 + dispatcher.go 插 `MarkDone` / `MarkError`
- 单测新增
- **不改** `main/index.ts` / `EventRouter.ts` / IPC；本 PR 跑通后 Go 端多写一些字段，但 Electron 端仍用自己的 db 写自己的 → 两边各写各的（短期可接受；PR 3 解决）

### PR 2：Go 侧 RPC handler + Go 启动期 bootstrap

- `gateway/handlers.go` 加 7 个新 RPC；调整 `agent.list_sessions` 返 SessionWire；`agent.get_messages` 返 MessageRecord JSON
- `cmd/app/main.go` 构造 `AppStateStore` + bootstrap active session
- 单测新增
- **不改** main 端；本 PR 跑通后 Go 端已经"全功能"，但 main 端没切过来；可以用手测 RPC 验证

### PR 3：Electron 端切到 Go + EventRouter 简化 + 旧 SessionStore 删除

- `main/index.ts` 改 8 个 IPC handler + 加 cache + title map
- `EventRouter.ts` 删 `store` 形参
- `src/main/store/SessionStore.ts` 删除
- `user-paths.ts` 加 deprecated 注释
- 跑 `go vet` + `go test` + `npm run lint` + `npm run build:agent` + §7.3 全部手测

> 拆分理由：PR 1 是纯新增 + dispatcher 微调，行为对外不变；PR 2 是 Go 侧完整能力补充，仍不触及 main；PR 3 才是真正的"main 切到 Go"，是行为变更。三步都可独立回滚。

## 附录 C：风险与回滚

- **PR 1 / PR 2 风险低**：纯 Go 端能力扩展；老 main 端不会用到（main 端没有调这些新方法），但 Go 端已经在用新 schema 落库；**回滚** = `git revert PR`。
- **PR 3 风险中**：把 Electron 端整段切到 Go 侧。如果发现 Go 端有性能 / 并发 / 错误处理问题，回滚 = `git revert PR 3`；落库是 fire-and-forget，回滚后丢的是 renderer view 短暂不可见。
- **数据丢失风险**：
  - dev 阶段不迁历史数据。PR 1 / PR 2 期间 Go 写新 schema 的 `sessions.db`、main 写老 schema 的 `darvin-cowork.sqlite`，两边互不影响（旧 db 后续不再被读；新 db 是干净空 schema）。
  - PR 3 落地后开发者人工 `rm <userData>/darvin-agent/darvin-cowork.sqlite` 清掉旧文件；本 spec **不**写自动删除逻辑（避免误删用户数据）。
