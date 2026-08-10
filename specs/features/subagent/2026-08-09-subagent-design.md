# Sub-agent 实现设计文档

> 起源：`specs/features/lobster-comparison/subagent.md` paper-level 设计稿。
> 本 spec 是启动实现前的最终设计稿，整合 LobsterAI（OpenClaw Gateway `sessions_spawn` 模式）与 DeepSeek-Reasonix（`subagent_store` + `SubagentScheduler` + `foregroundOnlyBash` 模式）调研结果。

## 1. 概述

### 1.1 问题 / 动机

darvin-agent 当前是「单 Agent per session」模型：每个 session 持有 1 个 `*agent.Agent`，run 串行 turn 队列执行（`internal/agentloop/loop.go`）。当任务变复杂时，主 agent 不得不自己顺序处理所有子任务，context 被工具结果填满、效率被 LLM 串行思考拖慢。

参考实现：

- **LobsterAI** (`docs/MULTI_AGENT.md`)：`sessions_spawn` tool 起 sub-agent，sub-agent 是 OpenClaw Gateway 的 child session（命名 `agent:<id>:subagent:<uuid>`），持久化 `subagent_runs` + `subagent_messages` 两张表，stale-record 启动时恢复。
- **DeepSeek-Reasonix** (`internal/agent/task.go` + `subagent_store.go` + `scheduler.go`)：5 个工具（`task` / `read_only_task` / `parallel_tasks` / `fleet` / `read_subagent_result`），full nested `Session` 持久化为 `.jsonl` + `.meta.json`，`SubagentScheduler` 槽位管理 + 写路径协调，context 隔离（sub-agent 看不到父 conversation），`SubagentDepth` 上下文追踪，`WithDestroyedChecker` 父死检测，`CleanupStaleRunning` 启动时修复 stale，`foregroundOnlyBash` 包装禁止 sub-agent 起 bg job。

darvin 这边 `src/renderer/services/i18n.ts` 已经有 `artifact.special.subagents` = "子代理能力尚未接入" 占位字符串，UI 已有入口在 `plus.*`，但 Go 端完全没实现。`internal/agents/ctxengine/ctxengine.go:41-43` 已经预留了 `PrepareSubagentSpawn` / `OnSubagentEnded` 接口但默认实现返回 `ErrSubAgentUnsupported`。

### 1.2 目标

1. 主 agent 暴露 5 个内置工具：
   - `delegate_subagent`：默认同步阻塞；传 `run_in_background=true` 异步返回 job_id
   - `list_subagents`：列出当前 session 派生出的所有 sub-agent run
   - `abort_subagent`：终止某 sub-agent run
   - `parallel_subagents`：一次性并行 spawn N 个（1-64），全部阻塞到完成
   - `read_subagent_result`：字节偏移分页读 sub-agent 完整结果
2. **完全上下文隔离**：sub-agent 只看 system prompt + workspace context + 自己收到的 prompt，看不到主 agent 对话历史。
3. 同步阻塞为主路径；并行靠 `parallel_subagents` 一次传 `tasks[]`。
4. session evict 时 sub-agent 跟着终止，不泄漏。
5. 持久化 sub-agent run 到 SQLite，UI 可浏览历史（UI 阶段另起 spec）。
6. per-session 并发上限 8（与 `jobs` 一致），>8 排队。

### 1.3 非目标

- 不做 sub-agent 之间的互相通讯（每个 sub-agent 独立，只与主 agent 通信）。
- 不做跨主 session 的 sub-agent 共享（sub-agent 生命周期 ⊆ 主 session）。
- 不做 sub-agent 自动拆分（主 agent 自己决定何时调 `delegate_subagent`）。
- 不做 sub-agent marketplace（本 spec 之内，scope / model 由主 agent 显式传）。
- 不做 sub-agent 嵌套（深度硬限 1：通过 scope 排除所有 subagent 工具 + 移除 `shell run_in_background`）。
- 不动 renderer / i18n（UI 阶段另起 spec，本 spec 只交付 Go 后端）。
- 不动 `ctxengine.PrepareSubagentSpawn` / `OnSubagentEnded` 接口（保留 seam，本 spec 不接）。

## 2. 现状分析

| 现状 | 说明 |
|---|---|
| 单 Agent per session | `internal/agentloop/loop.go:82` `Loop` drives one `agent.Agent` serially |
| Turn queue（串行） | `loop.go:92` `steerQueue` + `followUpQueue`，单 goroutine 消费 |
| Skill session | `loop.go:140` `SubmitSkill` 走 mini loop，skill-prompt + 限定 tool set |
| Tool registry | `protocol.ToolRegistry`（interface）；main agent 用全量，skill 用 scoped |
| MCP bridge | `internal/tools/mcp.go` 给主 agent 暴露 `mcp__<server>__<tool>` |
| Session 持久化 | `internal/agents/store/` + `database/` 走 SQLite；`store.go` 定义 interface，SQLite impl 走 GORM `glebarez/sqlite` |
| Agent 装配 | `internal/agents/agent.go:147` `Agent` struct；`NewAgentConfig` 注入依赖；`executor.Deps` 提供 session 基础设施 |
| 包边界 | Makefile `lint-agents-boundaries` 禁 `agents/` 引 capability 包；`internal/subagent` 不在禁列（按现状禁列只覆盖 `llm / tools / skills / mcp / acp / gateway / commands / memory / cron`），但本 spec 需要 sub-agent 反向构造 `*agent.Agent` → 通过 `protocol/` 接口注入避免循环 |
| `ctxengine` seam | `ctxengine/subagent.go` 已预留 `PrepareSubagentSpawn` / `OnSubagentEnded`，默认返回 `ErrSubAgentUnsupported`；本 spec 不接此 seam |

## 3. 方案设计

### 3.0 概念模型

```
Main session (S1)
  └─ Agent M1 (持有 tools, instructions, ctx)
       │
       │ tool call: delegate_subagent(prompt="...", description="research-A", scope=["read_file","grep"])
       │
       ├─ Subagent S1.1 (session id: "S1/sub/<rand>")
       │    └─ Agent M1.1 (scoped tools, fresh context, isolated system prompt)
       │        │ sub-tool call(s) ...
       │        └─ result: {text, tool_calls: [...], duration_ms}
       │
       │ tool call: parallel_subagents(tasks=[{prompt:"...",description:"a"},{prompt:"...",description:"b"}])
       │
       ├─ Subagent S1.2 (并行,scoped tools)
       ├─ Subagent S1.3 (并行,scoped tools)
       │   ... 全部完成后回传 N 段结果
       │
       └─ result back to M1
```

**关键不变量**：

- sub-agent 的 session id 命名空间是 `<parentSessionID>/sub/<random>`，DB 主键唯一。
- sub-agent 的 tool registry 是 main 的子集（`scope` 参数 + 默认白名单）。
- sub-agent 的 model 可以与 main 不同（`Spec.Model` 覆盖）。
- sub-agent 的 instructions 是默认 system prompt + `<subagent-context>` XML 块（描述工作目录等元信息），**不含**父 conversation 历史。
- sub-agent 不能 spawn（scope 排除所有 subagent 工具 + `shell run_in_background`）。
- sub-agent 的事件回放走 `MessageStore`（按 sub-agent 自己的 session id 分桶），不上父 session 流。

