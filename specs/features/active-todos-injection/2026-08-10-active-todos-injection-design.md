# 跟进 spec：host 端 active-todos 注入（让 agent 每轮看到当前清单 + 压缩免疫）

## 1. 概述

### 1.1 问题 / 背景

`todo_write` / `complete_step` 落地（`49efed0`）后，todo 是 **stateless**：清单只活在对话历史的 tool_use args 里，工具回执只是计数（`internal/tools/todo.go:103` "todo list updated: N items"）。这带来两个问题：

1. **agent 可见性是隐式的**：模型下一轮能看到清单，唯一靠它自己那条 `todo_write` tool_use 还在上下文里。没有 host 主动把「当前清单」摆到模型面前。
2. **压缩即丢**：`ctxengine/compact.go` 的 `partitionFold` 只把历史 digest 放 kept 区 verbatim（`isPinnableUserTurn` 返回 false），其余消息——含 `todo_write` 的 tool_use/tool_result 对——全部折叠进 LLM 摘要。摘要 prompt（`ctxengine/prompts.go`）只有 `## Pending & next step` 段能带出一两句，完整清单无保证。除非最后一次 `todo_write` 落在 RecentKeep 尾部，否则压缩后模型对「现在做到哪一步」的把握依赖摘要的运气。

对照参考项目，两个成熟做法：

- **Reasonix**（`internal/control/input.go:164-167`）：活动 **goal** 每轮以 `<active-goal>` XML 块 prepend 进用户消息——每轮从 host goal FSM 重新注入，压缩免疫。
- **LobsterAI**（`src/auto-reply/reply/inbound-meta.ts:811-814`）：goal **持久化在 session store**，每轮从 store 注入一行 `Active goal: …` 进 user-role 上下文前缀；压缩只折叠历史消息、不碰 store，QA 场景 `goal-context-survives-compaction.yaml` 专门断言压缩后下一轮 provider 请求仍含该行。

darvin 当前两者都没有：todo 不落 host 状态、不注入、压缩会丢。

### 1.2 目标

1. 新增 host 端 per-session「当前清单」store（`internal/todos`），从最近一次 `todo_write` 维护。
2. 每轮组装 LLM 请求时，把当前清单以 `<active-todos>` 块 append 进消息末尾（只进模型请求，**不污染持久化的 user 消息**）。
3. 压缩免疫：store 独立于消息历史，ctxengine 折叠不碰它。
4. 重启可恢复：session hydrate 时从**全量 DB 历史**（digest 边界之前的部分也在）重播种 store。

### 1.3 非目标

- 不做 `update_goal` / goal-turn FSM（D spec §7 已挂起；本 spec 只做 todo 可见性）。
- 不改前端 TodoPanel（它继续从消息桶派生，见 `todo-goal-frontend-panel` spec）。
- 不注入 `complete_step` 签收（模型靠自己的调用维护签收；注入只给清单状态）。
- 不落 SQLite 新表：用 in-memory store + hydrate 重播种（DB 消息表已是持久化真相源，`PersistCompaction` 不截断它）。
- 不覆盖 skill turn（`RunSkillSession` 独立 mini-loop 路径，v1 不注入，见 §5）。

## 2. 用户场景

### 场景 1: 长任务中途压缩后，agent 仍记得清单
**Given** 一个跑了很久的会话，agent 写过 `todo_write` 清单，上下文接近上限
**When** ctxengine 自动折叠历史（最后一次 `todo_write` 落在 fold 区）
**Then** 下一轮模型请求里仍有 `<active-todos>` 块（含最新 status），agent 继续推进而不是重新规划或丢失进度

### 场景 2: 多工具轮次中，清单始终在模型视野
**When** agent 在一个 run 内连续多轮调工具（MaxTurns 循环）
**Then** 每轮请求末尾都带当前 `<active-todos>`，模型看到自己把哪项标成 in_progress、哪些已 completed

### 场景 3: 重启后恢复注入
**When** 进程重启，session 从 MessageStore 重新 hydrate（含曾压缩的会话）
**Then** 从全量历史重播种 store，重启后第一轮仍注入当前清单

## 3. 功能需求

### FR-1: `internal/todos` store（纯 in-memory，无协议依赖）

- 类型 `Item{ Content string; Status string; ActiveForm string; Level int }`（与 `todo_write` args 同构）。
- 进程级单例 `Store`（map[sessionID][]Item + RWMutex），方法：
  - `Set(sessionID, items)`：整体替换当前清单（args 即状态，覆盖前一份）。
  - `Clear(sessionID)`：删除该 session 条目（等价于 `todos: []`）。
  - `Block(sessionID) string`：渲染 `<active-todos>` 块；无清单 / 空清单返回 `""`。
  - `Get(sessionID) ([]Item, bool)`：测试与 hydrate 用。
