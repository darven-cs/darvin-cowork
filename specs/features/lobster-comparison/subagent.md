# SUBAGENT 设计稿

> Tier 3 之一。从 `CHECKLIST.md` § 3 Tier 3「Sub-agent」项抽出来的 paper-level 设计稿。
> 本稿是「设计起点」—— 启动实现前需先评审 + 独立成 `specs/features/subagent/...` 完整 design 文档。

## 1. 概述

### 1.1 问题

darvin-agent 当前是「单 Agent per session」模型：每个 session 持有 1 个 `*agent.Agent`,run 串行 turn 队列执行。当任务变复杂时,主 agent 不得不自己顺序处理所有子任务,context 被工具结果填满、效率被 LLM 串行思考拖慢。

参考 LobsterAI 的 `coworkSubagent` + `subagentMessageStore` + `subagentRunStore`：主 agent 可以**委派子任务**给 sub-agent,sub-agent 在自己的 session 里独立 run,结果回流给主 agent。常见场景：

- "读 5 个 PDF 各做摘要" → 5 个 sub-agent 并行,主 agent 拿到 5 段摘要
- "为某报告生成 docx + xlsx + pptx 三件" → 3 个 sub-agent 并行
- "对比 3 只股票基本面" → 3 个 sub-agent 并行
- "在 100 个 URL 里搜 X" → N 个 sub-agent 并行

darvin-agent 这边 `i18n.ts` 已经有 `artifact.special.subagents` = "子代理能力尚未接入" 占位字符串,UI 也已有入口在 `plus.*`,但 Go 端完全没实现。

### 1.2 目标

1. 主 agent 暴露 3 个工具:`delegate_subagent` / `list_subagents` / `abort_subagent`。
2. sub-agent 跑在独立的 `*agent.Agent` + `*session.Session` 里,自己的 tool set + context + model。
3. 多个 sub-agent 可并行;主 agent 的工具调用阻塞到 sub-agent 完成(或超时)。
4. sub-agent 走会话持久化,用户能在 UI 浏览历史 sub-agent run。
5. session evict 时 sub-agent 跟着终止,不泄漏。

### 1.3 非目标

- 不做「sub-agent 之间互相通讯」(每个 sub-agent 独立,只与主 agent 通信)。
- 不做「跨主 session 的 sub-agent 共享」(sub-agent 生命周期 ⊆ 主 session)。
- 不做 sub-agent 自动拆分(主 agent 自己决定何时调 `delegate_subagent`)。
- 不做 sub-agent 的 marketplace(本 spec 之内,无 UI 选择;主 agent 调 tool 时传 scope / model)。

---

## 2. 现状分析

| 现状 | 说明 |
|---|---|
| 单 Agent per session | `internal/agentloop/loop.go:82` `Loop` drives one `agent.Agent` serially |
| Turn queue (串行) | `loop.go:92` `steerQueue` + `followUpQueue`,单 goroutine 消费 |
| Skill session | `loop.go:140` `SubmitSkill` 走 mini loop,skill-prompt + 限定 tool set |
| Tool registry | `protocol.ToolRegistry` (interface);main agent 用全量,skill 用 scoped |
| MCP bridge | `internal/tools/mcp.go` 给主 agent 暴露 `mcp__<server>__<tool>` |
| Session 持久化 | `internal/agents/store/` + `database/` 走 SQLite,主 session 落地 |
| 包边界 | Makefile `lint-agents-boundaries` 禁 `agents/` 引 capability 包;**`internal/subagent` 不在禁列**,但要小心:sub-agent 本身会调主 agent 的协议层,接口设计要参考 `protocol/` 现有 contract |

参考实现 (LobsterAI):
- `src/main/coworkSubagent/` — IPC handlers
- `src/main/subagentMessageStore.ts` — sub-agent ↔ main session message routing
- `src/main/subagentRunStore.ts` — sub-agent run records (id / status / started_at / ended_at)
- IM gateway can route to sub-sessions per platform

## 3. 方案设计

### 3.0 概念模型