### 3.1 数据结构

#### 3.1.1 GORM model（`internal/agents/store/models.go` 新增）

```go
type Subagent struct {
    ID             string    `gorm:"primaryKey"`              // "<parentSessionID>/sub/<rand>"
    ParentID       string    `gorm:"index"`                   // parent session id
    Status         string    `gorm:"default:'pending'"`       // pending|running|done|error|aborted|timeout
    Prompt         string
    Description    string                                       // 3-7 字短标签,主 agent 必填
    ScopeJSON      string                                       // JSON-encoded []string
    Model          string
    StartedAt      time.Time `gorm:"autoCreateTime"`
    EndedAt        time.Time
    ResultText     string                                       // final assistant text(截断到 16 KiB 用于存储)
    FullResultPath string                                       // file path to full result JSONL(默认空,off by default)
    ToolCalls      int
    ErrorMsg       string
    Depth          int       `gorm:"default:0"`                // nesting depth(0 = top-level)
}

func (Subagent) TableName() string { return "subagent_runs" }
```

#### 3.1.2 Store interface（`internal/agents/store/subagent_store.go` 新增）

```go
type SubagentStore interface {
    Insert(ctx context.Context, run Subagent) error
    Update(ctx context.Context, run Subagent) error
    Get(ctx context.Context, id string) (Subagent, error)
    ListByParent(ctx context.Context, parentID string) ([]Subagent, error)
    ListStaleRunning(ctx context.Context, before time.Time) ([]Subagent, error)
    Delete(ctx context.Context, id string) error
    DeleteByParent(ctx context.Context, parentID string) error
}
```

SQLite 实现走 `glebarez/sqlite` + GORM（与现有 `message_store.go` 模式一致）。

#### 3.1.3 `internal/subagent/` 包结构

```
internal/subagent/
  manager.go        // Manager / Spec / Info / Status
  scope.go          // 默认 scope / 工具过滤
  run.go            // 跑 sub-agent run(建子 Agent + 驱动 Run)
  context.go        // WithContext / FromContext
  result.go         // 结果格式化 + 分页
  manager_test.go
  scope_test.go
  run_test.go
```

#### 3.1.4 核心类型（`manager.go`）

```go
type Status string
const (
    StatusPending Status = "pending"
    StatusRunning Status = "running"
    StatusDone    Status = "done"
    StatusError   Status = "error"
    StatusAborted Status = "aborted"
    StatusTimeout Status = "timeout"
)

type Spec struct {
    Prompt          string
    Description     string          // 3-7 字短标签
    Scope           []string        // 工具名 whitelist;空 = 默认只读
    Model           string          // 空 = 继承父
    RunInBackground bool            // true 异步,立即返回 job_id
    TimeoutMs       int             // 默认 300000,max 600000
}

type Info struct {
    ID          string
    ParentID    string
    Status      Status
    Prompt      string
    Description string
    Scope       []string
    Model       string
    StartedAt   time.Time
    EndedAt     time.Time
    ResultText  string
    ToolCalls   int
    ErrorMsg    string
    DurationMs  int64
}

type Deps struct {
    Store           store.SubagentStore
    Provider        llm.Provider              // 复用父 session 的 LLM provider
    DefaultModel    string                    // 空 Spec.Model 时回退
    SessionFactory  func(id string) session.Session  // 构造子 session
    ToolRegistry    protocol.ToolRegistry      // 父的全量 tool registry,供 scope 过滤
    ParentDestroyed func() bool                // Reasonix WithDestroyedChecker
    MaxConcurrent   int                        // 默认 8
    ResultBufCap    int                        // 默认 1 MiB/run
}

type Manager struct {
    parentSessionID string
    deps            Deps
    parentDestroyed func() bool
    store           store.SubagentStore
    maxConcurrent   int
    mu              sync.Mutex
    runs            map[string]*runState      // id → state
    waiters         map[string][]chan Info    // Wait 订阅者
    resultBuf       map[string]*resultBuffer  // 完整结果(per run,cap 默认 1 MiB)
    scheduler       *scheduler                // 槽位管理
    ctx             context.Context
    cancel          context.CancelFunc
    closed          bool
}

func NewManager(parentSessionID string, deps Deps) *Manager
func (m *Manager) Spawn(ctx context.Context, spec Spec) (*Info, error)        // 同步:阻塞到完成;异步:返回 running + id
func (m *Manager) List() []Info
func (m *Manager) Get(id string) (Info, error)
func (m *Manager) Abort(id string) error
func (m *Manager) ReadResult(id string, offset, limit int) (string, error)
func (m *Manager) Wait(id string, timeout time.Duration) (Info, error)
func (m *Manager) Close()                                                  // 终止所有 running run,幂等
```

### 3.2 工具面（主 agent 视角）

5 个工具全部走 `subagent.FromContext(ctx)` 拿 manager；manager 缺失统一报 `"subagent support not available in this context"`。

| 工具 | 参数 | 行为 | 输出格式 |
|---|---|---|---|
| `delegate_subagent` | `prompt` (string, 必填) / `description` (string, 必填) / `scope` (string[]) / `model` (string) / `run_in_background` (bool, default false) / `timeout_ms` (int, default 300000, max 600000) | `Spawn`（同步阻塞）/ `SpawnAsync`（立即返回 running） | 同步：`[subagent <id> status: done \| <ms>ms \| <n> tool calls]\n<final text>`；异步：`started background subagent <id>` |
| `list_subagents` | 无 | `List()` | 每行 `<id>  <description>  <status>  <ms>ms`（按 started_at desc） |
| `abort_subagent` | `id` (string, 必填) | `Abort` | `aborted subagent <id>` |
| `parallel_subagents` | `tasks` (array, 1-64 项) / `description` (string) | 并行 spawn N 个 sub-agent，**全部同步阻塞**到完成 | `[<id> status: done \| ...]\n<text1>\n---\n[<id> status: error \| ...]\n<err>\n---\n...`（按 tasks 顺序输出） |
| `read_subagent_result` | `ref` (string, 必填) / `offset_bytes` (int, default 0) / `limit_bytes` (int, default 12288, max 24576) | `ReadResult` 分页 | raw 文本 + `[offset X, returned Y of Z bytes]` 头 |

### 3.3 Scope 设计

`scope` 是工具名白名单；不传时**默认走「safe read-only」**：

- ✅ always allow：`read_file`, `grep`, `glob`, `list_dir`, `web_fetch`, `code_index`（只读 action：`search` / `info` / `outline`）
- ❌ default 排除（除非显式 allow）：所有 `write_file` / `edit_file` / `delete_range` / `multi_edit` / `move_file` / `notebook_edit` / `delete_symbol` / `code_index` 写 action
- ❌ **永远** 排除：`shell`（防止 spawn 攻击 + bash bg 失控）、所有 `subagent_*` 工具（`delegate_subagent` / `list_subagents` / `abort_subagent` / `parallel_subagents` / `read_subagent_result`）、`bash_output` / `kill_shell` / `wait`（jobs 工具，避免 sub-agent 起 bg job）
- 显式 `scope=["shell"]` 时允许 shell（**危险**，工具 description 警告 + 日志记 warning）