- **不 import** `agents/protocol`：Item 用自己的 struct，保持零外部依赖（调用方负责解析协议消息 → Item）。

### FR-2: executor 分发 `todo_write` 时更新 store

在 `executeOneTool`（`internal/agents/executor/executor.go:416`，所有工具的唯一执行入口，`runToolsParallel` → `executeOneTool`）执行完工具后：

- 若 `c.Name == "todo_write"`：
  - 解析 `c.Arguments`（`json.RawMessage` → `{ todos: [] }`）为 `[]todos.Item`；
  - 结果 `!IsError`：`todos.Set(sessionID, items)`；清单为空数组 → `todos.Clear(sessionID)`；
  - 结果 `IsError`（校验失败）：**不更新** store（args 已在消息里，但 host 状态保持上一次合法值）。
- 解析用 executor 内联的小 struct（不 import `internal/tools`，规避 `agents/` 边界规则）。

### FR-3: executor 请求构建时注入 `<active-todos>` 块

在 `executor.go` 组装 `protocol.CompletionRequest`（~197 行，`d.Provider().Stream(ctx, req)` 之前）:

- `block := todos.Block(d.Session().ID)`；非空时把 `{Role: user, Content: block}` **append 到 `messages` 末尾**。
- 只改本地 `messages` 切片（assemble 投影或 `d.Session().Messages()` 的拷贝），**不写回 session / DB** → 持久化 user 消息保持原始内容（`dispatcher.go:107-117` 已用原始 `msg.Content` 落库，不受影响）。
- 位置选择「末尾 append」的理由：① 多工具轮次中 messages 常以 tool_result 结尾，末尾补一条 user 满足 alternation（tool→user）；普通轮次结尾是 user，user→user 由 Anthropic 合并。② 紧贴模型生成点，显著性最高。③ 实现最简。
- 每个 run 的每个 turn 都执行（循环内），模型每轮都看到。

### FR-4: hydrate 重播种

在 `sessionruntime/hydrate.go` 的 `hydrateSession`：`history` 从 `MessageStore.List` 读全量后、`splitAtBoundary` **之前**，扫描其中最后一条含 `todo_write` ToolCall 的 assistant 消息，解析 `Arguments` → `todos.Set(sess.ID, items)`；空清单 → `Clear`。

- 必须在 slice 之前：`PersistCompaction`（`agent_context.go:84`）只写 digest 不截断 DB，`history` 是全量；边界 slice 只影响送入模型的内存切片。这样「压缩过的会话重启」也能从边界前的 DB 行恢复最后一次 `todo_write`。

## 4. 实现方案

### 4.1 数据流

```
todo_write 分发 (executeOneTool) ──► todos.Set(sessionID, items)   ──┐   host store（in-memory，独立于历史）
                                                                    ├─► 每轮 executor 请求构建 ──► [messages…, <active-todos>] ──► LLM
hydrate (MessageStore 全量) ────────► todos.Set(sessionID, items)   ──┘         （只进请求，不落库）
                                                                    │
ctxengine compact（折叠历史） ───────────► 不碰 store（压缩免疫）       ┘
```

### 4.2 store 实现（`internal/todos/store.go`，~90 行）

```go
// Package todos holds the host-side current-task-list per session.
package todos

type Item struct {
    Content    string
    Status     string // pending | in_progress | completed
    ActiveForm string
    Level      int    // 0 = phase, 1 = sub-step
}

type Store struct {
    mu       sync.RWMutex
    bySession map[string][]Item
}

var global = &Store{bySession: make(map[string][]Item)}

func Set(sessionID string, items []Item)      // 非空整体替换；空 → Clear
func Clear(sessionID string)
func Get(sessionID string) ([]Item, bool)
func Block(sessionID string) string           // 渲染 <active-todos>；空返回 ""
```

- 单例 + RWMutex，sessionID 唯一，天然多 session 安全。
- `Block` 渲染格式见 4.3；`Set` 对空数组转发 `Clear`，语义对齐「`todos: []` 清空清单」。

### 4.3 注入块格式（`Block` 输出）

```xml
<active-todos>
Host-tracked task list — the authoritative current state of your todo_write plan.
Re-send the COMPLETE list via todo_write whenever progress changes; keep at most one item in_progress.
- [in_progress] Design the parser
  - [completed] Write the lexer
  - [pending]   Write the parser
- [pending] Add tests
</active-todos>
```

- 每行：`- [status] content`，`level=1` 缩进两格；status 用英文 token（模型向，不走 i18n）。
- 开头指令约束模型行为（完整重发、串行 in_progress），对齐 `todo_write` 的 schema 语义。
- 清单为空 → `Block` 返回 `""`，不注入。

### 4.4 关键文件改动点