```
Main session (S1)
  └─ Agent M1 (持有 tools, instructions, ctx)
       │
       │ tool call: delegate_subagent(prompt="...", scope=["read_file","grep"], model="claude-haiku-4-5")
       │
       ├─ Subagent S1.1 (独立 session id "S1/sub/abc")
       │    └─ Agent M1.1 (scoped tools, fresh context)
       │        │ sub-tool call(s) ...
       │        └─ result: {text, tool_calls: [...], duration_ms}
       │
       └─ result back to M1
```

**关键不变量**:
- sub-agent 的 session id 命名空间是 `<mainSessionID>/sub/<random>`,数据库唯一约束。
- sub-agent 的 tool registry 是 main 的子集(scope 参数);不传 scope 默认走 main 全部 tools(危险,建议默认只给只读工具 + 显式允许写工具)。
- sub-agent 的 model 可以与 main 不同(主 agent 可以委派轻量任务给 haiku 节省成本)。
- sub-agent 的 instructions 是默认的 + main 在 prompt 里给的 task description,sub-agent 看不到 main 的 IDENTITY/SOUL/USER(独立 persona)。

### 3.1 数据结构

**`internal/subagent/` 新包**:

```go
package subagent

type Status int
const (
    StatusPending Status = iota
    StatusRunning
    StatusDone        // exit 0
    StatusFailed
    StatusAborted
    StatusTimeout
)

func (s Status) String() string { ... }

// Info is the read-only snapshot a manager returns to tool code.
type Info struct {
    ID          string   // "<mainSessionID>/sub/<rand>"
    ParentID    string
    Status      Status
    Prompt      string
    Scope       []string // tool names allowed
    Model       string
    StartedAt   time.Time
    EndedAt     time.Time
    Result      string   // final assistant text
    ToolCalls   int      // count
    ErrorMsg    string
}

// Spec is the input to Spawn.
type Spec struct {
    Prompt  string
    Scope   []string  // tool names; empty = all read-only tools (safe default)
    Model   string    // empty = inherit from parent
    Timeout time.Duration // 0 = no timeout
}

// Manager is per-main-session, goroutine-safe.
type Manager struct {
    parent  *agent.Agent
    store   RunStore         // persistence
    mu      sync.Mutex
    runs    map[string]*Run
}

func (m *Manager) Spawn(ctx context.Context, spec Spec) (Info, error) // returns immediately with Status=Running
func (m *Manager) List() []Info
func (m *Manager) Get(id string) (Info, bool)
func (m *Manager) Abort(id string) error
func (m *Manager) Wait(id string, timeout time.Duration) (Info, bool) // blocks until done or timeout
func (m *Manager) Close() // aborts all running sub-agents, idempotent
```

**`RunStore` 抽象**(可注入,默认走 SQLite):

```go
type RunStore interface {
    Insert(ctx context.Context, run Run) error
    Update(ctx context.Context, run Run) error
    ListByParent(ctx context.Context, parentID string) ([]Run, error)
    Get(ctx context.Context, id string) (Run, error)
}

type Run struct {
    ID         string
    ParentID   string
    Status     Status
    Prompt     string
    ScopeJSON  string  // serialized
    Model      string
    StartedAt  time.Time
    EndedAt    time.Time
    Result     string
    ToolCalls  int
    ErrorMsg   string
}
```

### 3.2 工具面（主 agent 视角）

| 工具 | 参数 | 行为 |
|---|---|---|
| `delegate_subagent` | `prompt` (string, 必填) / `scope` (string[], 可选) / `model` (string, 可选) / `timeout_ms` (integer, 默认 300000, max 600000) | `Spawn` 立即返回 sub-agent id(running 状态);`Wait` 阻塞到完成 / 超时 / abort;返回最终结果。**默认是同步** —— 主 agent 一次只跑一个 delegate,需要并行就多次调 delegate_subagent 在同一 turn(LLM 调度)。 |
| `list_subagents` | 无 | `List()` 返回当前 main session 的所有 sub-agent 快照(含已结束);`isError=false` 即使空。 |
| `abort_subagent` | `id` (string, 必填) | `Abort(id)`,SIGTERM-style cancel;返回确认。 |