scope 校验失败时工具返回 `"tool <name> not allowed in subagent scope"` 错误，不向上抛 panic。

### 3.4 装配

#### 3.4.1 `internal/agents/agent.go`

- `Agent` struct 新增字段 `subagents *subagent.Manager`。
- `NewAgentConfig` 不直接接 `Subagents`（避免构造时机过早）。
- 新增 `func (a *Agent) AttachSubagents(sm *subagent.Manager)`：由 `agentloop` factory 装配时调用。
- 新增 `func (a *Agent) Subagents() *subagent.Manager`：永远非 nil；首次调时若未 Attach，懒构造（拿 `a.SubagentStore()` / `a.LLMProvider()` 等依赖），便于 main agent 在无 AgentLoop 上下文测试时仍可用。

#### 3.4.2 `internal/agents/executor/executor.go`

- `Deps` interface 新增 `Subagents() *subagent.Manager`。
- `executeOneTool` 把 manager 印入 `tctx`（仅当 `tctx` 中没有时，避免覆盖 async 路径的 ctx）。

#### 3.4.3 `internal/agentloop/factory.go`

- `AgentFactory` 加 `SubagentStore store.SubagentStore` 字段。
- `Build()` 在 `agent.New` 之后调 `a.AttachSubagents(subagent.NewManager(...))`：

```go
sm := subagent.NewManager(sessionID, subagent.Deps{
    Store:           f.SubagentStore,
    Provider:        f.Provider,
    DefaultModel:    f.Model.Model,
    SessionFactory:  func(id string) session.Session { return session.NewSession(id) },
    ToolRegistry:    f.ToolRegistry,
    ParentDestroyed: func() bool { return a.IsClosed() },
    MaxConcurrent:   8,
    ResultBufCap:    1 << 20, // 1 MiB
})
a.AttachSubagents(sm)
```

#### 3.4.4 `internal/agentloop/session.go`

- `AgentLoopSession.Close()` 在 `Loop.Close()` 之后追加 `if sm := s.Agent.Subagents(); sm != nil { sm.Close() }`。

### 3.5 持久化

- `internal/agents/store/subagent_store.go` 走 SQLite + GORM（与 `message_store.go` 同模式）。
- 落地时机：`Spawn` 插 `pending` → 启动后改 `running` → 终止时改终态（`done` / `error` / `aborted` / `timeout`）。
- 失败不回滚（失败也是状态）。
- 启动时 `Manager.New` 跑 `ListStaleRunning(time.Now().Add(-1 * time.Hour))`，所有 stale `running` 改 `error` + `error_msg="interrupted by restart"`（Reasonix `CleanupStaleRunning` 模式）。
- 完整结果（超 `ResultText` 截断阈值）默认**不落盘**，留在内存 `resultBuf`（cap 1 MiB/run）；`FullResultPath` 字段预留 off-by-default 落盘开关，本 spec 不实现。

### 3.6 并发模型

- `Spawn` 立即返回 `Info{Status: Running, ID: ...}`，后台 goroutine 跑 `runSpec`。
- 主 agent 调 `delegate_subagent` 时（同步模式），**工具实现侧**调 `Wait(id, spec.TimeoutMs)`，阻塞到完成 / 超时 / abort。
- LLM 想并行：使用 `parallel_subagents(tasks=[...])` 一次传 N 项；工具实现侧起 N 个 goroutine + sync.WaitGroup，等全部完成。
- 同一 turn 内多次调 `delegate_subagent`（不带 `run_in_background`）也可触发并行（executor 走 `runToolsParallel` + `wg.Wait`），但 LLM 不会自动这么做，需在工具 description 里写「需要并行时使用 `parallel_subagents`」。
- `MaxConcurrent=8` 限流：超限时 `scheduler` 排队（FIFO），`Spawn` 立即返回 running，但 `runSpec` 启动时 `acquireSlot` 阻塞。

### 3.7 失败 / 错误

| 场景 | 行为 |
|---|---|
| Spec.Prompt 空 | `Spawn` 返回 error，工具不上 spawn |
| Spec.Description 空 | 工具自动填 `<id>` 短前缀；DB 存空字符串 |
| Scope 校验失败 | `runSpec` 启动时立即标 `error` + errorMsg |
| Manager.Spawn 内部错 | `Spawn` 返回 error，工具不返回 id |
| sub-agent run panic | recover 住，DB 标 `error` + stacktrace，移除运行 entry |
| 父 session evict | `AgentLoopSession.Close` 链式调 `Subagents().Close` → 全 running 标 `aborted` |
| parentDestroyed=true | `runSpec` 启动时检查；true 即 no-op + 标 `aborted` |
| 超时 | `runSpec` 用 `context.WithTimeout`，到时 cancel sub-agent run + 标 `timeout` |
| 进程重启 | `Manager.New` 跑 `ListStaleRunning` 修复 stale |

### 3.8 UI

参考 LobsterAI (`specs/features/subagent-artifact-panel/2026-07-01-subagent-artifact-panel-design.md` + `src/renderer/components/artifacts/SubagentPanelContent.tsx` + `src/renderer/utils/subagentDisplay.ts` + `src/renderer/components/cowork/SubagentTurnLinks.tsx`) 的「artifact 右侧 panel 特殊 tab」模型。darvin renderer 已预留 `ArtifactSpecialTab.Subagents` 枚举 + `artifact.special.subagents` i18n key（`composables/useArtifacts.ts:24,117` / `services/i18n.ts:320`）以及 placeholder 渲染（`components/side-panel/ArtifactPanel.vue:98-103`），本 spec 把这块从 placeholder 落到完整 UI 实现稿，**与 Go 后端一起在本 spec 内交付**。

#### 3.8.1 总体架构

**不是** sidebar 子行、**不是** 独立页面、**是** artifact 右侧 panel 的新 special tab（与 File List / Browser 平级，复用现有 artifact 区域的 tab / fallback 机制）。

| 选择 | 理由（参考 LobsterAI 经验） |
|---|---|
| 不放 sidebar 子行 | sidebar 层级变深 + 与主 Agent 会话列表混淆；切换主会话时 sidebar/subagent 状态易空白/不同步 |
| 不做独立页面 | 点击 subagent 跳走整页后，从 subagent 详情回主会话时 sidebar 状态易错 |
| artifact tab | 复用现有 artifact tab 优先级 / fallback 机制（artifact → browser → file list → 关闭 panel） |
| 不放 chat 流折叠 | 折叠模式会污染主会话正文 + tool call 块布局；artifact tab 是独立信息通道 |

LobsterAI 早期版本试过 chip + 跳整页 + sidebar 子行，被替换成 artifact tab，理由相同。

#### 3.8.2 三种入口