| 位置 | 改动 |
|------|------|
| `internal/todos/store.go` | **新增** store + `Block` 渲染 |
| `internal/todos/store_test.go` | **新增** 单测 |
| `internal/agents/executor/executor.go` | ① `executeOneTool` 分发 hook（~416）；② 请求构建注入（~197） |
| `internal/agents/executor/executor_test.go` | **新增** dispatch 更新 / 注入 / 不污染持久化 / IsError 不更新 |
| `internal/sessionruntime/hydrate.go` | `splitAtBoundary` 前重播种 |
| `internal/sessionruntime/hydrate_test.go` | **新增** 含压缩 digest 的重启恢复 |

### 4.5 压缩免疫原理

- store 更新发生在**实时分发**（执行 `todo_write` 的瞬间），先于任何压缩。
- ctxengine 折叠只重组 `messages` 切片（`Compact` 返回 `RetainedMessages` → `Session().ReplaceAll`），**不接触 `internal/todos` 单例**。
- 因此压缩后下一轮 `Block(sessionID)` 仍返回当前清单 → 注入恢复完整列表，不依赖摘要段。

### 4.6 参照实现对照

| | darvin（本 spec） | Reasonix | LobsterAI |
|---|---|---|---|
| 状态存放 | in-memory per-session store + hydrate 重播种 | in-memory executor todo state + host goal FSM | session store 持久化 |
| 每轮可见 | `<active-todos>` user 块（请求末尾） | `<active-goal>` 块（用户消息前缀） | `Active goal:` 行（user-role 上下文前缀） |
| 压缩免疫 | store 独立于历史 | goal 每轮重注入 | store 不碰 |
| 重启恢复 | hydrate 从全量 DB 重播种 | 会话重建时重派生 | 从持久化 store 读 |

darvin 取「in-memory + hydrate 重播种」是因为 DB 消息表本身就是持久化真相源（压缩不截断），无需像 LobsterAI 另立 SQLite 表；注入点选 executor（贴近 LLM）则避免了 Reasonix 那套「compose 注入 + 前端 strip 前缀」的显示层回补。

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| `todo_write` 传 `todos: []` | `Clear(sessionID)` → `Block` 返回 ""，不注入（与前端空清单态一致） |
| `todo_write` 校验失败（IsError） | 不更新 store（保持上一次合法清单） |
| 多次 `todo_write` | 最后一次为准（Set 整体替换） |
| ctxengine 自动 / 手动压缩 | 不碰 store；下一轮仍注入 |
| 进程重启（含压缩过的会话） | hydrate 从全量 DB（边界前）重播种 |
| skill turn（`RunSkillSession`） | v1 不注入（独立 mini-loop 路径；如需再单独接线） |
| messages 末尾是 tool_result | append user 块满足 alternation（tool→user 合法） |
| messages 末尾是 user | user→user 由 provider 合并，块被并入前一条用户文本，语义自限 |
| 会话删除 | store 条目残留无害（只对活跃 session 读）；可选加 `Clear` 收尾 |
| 前端 TodoPanel | 不受影响（仍从消息桶派生；压缩后面板可能空而模型仍见注入，属前端 spec §5 已知差异） |
| 跨平台 | 无 OS 依赖（纯 Go in-memory） |

## 6. 涉及文件

| 文件 | 变更 |
|------|------|
| `src/darvin-agent/internal/todos/store.go` | **新增**，per-session 清单 store + `Block` 渲染 |
| `src/darvin-agent/internal/todos/store_test.go` | **新增**，Set/Get/Clear/Block 单测 |
| `src/darvin-agent/internal/agents/executor/executor.go` | `executeOneTool` 分发 hook + 请求构建注入 |
| `src/darvin-agent/internal/agents/executor/executor_test.go` | **新增**，dispatch 更新 / 注入 / 持久化不污染 / IsError 不更新 |
| `src/darvin-agent/internal/sessionruntime/hydrate.go` | `splitAtBoundary` 前重播种 |
| `src/darvin-agent/internal/sessionruntime/hydrate_test.go` | **新增**，含压缩 digest 的重启恢复 |

## 7. 验收标准

- [ ] `go test ./...` 全绿；新增用例覆盖：
  - `store_test.go`：Set/Get/Clear/Block 渲染、空清单返回 ""、`todos: []` → Clear
  - `executor_test.go`：分发 `todo_write` 后 store 更新；IsError 不更新；请求末尾注入 `<active-todos>`；空 store 不注入；注入后 `Session().Messages()` 与持久化 user 消息**不含**该块
  - `hydrate_test.go`：DB 含压缩 digest + 边界前 `todo_write` → hydrate 后 store 恢复该清单
- [ ] `gofmt -l .` / `go vet ./...` 零告警；`staticcheck -checks 'ST10*'` 零告警
- [ ] 手测：长 prompt 触发压缩（或手动 `/compact`）后，agent 下一轮仍记得清单（观察它继续推进而非重新规划）
- [ ] 前端回归：TodoPanel 正常（本 spec 不改前端，需确认无回归）