`delegate_subagent` **返回格式**:
```
[subagent abc-1234 status: done | 1234ms | 5 tool calls]
<final assistant text here>
```

超时分支: `[subagent abc-1234 status: timeout | 300000ms | output so far]`.
abort 分支: `[subagent abc-1234 status: aborted]`.

### 3.3 Scope 设计

`scope` 是工具名白名单;不传时**默认走「safe read-only」**:
- ✅ always: `read_file`, `grep`, `glob`, `list_dir`, `web_fetch`, `web_search`(后者待实现)
- ❌ default 排除: 所有 `write_*` / `edit_*` / `delete_*` / `multi_edit` / `move_file` / `shell` / `notebook_edit` / `code_index`(写工具)/ `delete_symbol`
- ❌ default 排除: `delegate_subagent` / `list_subagents` / `abort_subagent`(避免 sub-agent 嵌套 spawn)

显式传 `scope=["read_file", "shell"]` 则按 whitelist;scope 之外的工具 sub-agent 调了会返回 `"tool not allowed in subagent scope"` 错误。

### 3.4 装配

`internal/agents/agent.go`:
- `Agent` struct 新增字段 `subagents *subagent.Manager`。
- `NewAgentConfig` 加可选 `Subagents *subagent.Manager`;nil 时 `New` lazy init fresh。
- `Jobs()` 同理改造:`a.jobs` + `a.subagents` 都 lazy init,`Subagents()` 永远非 nil。

`internal/agents/executor/executor.go`:
- `Deps` 加 `Subagents() *subagent.Manager`(同 `Jobs()` 印入 ctx 那次改)。

`internal/agentloop/session.go`:
- `Close()` 在 `Jobs().Close()` 后追加 `Subagents().Close()`。

`internal/agentloop/factory.go`:
- `AgentFactory` 加可选 `Subagents` 字段,`Build` 透传。

### 3.5 持久化

- `subagent.RunStore` 走 SQLite,新表 `subagent_runs`(id PK / parent_id / status / prompt / scope_json / model / started_at / ended_at / result / tool_calls / error_msg)。
- 落地时机:`Spawn` 插 pending → 启动后改 running → 终止时改终态。
- 失败不回滚(失败也是状态)。

### 3.6 并发模型

- `Spawn` 立即返回(`go m.runSpec(spec)` 起后台 goroutine)。
- 主 agent 调 `delegate_subagent` 时,**工具实现侧**调 `Wait(id, spec.Timeout)`,阻塞到完成 / 超时 / abort。
- LLM 想并行:同一 turn 内调多次 `delegate_subagent` —— 工具实现侧需要起一个 goroutine 等每个,然后等全部;**但当前 darvin executor 是 tool-by-tool 串行**(看 `executor.go:347 runToolsParallel`—— 哦,这个其实是并行的,wg.Wait 等全部)。所以并行没问题。
- **风险**:LLM 不会自动并行调多次 delegate_subagent;要 prompt engineering 引导(在工具 description 里说"需要并行时同 turn 调多次")。

### 3.7 失败 / 错误

| 场景 | 行为 |
|---|---|
| Scope 校验失败 | tool 立即返回 error,主 agent 看到 |
| Spec.Prompt 空 | 工具返回 error,不上 spawn |
| Manager.Spawn 内部错 | Spawn 返回 error,工具不返回 id |
| sub-agent run panic | RunStore 记 status=Failed + errorMsg,manager 移除运行 entry |
| SubagentManager.Close | 遍历所有 running run,调 `cmd`-style cancel,RunStore 标 aborted |
| 父 session evict | `AgentLoopSession.Close` 链式调 `Subagents().Close` |

### 3.8 UI

LobsterAI 没专门做 sub-agent UI(消息就当主 session 的一部分)。darvin 这里建议：

- 主 session message 流里把 sub-agent 消息**折叠**显示(`<details>` 风格),子消息可展开。
- session 切换/历史视图里,sub-agent run 单独有个 tab/折叠,标注"由 session X 派生"。

UI 工作量: ~300-500 行 Vue(主面板 + 折叠),跟随 `i18n.ts` 字典加 key。

---

## 4. 涉及文件(预计)