| 入口 | 触发 | 行为 |
|---|---|---|
| **artifact 加号菜单** | 右侧 panel 右上角 `+` 菜单加 "子代理" 项（轻量机器人线性图标，复用 `assets/icons/subagents.svg`） | 右侧 panel 打开 Subagents special tab + 列表态；无 run 时显示空态 |
| **主会话工具调用 chip** | `delegate_subagent` / `parallel_subagents` 工具调用块下方展示 `SubagentChip`（按 toolCallId 关联 run，**不集中在 assistant turn 末尾**） | 点击 chip → 右侧 panel 切到 Subagents tab + 详情态，**不跳页** |
| **delegate_subagent 完成后自动跳** | sync 模式下主 agent 拿到 result 后自动激活 Subagents tab（避免用户手动找） | 若 Subagents tab 未开则开 + 进入详情；若已开在别的 subagent 上则切到新；同 id no-op |

#### 3.8.3 列表态（`SubagentRunList.vue`）

按状态三组分组（顺序：running → done → error，每组按 `startedAt` desc），与 LobsterAI `SubagentSection` 同构：

```
[running 2]
  ⏳ research-stock-A   2,341ms (进行中)
  ⏳ fetch-docs-B       1,820ms (进行中)

[done 3]
  ✅ analyze-repo       12,341ms · 5 tool calls
  ✅ parse-config       8,022ms · 2 tool calls
  ✅ list-tmp           1,512ms · 1 tool calls

[error 1]
  ❌ scrape-page        4,102ms · shell permission denied
```

- 每行字段（4 字段）：`initial` 圆形头像 + `displayName` + `statusDot`（running=blue animate-pulse / done=green / error=red / aborted=gray）+ 右侧 `duration`（running 显示 i18n `artifact.subagents.section.running`，否则 `formatDuration(startedAt, endedAt)`）+ 副标题 `task` 摘要（error 组额外展示 `errorMsg`）
- 分组 sticky header：`sticky top-0 bg-background z-10`（沿用 LobsterAI `SubagentSection` 行为）
- 列表项点击 → 进入详情态
- 空态："当前会话暂无子代理"（`artifact.subagents.empty`）
- 加载态：spinner + "加载中…"（复用 `chat.loading` i18n key）

`formatDuration(startedAt, endedAt)` 走 i18n `formatRelativeTime` 简单封装（ms → "<60s → 'Ns' / <60m → 'Nm' / <24h → 'Nh' / 'Nd'"）；不在 darvin 引入相对时间自定义格式，避免和现有 `formatRelativeTime` 双轨。

#### 3.8.4 详情态（`SubagentRunDetail.vue`）

只读展示 sub-agent 会话历史，结构对标 LobsterAI `SubagentDetailContent`：

- 顶部 bar：返回按钮 + `initial` 头像 + `displayName` + `task` 摘要（仅当非空）+ 右侧 `status badge`（dot + i18n 标签）
- 主区滚动容器：复用主 artifact panel 滚动容器（`ref="contentRef"`，`scrollTop = scrollHeight` 在 `messages.length` 增长时自动滚底；与 LobsterAI 同步）
- 内容：`MessageList.vue` 子集（**read-only 模式**，无 composer / 无 artifact 卡片 action）；history 为空 + task 非空 → 合成只读 user message（id `synthetic-task`，type `user`，content `task`，timestamp `startedAt`）
- 轮询：`status === 'running'` 时每 5s 间隔 `loadMessages` + `refreshList`；变终态 `clearInterval`

`isStreaming` prop 透传给 `MessageList` 子集；streaming 时显示 `cursor-blink` token。

#### 3.8.5 显示名优先级（`composables/useSubagents.ts`）

对齐 LobsterAI `src/renderer/utils/subagentDisplay.ts`：

```ts
function getSubagentDisplayName(run: SubagentRun): string {
  const description = run.description?.trim();
  if (description) return description;
  const id = run.id?.trim();
  if (!id) return t('artifact.subagents.placeholder');  // 兜底
  return id.length > 8 ? id.slice(0, 8) : id;  // id 短前缀
}

function getSubagentDisplayInitial(run: SubagentRun): string {
  return getSubagentDisplayName(run).slice(0, 1).toUpperCase() || 'S';
}
```

`description` > `<id>` 短前缀 > i18n 兜底（参考 LobsterAI `label > taskName > agentId` 链；darvin 退化为 `description > id`，无 taskName 概念）。

#### 3.8.6 SubagentChip（`components/chat/SubagentChip.vue`）

工具调用块下方的 chip，结构对标 LobsterAI `SubagentTurnLinks`：

```vue
<template>
  <button
    v-for="run in subagents"
    :key="run.id"
    type="button"
    class="inline-flex h-7 items-center gap-1.5 rounded-full border border-border bg-surface px-2.5 text-xs text-text-muted transition-colors hover:border-primary hover:text-text"
    :aria-label="t('chat.subagent.chip.open')"
    :data-testid="'subagent-chip-' + run.id"
    @click="open(run)"
  >
    <span
      class="h-1.5 w-1.5 shrink-0 rounded-full"
      :class="dotClass(run.status)"
    />
    <Icon name="subagents" :size="12" class="shrink-0" />
    <span class="truncate max-w-[160px]">{{ displayName(run) }}</span>
    <span v-if="run.status === 'running'" class="text-text-subtle">·</span>
    <span v-if="run.status === 'running'" class="text-text-subtle">{{ t('artifact.subagents.section.running') }}</span>
  </button>
</template>

<script setup lang="ts">
import { useArtifacts, ArtifactSpecialTab } from '../../composables/useArtifacts';
import { useSubagents } from '../../composables/useSubagents';
import { t } from '../../services/i18n';
import Icon from '../common/Icon.vue';

const props = defineProps<{ subagents: SubagentRun[]; toolCallId: string }>();
const artifacts = useArtifacts();
const subagents$ = useSubagents(toRef(() => /* parent session id */));

function open(run: SubagentRun) {
  artifacts.activateTab(/* parent session id */, ArtifactSpecialTab.Subagents);
  subagents$.selectRun(run.id);
}
</script>
```

集成点：`ToolCallGroup.vue` 在 `delegate_subagent` / `parallel_subagents` 工具调用块内（`<template>` 内嵌 `<SubagentChip>`，**不**放在 assistant turn 末尾），通过 toolCallId 拿 `useSubagents().runs.filter(r => r.toolCallId === toolCallId)` 关联。

LobsterAI `variant: 'turn' | 'tool'` 双形态在本 spec 简化为单 `tool` 形态（darvin 不在 turn 末尾展示 chip）；以后真有需求再加 turn 形态。

#### 3.8.7 composable `useSubagents`

`src/renderer/composables/useSubagents.ts` 单一职责：

```ts
export interface SubagentRun {
  id: string;
  parentSessionId: string;
  status: 'pending' | 'running' | 'done' | 'error' | 'aborted' | 'timeout';
  description: string;
  toolCallId?: string;  // 来自 delegate_subagent tool result，便于 chip 反查
  prompt: string;       // 用于历史空时合成 user message
  scope: string[];
  model: string;
  startedAt: number;
  endedAt: number | null;
  toolCalls: number;
  errorMsg: string;
}

export interface SubagentMessage {
  id: string;
  type: 'user' | 'assistant' | 'tool_use' | 'tool_result' | 'system';
  content: string;
  toolName?: string;
  toolCalls?: Array<{ name: string; input: unknown }>;
  timestamp: number;
}

export function useSubagents(parentSessionId: MaybeRefOrGetter<string>) {
  const runs = ref<SubagentRun[]>([]);
  const loading = ref(false);
  const selectedId = ref<string | null>(null);
  const messagesByRun = ref<Record<string, SubagentMessage[]>>({});

  async function refreshList(): Promise<void> {
    const sid = toValue(parentSessionId);
    if (!sid) return;
    loading.value = true;
    try {
      const next = await window.darvin.subagentList(sid);
      runs.value = next ?? [];
    } finally {
      loading.value = false;
    }
  }

  async function loadMessages(runId: string): Promise<void> {
    const ms = await window.darvin.subagentGetMessages(runId);
    messagesByRun.value = { ...messagesByRun.value, [runId]: ms ?? [] };
  }

  function selectRun(id: string | null): void {
    selectedId.value = id;
    if (id && !messagesByRun.value[id]) void loadMessages(id);
  }

  // 轮询：running 状态下 5s 间隔；终态 stop
  let timer: ReturnType<typeof setInterval> | undefined;
  function startPolling() { if (!timer) timer = setInterval(refreshList, 5_000); }
  function stopPolling() { if (timer) { clearInterval(timer); timer = undefined; } }
  watch(runs, (rs) => { rs.some(r => r.status === 'running') ? startPolling() : stopPolling(); });
  onBeforeUnmount(stopPolling);

  // 父 session 切换 → 重置
  watch(() => toValue(parentSessionId), () => {
    runs.value = [];
    selectedId.value = null;
    messagesByRun.value = {};
    stopPolling();
  });

  return { runs, loading, selectedId, messagesByRun, refreshList, loadMessages, selectRun };
}
```

**全局单例**：和 `useArtifacts` 一致，模块顶层 `parentSessionId` 通过 `useSession().activeSessionId` 取；不在 composable 内再起全局 ref。

#### 3.8.8 IPC 通道 + preload + shared API

新增 4 个 IPC 通道（仅 renderer 主动拉，sub-agent 状态变更仍走 `agent.event` push；不在 subagent 上单独起 push 通道）：

```ts
// src/shared/darvin-api.ts
interface DarvinApi {
  // ... existing ...
  subagentList(parentSessionId: string): Promise<SubagentRun[]>;
  subagentGetMessages(runId: string): Promise<SubagentMessage[]>;
  subagentAbort(runId: string): Promise<void>;
  subagentReadResult(runId: string, offset: number, limit: number): Promise<string>;
}
```

主进程 `src/main/index.ts` 注册 4 个 `ipcMain.handle` 通道；`src/main/runtime/client.ts` `AgentClient` 加对应 `request(method, params)` 调用。`src/preload/index.ts` 通过 `contextBridge` 暴露 `window.darvin.subagentList / subagentGetMessages / subagentAbort / subagentReadResult`，与现有 ~70 个 channel 一致。

#### 3.8.9 状态流

```
主 session 切换
    │
    ▼
useSubagents(parentSessionId) 自动重置 runs / selectedId / messagesByRun
    │
    ▼
ArtifactPanel 渲染 ArtifactSpecialTab.Subagents → SubagentPanelContent
    │
    ▼
list 态：runs.groupBy(status, desc) → SubagentRunList 三段（running/done/error）
    │
    │ 列表项 click
    ▼
detail 态：selectRun(id) → loadMessages(id)
    │
    │ running 状态 → 5s 轮询
    ▼
SubagentRunDetail：scrollable content + read-only MessageList 子集
    │
    │ 返回 click → selectRun(null)
    ▼
list 态
```

`useArtifacts.activateTab(sid, ArtifactSpecialTab.Subagents)` 在 chip click / add menu click 时调用，自动开 side panel（与现有 artifact tab 行为一致；`useArtifacts.ts:134-137` 已有 `activateTab`）。

`SubagentChip` → `useArtifacts.activateTab` + `useSubagents.selectRun` 双调用，确保 panel 切到 Subagents tab + 进入对应 detail。

#### 3.8.10 组件清单（**全部在本 spec 内交付**）

| 文件 | 角色 | 行数 |
|---|---|---|
| `src/renderer/components/side-panel/SubagentPanelContent.vue` | 列表态 + 详情态容器；按 `useSubagents.selectedId` 分发；对应 LobsterAI `SubagentPanelContent.tsx` | ~80 |
| `src/renderer/components/side-panel/SubagentRunList.vue` | 列表态三段分组（running/done/error）；对应 LobsterAI `SubagentSection` + `SubagentPanelRow` | ~150 |
| `src/renderer/components/side-panel/SubagentRunDetail.vue` | 详情态（顶部 bar + 滚动 content + 5s 轮询）；对应 LobsterAI `SubagentDetailContent` | ~120 |
| `src/renderer/components/chat/SubagentChip.vue` | 工具调用块下方 chip；对应 LobsterAI `SubagentTurnLinks` | ~80 |
| `src/renderer/components/common/SubagentIcon.vue` | 复用现有 `assets/icons/subagents.svg`，封装为 Vue 组件（当前 `.vue` 不存在；新建） | ~25 |
| `src/renderer/composables/useSubagents.ts` | list / select / messages / polling / session 重置；对应 LobsterAI `SubagentTracker` + `CoworkSessionDetail` subagent 切片 | ~150 |
| `src/renderer/composables/useSubagents.test.ts` | composable 单测：refreshList / selectRun / polling start/stop / session 切换重置 | ~120 |
| `src/renderer/components/side-panel/SubagentPanelContent.test.ts` | 列表分组 / 详情切换 / 空态 / 加载态单测 | ~80 |
| `src/renderer/components/chat/SubagentChip.test.ts` | chip click → activateTab + selectRun 联调单测 | ~50 |
| `src/renderer/components/side-panel/ArtifactPanel.vue` | 修改：placeholder 块替换为 `<SubagentPanelContent :session-id="..." />`；新增 Subagents 加号菜单入口（artifact `+` 加菜单项） | ~+30 |
| `src/renderer/services/i18n.ts` | 新增 ~10 个 `artifact.subagents.*` / `chat.subagent.*` key | +40 |
| `src/shared/darvin-api.ts` | 4 个新 IPC 接口 + `SubagentRun` / `SubagentMessage` 类型导出 | +60 |
| `src/main/index.ts` | 4 个新 `ipcMain.handle` 通道 | +40 |
| `src/main/runtime/client.ts` | `AgentClient.subagentList/GetMessages/Abort/ReadResult` 调用封装 | +30 |
| `src/preload/index.ts` | `contextBridge` 暴露 4 个 `window.darvin.subagent*` 方法 | +20 |

**UI 总工作量：~1080 行新增/修改**（含单测）。

#### 3.8.11 i18n keys

按现有 `artifact.special.*` / `artifact.fileList.*` 命名约定（**全部走 `t()`，保持 zh/en key 同步**）：