| 文件 | 变更 |
|---|---|
| `internal/subagent/subagent.go` | 新增,Manager / Info / RunStore / Scope 校验 |
| `internal/subagent/subagent_test.go` | 新增,manager 单测(scope 校验 / 并发 Spawn / Abort / Close) |
| `internal/subagent/run_store.go` | 新增,SQLite 实现 |
| `internal/subagent/run_store_test.go` | 新增,CRUD 单测 |
| `internal/tools/subagent.go` | 新增,`delegate_subagent` / `list_subagents` / `abort_subagent` 三个工具 |
| `internal/tools/subagent_test.go` | 新增 |
| `internal/agents/agent.go` | `subagents` 字段 + `Subagents()` + `NewAgentConfig.Subagents` |
| `internal/agents/executor/executor.go` | `Deps.Subagents()` + 印 ctx |
| `internal/agentloop/session.go` | `Close` 调 `Subagents().Close` |
| `internal/agentloop/factory.go` | `AgentFactory.Subagents` 透传 |
| `src/renderer/composables/useSubagents.ts` | 新增,sub-agent run 列表/状态 composable |
| `src/renderer/components/chat/SubagentFold.vue` | 新增,折叠展示 |
| `src/renderer/services/i18n.ts` | 加 `chat.subagent.*` / `settings.subagent.*` key |

## 5. 验证计划

1. `gofmt -l .` / `goimports -l .` 为空。
2. `go vet ./...` 零警告;`staticcheck -checks 'ST10*' ./...` 零告警。
3. `golangci-lint run ./...` 零告警(相对 baseline 不新增)。
4. `go test ./...` 全绿。`internal/subagent` 单测:
   - Spawn 立即返回(running 状态)、并发 Spawn N 个(无 race)。
   - Scope 默认白名单(拒绝 write / shell / 嵌套 spawn)。
   - Abort 终止 running。
   - Close 终止所有 running,幂等。
   - RunStore 持久化(SQLite CRUD)。
5. `internal/tools/subagent_test.go` 覆盖:三个工具的正常 / 异常路径。
6. `make lint-agents-boundaries` 通过(确认 `agents/` 引 `subagent` 不触发边界告警;`internal/subagent` 不在禁列)。
7. 冒烟:Electron 端主 agent 调 3 次 `delegate_subagent` 并行(各读 1 个文件),3 个 sub-agent 并发完成,主 agent 拿到 3 段结果。

## 6. 风险 / 开放问题

1. **LLM 是否会自动并行调多次 `delegate_subagent`** —— 需要工具 description 明确写「可以同 turn 调多次实现并行」,以及未来在 system prompt 里加引导(可选)。
2. **sub-agent 的 context 跟 main 怎么隔离** —— 完全独立,sub-agent 看不到 main 的消息历史;这意味着 sub-agent 不知道 main agent 之前跟用户聊过什么。**如果是问题,后续可加 `Spec.Context` 字段(主 agent 显式给子任务的「前置信息」)。**
3. **sub-agent 的 token 消耗记账** —— sub-agent run 的 token 算谁的?目前建议算 sub-agent 自己的 Run(写到 RunStore),主 session 那边不动;UI 显示时聚合。**等真上线后再讨论怎么展示给用户。**
4. **sub-agent 嵌套深度限制** —— sub-agent 不能再 spawn(本 spec 默认 scope 排除),简单粗暴。要不要允许一层嵌套?目前不做。
5. **UI 折叠展示的可读性** —— 当 sub-agent 跑 10 次,主 session 流会被 10 段折叠填满。要不要做"按需展开"?要,但放到 UI 阶段再说。

## 7. 后续(不在本 spec)

- 跨主 session 共享 sub-agent(目前 per-session 隔离)。
- sub-agent 自动拆分(main agent 不显式调,系统自动拆分复杂任务)。
- sub-agent 的 sub-agent(嵌套 spawn,目前禁止)。
- sub-agent marketplace / 模板(用户保存「常用 sub-agent 模板」直接复用)。

---

**当前状态**: 📝 paper-level 设计稿,等启动实现前评审 + 独立成 `specs/features/subagent/...` 完整 design 文档。