| key | zh | en |
|---|---|---|
| `artifact.special.subagents`（已有） | 子代理 | Subagents |
| `artifact.subagents.placeholder`（已有） | 子代理能力尚未接入（**保留作历史**） | （保留） |
| `artifact.subagents.empty` | 当前会话暂无子代理 | No subagents in this session |
| `artifact.subagents.section.running` | 运行中 | Running |
| `artifact.subagents.section.done` | 已完成 | Done |
| `artifact.subagents.section.error` | 出错 | Error |
| `artifact.subagents.section.aborted` | 已中止 | Aborted |
| `artifact.subagents.row.toolCalls` | {n} 个工具调用 | {n} tool calls |
| `artifact.subagents.row.elapsed` | {ms}ms | {ms}ms |
| `artifact.subagents.detail.back` | 返回列表 | Back to list |
| `artifact.subagents.detail.empty` | 该子代理暂无消息 | No messages yet |
| `chat.subagent.chip.open` | 查看子代理详情 | View subagent detail |
| `chat.subagent.menu.open` | 打开子代理面板 | Open subagents panel |

`{n}` / `{ms}` 用 `t('artifact.subagents.row.toolCalls', { n })` 插值，不在 template 内手拼字符串。`artifact.subagents.placeholder` 历史 key 不删（保持向后兼容，新版组件不再使用）。

#### 3.8.12 darvin vs LobsterAI 关键差异

| 项 | LobsterAI | darvin |
|---|---|---|
| 显示名优先级 | `label` > `taskName` > `agentId` | `description` > `<id>`（无 taskName 概念） |
| 多 Agent 体系 | `subagents.allowAgents` + `taskName` 路由 | 无；等价物是 `scope`（工具白名单） |
| Child session 持续对话 | subagent 可 materialize 为 Cowork child session | 无；sub-agent 跑完即结束 |
| Pending 追踪 | renderer 侧 `SubagentTracker`（tool result 晚到） | 无；Go 端 `Spawn` 同步走 DB |
| 状态轮询频率 | 5s | 5s（沿用） |
| sidebar 子行 | 早期有，2026-07 移除 | 本就不存在 |
| Self spawn 兼容 | `allowAgents` 自动补 self | **无**（深度硬限 1，sub-agent scope 排除所有 subagent 工具） |
| 协作 Agent 配置 UI | Agent 设置页「协作」Tab | 无（darvin 单一主 Agent，scope 即协作能力） |
| Chip variant | `turn` / `tool` 双形态 | 单 `tool` 形态（简化） |
| 状态机 | Redux + immer + slice | Vue `ref` + composable（沿用 `useArtifacts` 模式） |
| Icon library | `@heroicons/react` | 自定义 SVG（`assets/icons/subagents.svg`，遵守 `Icon.vue` 约定） |

#### 3.8.13 边界情况

| 场景 | 处理 |
|---|---|
| 当前会话无 subagent run | 空态 "当前会话暂无子代理" + 加号菜单入口仍可点开 |
| subagent 仍 running | 列表 + 详情都按 5s 间隔轮询；变终态后 `clearInterval` |
| subagent 历史为空但 task 存在 | 合成只读 user message 展示初始 prompt（id `synthetic-task`） |
| 用户关闭 Subagents tab | 按现有 fallback 规则切到下一个 artifact tab（artifact → browser → file list → 关闭 panel，`useArtifacts.ts:139-149` 已有） |
| sidebar 切换主会话 | `useSubagents` 监听 `parentSessionId` 变化自动清空 `runs` / `selectedId` / `messagesByRun` + `stopPolling` |
| 历史 data 缺 messages | 列表展示 run summary；详情合成 user message 兜底 |
| delegate_subagent 在 running 时被 abort | 列表行 status 切到 `aborted`；终态轮询停止 |
| sync delegate 完成后跳详情 | Subagents tab 未开则开 + 详情；已开在别的 subagent 上则切到新；同 id no-op |
| chip click 时 panel 已开在别的 tab | `activateTab` 切到 Subagents + `selectRun` 进详情，不影响已开 artifact 预览 tab 的存在（仅切 active） |
| 进程重启后历史 subagent run 显示 | `darvin.subagentList` 走 Go 端 `Manager.List()`，DB 查所有 non-pending run；状态由 `SubagentStore.ListByParent` 提供 |
| aborted / timeout run 仍在 list 中展示 | 保留为单独组（`aborted` 走 `done` 组前置或独立 `aborted` 组？**默认独立 `aborted` 组**，避免污染 done） |
| 多语言 fallback | en 字典与 zh 同步通过 `assertSameKeys` 兜底（已有） |

#### 3.8.14 UI 单测要点

`useSubagents.test.ts`（composable 单测 + vitest fake timers）：

- `refreshList` 调用 IPC 一次，`runs` 更新；error 不抛
- `selectRun(id)` 设置 `selectedId` + 触发 `loadMessages`（仅当 messages 缓存缺失）
- `selectRun(null)` 仅清 `selectedId`
- running run 存在 → `setInterval` 启动；全部 run 变终态 → `clearInterval`
- `parentSessionId` 变化 → `runs` / `selectedId` / `messagesByRun` 清空 + 轮询停止
- `onBeforeUnmount` 触发 `stopPolling`

`SubagentPanelContent.test.ts`：

- 列表态：3 段分组（running / done / error），每段按 `startedAt` desc
- 列表项 click → 切换详情态 + 触发 `loadMessages`
- 详情态：返回按钮 click → 切回列表态 + 清 `selectedId`
- 空态：`runs = []` → 显示空态文案
- 加载态：`loading = true && runs.length === 0` → 显示 spinner
- 历史空 + prompt 存在 → 合成 user message 显示
- 详情态轮询：running status → 5s 触发 `loadMessages` + `refreshList`；变终态停止

`SubagentChip.test.ts`：

- chip click → `useArtifacts().activateTab(parentSessionId, 'subagents')` + `useSubagents().selectRun(runId)`
- aria-label = `t('chat.subagent.chip.open')`
- status dot 颜色按 status 切换
- running status 显示「运行中」副标签

#### 3.8.15 端到端 UI 冒烟

1. `npm start` 起 Electron。
2. 主 agent 调 `delegate_subagent(prompt="...", description="test-A")`。
3. 工具调用块下方出现 chip（`subagent-chip-<id>`），显示 `test-A` + 蓝色 dot + "运行中"。
4. 点击 chip：右侧 panel 切到 Subagents tab + 进入该 run 详情（**主页面不跳走**）。
5. 5s 后详情刷新一次；run 结束后轮询停止。
6. 点击 chip 顶部返回 → 列表态 + 该 run 在 `done` 组顶部。
7. 加号菜单打开 artifact panel → 选 "子代理" → 列表态显示。
8. sidebar 切到另一个主 session → Subagents tab 重置（runs 清空）。
9. 关闭 Subagents tab → 切到下一个 artifact tab 或关闭 panel。

## 4. 涉及文件

| 文件 | 变更 | 大小 |
|---|---|---|
| `specs/features/subagent/2026-08-09-subagent-design.md` | **新增**（本文件，从 `lobster-comparison/subagent.md` 抽出并更新） | ~400 行 |
| `specs/features/lobster-comparison/subagent.md` | 删除（已抽出到独立 spec） | -290 行 |
| `specs/features/lobster-comparison/CHECKLIST.md` | subagent 项标「抽到独立子 spec」+ 链接 | +5 行 |
| `internal/subagent/manager.go` | 新增：Manager / Spec / Info / Status / Deps | ~250 行 |
| `internal/subagent/scope.go` | 新增：默认 scope + 工具过滤 | ~100 行 |
| `internal/subagent/run.go` | 新增：跑一个 sub-agent run | ~200 行 |
| `internal/subagent/context.go` | 新增：WithContext / FromContext | ~30 行 |
| `internal/subagent/result.go` | 新增：结果格式化 + 分页 | ~80 行 |
| `internal/subagent/manager_test.go` | 新增 | ~250 行 |
| `internal/subagent/scope_test.go` | 新增 | ~100 行 |
| `internal/subagent/run_test.go` | 新增 | ~150 行 |
| `internal/agents/store/models.go` | 新增 `Subagent` GORM model | +30 行 |
| `internal/agents/store/subagent_store.go` | 新增 `SubagentStore` + SQLite impl | ~150 行 |
| `internal/agents/store/subagent_store_test.go` | 新增 | ~150 行 |
| `internal/runtime/database.go` | 注册迁移 + `Stores.Subagents` | +20 行 |
| `internal/agents/agent.go` | `subagents` 字段 + `Subagents()` + `AttachSubagents()` | +30 行 |
| `internal/agents/executor/executor.go` | `Deps.Subagents()` + 印 ctx | +10 行 |
| `internal/agentloop/factory.go` | `SubagentStore` 字段 + `Build` 构造 | +30 行 |
| `internal/agentloop/session.go` | `Close` 调 `Subagents().Close` | +5 行 |
| `internal/tools/subagent.go` | 5 个工具 | ~400 行 |
| `internal/tools/subagent_test.go` | 5 个工具单测 | ~300 行 |
| `internal/tools/registry_test.go` | 加 5 个工具到 want | +5 行 |
| `src/renderer/composables/useSubagents.ts` + `useSubagents.test.ts` | list / select / messages / polling / session 重置 | ~270 行 |
| `src/renderer/components/side-panel/SubagentPanelContent.vue` | 列表态 + 详情态容器 | ~80 行 |
| `src/renderer/components/side-panel/SubagentRunList.vue` | 列表态三段分组 | ~150 行 |
| `src/renderer/components/side-panel/SubagentRunDetail.vue` | 详情态（轮询 + 历史） | ~120 行 |
| `src/renderer/components/chat/SubagentChip.vue` | 工具调用块下方 chip | ~80 行 |
| `src/renderer/components/side-panel/SubagentPanelContent.test.ts` + `SubagentChip.test.ts` | 列表分组 / 详情切换 / chip 联调单测 | ~130 行 |
| `src/renderer/components/common/SubagentIcon.vue` | 复用 `assets/icons/subagents.svg` | ~25 行 |
| `src/renderer/components/side-panel/ArtifactPanel.vue` | placeholder 块替换为 `<SubagentPanelContent>` + 加号菜单入口 | +30 行 |
| `src/renderer/services/i18n.ts` | 新增 ~10 个 `artifact.subagents.*` / `chat.subagent.*` key | +40 行 |
| `src/shared/darvin-api.ts` | 4 个新 IPC 接口 + `SubagentRun` / `SubagentMessage` 类型导出 | +60 行 |
| `src/main/index.ts` | 4 个新 `ipcMain.handle` 通道 | +40 行 |
| `src/main/runtime/client.ts` | `AgentClient.subagentList/GetMessages/Abort/ReadResult` 调用封装 | +30 行 |
| `src/preload/index.ts` | `contextBridge` 暴露 4 个 `window.darvin.subagent*` 方法 | +20 行 |

**本 spec 落地总计（Go 后端 + renderer + IPC + 单测）：~3780 行新增/修改**

## 5. 验证计划

### 5.1 静态检查

1. `gofmt -l .` / `goimports -l .` 输出为空。
2. `go vet ./...` 零警告。
3. `staticcheck -checks 'ST10*' ./...` 零告警（ST1000/1003/1005/1006/1019/1020-1023）。
4. `golangci-lint run ./...` 零告警（baseline 不增）。
5. `make lint-agents-boundaries` 通过：
   - 确认 `internal/subagent` 不在禁列（`subagent` 包可被 `agents/` 通过 `Deps` interface 引用）。
   - 确认 `internal/subagent` 反向引 `internal/agents` 不触发（规则只限制 `agents/`，不限制其他包引 `agents/`）。

### 5.2 单元测试

`internal/subagent` 包：

- 默认 scope 拒绝 `write_file` / `shell` / 嵌套 spawn 工具
- `Spawn` 立即返回（同步 vs 异步）
- `Wait` 阻塞到完成 / 超时
- `Abort` 终止 running
- `Close` 终止所有 running，幂等（`Close` 调两次不 panic）
- `ReadResult` 分页正确（offset 越界 / limit 超 cap）
- `List` 快照（含已结束）
- 并发 `Spawn` 上限（>8 时排队 / 阻塞）
- `parentDestroyed=true` 时 `runSpec` 立即返回 error
- 启动时 `ListStaleRunning` 修复 stale

`internal/tools/subagent` 包：

- 5 个工具 schema 正确（JSON Schema 验证）
- 正常路径（sub-agent 跑简单 task，`delegate_subagent` 返回结果）
- 异常路径（manager 缺失 / `id` 不存在 / `parallel_subagents` 传 65 个超限 / `limit_bytes` > 24576 自动 clamp）
- 工具 description 含「需要并行时使用 `parallel_subagents`」提示

`internal/agents/store/subagent_store` 包：

- CRUD 全覆盖（Insert / Update / Get / ListByParent / ListStaleRunning / Delete / DeleteByParent）
- 并发读写不 race

### 5.3 端到端冒烟

1. `npm run build:agent` 编译成功。
2. `npm start` 拉起 Electron。
3. 主 agent 调 `delegate_subagent(prompt="list the /tmp directory", description="list-tmp")`（同 turn 也调 `read_file` 模拟并行）。
4. 验证：
   - sub-agent 在独立 session 里跑，DB 看到 `subagent_runs` 行（status `done`）。
   - 主 agent tool result 含 `[subagent <id> status: done | <ms>ms | <n> tool calls]` 头。
   - `list_subagents` 看到该 run。
   - `read_subagent_result` 分页正确。
5. 关闭主 session，验证 sub-agent 跟着 `aborted`（再开 Electron 看 DB）。

### 5.4 UI 端到端冒烟（对齐 § 3.8.15）

1. `npm start` 起 Electron。
2. 主 agent 调 `delegate_subagent(prompt="...", description="test-A")`。
3. 工具调用块下方出现 chip（`data-testid="subagent-chip-<id>"`），显示 `test-A` + 蓝色 dot + "运行中"。
4. 点击 chip：右侧 panel 切到 Subagents tab + 进入该 run 详情（**主页面不跳走**）。
5. 5s 后详情刷新一次；run 结束后轮询停止。
6. 点击详情顶部返回 → 列表态 + 该 run 在 `done` 组顶部。
7. 加号菜单打开 artifact panel → 选 "子代理" → 列表态显示。
8. sidebar 切到另一个主 session → Subagents tab 重置（runs 清空）。
9. 关闭 Subagents tab → 切到下一个 artifact tab 或关闭 panel。

### 5.5 静态检查（renderer）

1. `npm run lint` 零错误。
2. `npm run test` 全部通过（含新增的 `useSubagents.test.ts` / `SubagentPanelContent.test.ts` / `SubagentChip.test.ts`）。
3. zh / en i18n 字典 `assertSameKeys` 校验通过。

## 6. 风险 / 开放问题

1. **`subagent` 包反向依赖 `agents.Agent` 的循环** — `Manager` 需要构造子 `*agent.Agent`（用于跑 sub-agent run），但 `agents/` 又不能引 `subagent/`（边界规则只单向）。**方案**：`subagent` 直接引 `internal/agents`（规则不限其他包引 `agents/`，只限 `agents/` 引 capability 包），通过 `Deps.SessionFactory` / `Deps.ToolRegistry` 等接口注入子 session 构造逻辑，避免 `subagent` 知道 `*agent.Agent` 具体类型；子 Agent 构造走 `subagent` 包内部 `agent.New(...)` 调用，由 `agentloop` 装配时把 `Provider` / `ToolRegistry` 等已构造好的依赖传进来。
2. **子 Agent 的 session ID 命名** — 用 `<parentSessionID>/sub/<rand>`，DB 主键唯一；`<rand>` 走 `crypto/rand` 8 字节 hex。
3. **ctxengine `PrepareSubagentSpawn` seam** — 本 spec 不接；等真需要 sub-agent context 注入时再接。sub-agent 构造时用空 `Instructions()` + `<subagent-context>` XML 块手动拼 system prompt（`<workspace_root>` / `<parent_session_id>` / `<description>`）。
4. **shell 在 sub-agent 中的处理** — Reasonix 把 bash 包成 `foregroundOnlyBash` 禁止 `run_in_background`。darvin 现状是 `run_in_background` 还没实现（`specs/features/builtin-tools-c-bg-jobs/` 写了未落地）；sub-agent scope 默认排除 `shell`，等 jobs spec 落地后再考虑是否允许 + 是否需要包成 `foregroundOnly`。
5. **`parallel_subagents` 的并发上限** — per-session 8（与 jobs 一致），>8 排队；tasks[] 上限 64。
6. **stale 恢复的时机** — `Manager.New` 启动时跑一次；不动全局启动流程。
7. **sub-agent 消息持久化** — 走现有 `MessageStore`，按 sub-agent 自己的 session id 分桶，不写父 session 流；UI 阶段通过 `parent_id = <sub-agent id>` 过滤展示。
8. **token 消耗记账** — sub-agent run 的 token 算 sub-agent 自己的 `SessionUsage`（session id 即 sub-agent session id），主 session 不动；UI 阶段聚合展示。
9. **UI 暴露** — 本 spec 不动 renderer；先把 Go 端跑通，UI 阶段另起 spec（`specs/features/subagent-ui/...`）。

## 7. 落地顺序

1. **抽 spec**（本文件）：从 `lobster-comparison/subagent.md` 抽出并更新；`CHECKLIST.md` 标记「已抽到独立子 spec」。
2. **数据库层**：`internal/agents/store/models.go` 加 `Subagent` model + `subagent_store.go` + `subagent_store_test.go`；`internal/runtime/database.go` 注册迁移。
3. **`internal/subagent/` 包**：`manager.go` / `scope.go` / `run.go` / `context.go` / `result.go` + 单测。
4. **Agent / Executor / AgentLoop 装配**：agent.go / executor.go / factory.go / session.go 注入 + 印 ctx。
5. **工具实现**：`internal/tools/subagent.go` 5 个工具 + 单测。
6. **注册计数更新**：`internal/tools/registry_test.go` `want` map 加 5 个新工具名（14 → 19）。
7. **Go 后端验证**：5.1 静态检查 + 5.2 Go 单元测试 + 5.3 Go 端到端冒烟。
8. **IPC 通道**：`src/shared/darvin-api.ts` 加 4 个 IPC 接口 + `SubagentRun` / `SubagentMessage` 类型；`src/main/index.ts` `ipcMain.handle` 注册；`src/main/runtime/client.ts` `AgentClient` 调用封装；`src/preload/index.ts` `contextBridge` 暴露 `window.darvin.subagent*`。
9. **renderer composable + 组件**：`useSubagents.ts` + 单测；`SubagentPanelContent.vue` / `SubagentRunList.vue` / `SubagentRunDetail.vue` / `SubagentChip.vue` / `SubagentIcon.vue` + 单测；`ArtifactPanel.vue` 替换 placeholder + 加号菜单入口；i18n 字典扩充。
10. **renderer 验证**：`npm run lint` + `npm run test`（含 useSubagents / SubagentPanelContent / SubagentChip 单测）。
11. **端到端 UI 冒烟**：5.4 § 3.8.15 9 步流程。
12. **commit**（无 `Co-Authored-By`）。

## 8. 后续（不在本 spec）

- **跨主 session 共享 sub-agent**：当前 per-session 隔离；评估「sub-agent 模板」（用户保存「常用 sub-agent」scope + model 组合）后再启动。
- **sub-agent 自动拆分**：主 agent 不显式调，系统自动拆分复杂任务。
- **sub-agent 嵌套**：当前 scope 排除所有 subagent 工具（深度硬限 1）；评估 1 层嵌套后启动（对应 LobsterAI `subagents.allowAgents` + `requireAgentId` 机制）。
- **jobs spec 落地后**：`subagent` scope 是否允许 `shell`；是否需要包成 `foregroundOnly`（参考 Reasonix `foregroundOnlyBash`）。
- **sub-agent token 展示**：UI 阶段聚合 `SessionUsage` 展示（按 sub-agent session id 分桶）；评估在列表行 / 详情顶部加 token usage 行。
- **sub-agent 流式 push**：当前 list / detail 都走 5s 轮询；评估在 `agent.event` 增 `subagent_status` push 通道，减轮询频率。
- **sub-agent marketplace / 模板**：用户保存「常用 sub-agent 模板」直接复用（参考 LobsterAI 协作 Agent 配置）；spec 落地后另起 `specs/features/subagent-template/` 子 spec。
- **多 Agent 体系**：darvin 当前单一主 Agent；评估多 Agent 切换体系后，sub-agent 可路由到任意 Agent（参考 LobsterAI `taskName` → `agentId` 路由）。
- **SubagentChip turn 形态**：本 spec 仅 `tool` 形态（chip 在工具调用块内）；后续若需要在 assistant turn 末尾集中展示，再加 `variant: 'turn'` 双形态（参考 LobsterAI `SubagentTurnLinks.tsx`）。
