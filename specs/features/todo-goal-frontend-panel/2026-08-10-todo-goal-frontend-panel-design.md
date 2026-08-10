# Todo/Goal 前端面板（TodoPanel）设计文档

## 1. 概述

### 1.1 问题 / 背景

Go 端 `todo_write` / `complete_step` 已落地（commit `49efed0`），但 renderer 没有任何可视入口：

- `ComposerToolbar.onPick` 对 `goal` / `todo` 两个按钮只 `console.warn`（`ComposerToolbar.vue:61-68`），点了没反应。
- 工具调用只在 chat 里以 `ToolCallGroup` 的原始 JSON args 展示，没有结构化的清单视图；`plus.todo.*` / `plus.goal.*` 文案已就位（"待办清单 / 管理你的任务列表"）但无落地。

D spec（`specs/features/builtin-tools-d-todo-goal/`）§8 已把「前端 TodoPanel」列为后续项：订阅 `tool_start` 的 `todos` 参数，渲染两阶段 checklist + `complete_step` 的 sign-off 徽标，随会话切换 / 历史回放可见。

### 1.2 目标

1. 新增 TodoPanel（artifact 面板特殊 tab），渲染当前会话**最近一次** `todo_write` 的两级清单 + `complete_step` 签收徽标。
2. PlusMenu 的 `goal` / `todo` 按钮打开该面板。
3. 纯 renderer 实现，**零 Go / IPC 改动**——数据从消息桶提取（live 事件 + 历史回放走同一条路）。

### 1.3 非目标

- 不做清单编辑 / 手动增删改（清单由 agent 通过工具驱动，面板只读）。
- 不做独立的 Goal 面板（goal 与 todo 共用 TodoPanel）。
- 不改 Go 端：不新增 todo 持久化 / IPC / 事件类型。
- 不在 `tool_start` 时自动弹开侧栏（与 artifact / subagent 一致，用户主动打开）。
- 不做跨 session 汇总。
- 不接 PlusMenu 的 `settings`（保留现有 `console.warn` 兜底，行为不变，见 FR-3）。
- 不修 `ArtifactPanel.specialTabs` 顶层缓存 `t()` 的切语言不刷新问题（继承性问题，`ArtifactPanel.vue:127-131`，不在本 spec 范围，见 FR-3）。

## 2. 用户场景

### 场景 1: 让 agent 规划任务，清单实时出现在面板
**Given** 用户在聊天里发「帮我规划今天开发任务」
**When** agent 调用 `todo_write`（含 milestones + sub-steps 的两级清单）
**Then** chat 里出现 todo_write 的工具调用卡片，同时 TodoPanel 渲染结构化清单；agent 更新 `in_progress` / `completed` 后清单实时变化

### 场景 2: 从 PlusMenu 打开面板
**Given** 当前会话已有清单
**When** 用户点 composer 的 + 菜单 →「待办清单」或「目标设定」
**Then** 右侧 artifact 面板打开并激活 Todo tab，显示当前清单

### 场景 3: 签收可见
**When** agent 完成某步骤后调用 `complete_step`（带 evidence）
**Then** 对应清单项显示「已签收 · N 项证据」徽标

### 场景 4: 切会话 / 重启回放
**When** 用户切到另一个有历史清单的会话，或重启后打开面板
**Then** 面板从该会话消息历史还原最近一次 `todo_write` 的清单与签收

## 3. 功能需求

### FR-1: useTodos composable（数据提取）

singleton（模块级 ref），从 `useMessages().currentMessages`（active session 消息桶）派生，**不新增任何 IPC**：

- 扫描 `kind === 'tool_use'` 的消息：
  - `tool === 'todo_write'`：取**最后一次**调用的 `input.todos` 作为当前清单（stateless，args 即状态，覆盖前一份）。`hasList` = 存在至少一条 `todo_write` 调用（最后一条即使 `todos: []` 也算，见 FR-2 空态二态）。
  - `tool === 'complete_step'`：收集所有调用的 `input.{step_id, content, evidence}`；**按 `content` 去重、后者覆盖**。`step_id` 是 todo 列表索引，`todo_write` 整体替换会使其漂移，不能作稳定键；`content` 才是 D spec §3.2 定义的「便于前端对账」回显。实现用 `Map<content, TodoSignOff>` 累加，重复命中更新 `evidenceCount` / `createdAt`。
- 暴露：`items`（`TodoItem[]`）、`signOffs`（`TodoSignOff[]`，content 去重后）、`hasList`、`updatedAt`。
- `updatedAt` = 最后一次 `todo_write` tool_use 消息的 `createdAt`（live 为事件到达时间，历史回放为持久化的 assistant 消息时间；与「最后一次为准」语义一致）。
- session 切换自动随 `currentMessages` 切换（消息桶按 active session 分桶）。

### FR-2: TodoPanelContent 渲染

- **空态二态区分**：
  - `!hasList`（本会话从未有 `todo_write`）→ 引导态：「当前会话暂无待办」+「让助手先规划一下任务」（`artifact.todo.empty` / `.empty.hint`）。
  - `hasList && items.length === 0`（最后一次 `todo_write` 传 `todos: []`）→ 空清单态：「当前清单为空」（`artifact.todo.empty.list`），**不显示**引导文案。
- **两级渲染**：若清单里存在 `level=1` 项 → 树形渲染（`level=0` 里程碑加粗成组，`level=1` 子步骤缩进在其下）；若全为 `level=0` 或未填 level → 平铺渲染为普通行。
- 每项：状态图标（pending 圆圈 / in_progress 脉冲 / completed 勾）+ `content` + `in_progress` 项的 `activeForm` 副标题。
- **in_progress 脉冲**：`theme.css` 的 `@theme` 块新增动画 token `--animate-todo-pulse: todo-pulse 1.6s ease-in-out infinite;` + `@keyframes todo-pulse`（opacity 明暗循环），组件用 `animate-todo-pulse` utility 消费；不走内联 style / 组件级 `<style>`（符合「动效走 @theme」规则）。
- **签收徽标**：按 `content` 回显匹配（D spec §3.2 明确 content 是「便于前端对账」的回显），命中显示「已签收 · N 项证据」；同一 `content` 重复签收只取最后一次（FR-1 去重规则）。清单内出现重复 `content` 时徽标同时命中多行（v1 接受该歧义，不按 step_id 消歧）。

### FR-3: ArtifactPanel 特殊 tab + PlusMenu 接线

- `ArtifactSpecialTab` 增 `Todo: 'todo'`（`useArtifacts.ts`），`isSpecialTabId` 收录。
- `ArtifactPanel.vue` `specialTabs` 增一项（icon `list` / label `artifact.special.todo`），内容区 `v-else-if` 渲染 `<TodoPanelContent>`。
- `ComposerToolbar.onPick`（`ComposerToolbar.vue:61`，union `'upload' | 'goal' | 'todo' | 'settings'`）分派：
  - `goal` / `todo` → `artifacts.activateTab(sid, ArtifactSpecialTab.Todo)`（`activateTab` 内部已 `setPanelOpen(sid, true)` 弹侧栏），并从 `console.warn` 兜底中移除这两个 id。
  - `upload` → 保持现有 `pickAttachments()`。
  - `settings` → **保留**现有 `console.warn` 兜底（本 spec 不接设置面板；整段移除 warn 会让 `settings` 静默 no-op，属于行为变化，不在本 spec 范围）。
- Todo tab 沿用 `specialTabs` 现有模式（含其 `<script setup>` 顶层缓存 `t()` 的已知局限，本 spec 不修，见非目标）。

### FR-4: i18n

新增 keys（zh / en 同步，`assertSameKeys` 守卫）：

- `artifact.special.todo`
- `artifact.todo.empty` / `artifact.todo.empty.hint`（引导态：无 `todo_write`）
- `artifact.todo.empty.list`（空清单态：`todos: []`）
- `artifact.todo.status.pending` / `artifact.todo.status.in_progress` / `artifact.todo.status.completed`
- `artifact.todo.signedOff`（`{n} 项证据`）

## 4. 实现方案

### 4.1 数据流（关键设计：消息桶即真相源）

```
Go tool_start(todo_write / complete_step) ─┐
                                            ├─► useMessages.appendEvent ─► messagesBySessionId[sid]
getMessages(sid) 历史回放 ─────────────────┘      （tool_use 消息，含 input）
                                                        │
                                        useTodos 从 currentMessages 派生
                                                        ▼
                                            TodoPanelContent（active session）
```

- live 与 replay 走同一条消息桶，天然覆盖「会话切换 / 历史回放」；不新增 Go 持久化 / IPC（todo stateless，清单活在对话历史，与 D spec §3.3 一致）。
- `appendEvent` 每次事件替换 `messagesBySessionId.value` map 对象 → computed 自动重算（O(n) 扫描，消息量小，无需优化）。

### 4.2 类型

```ts
// useTodos.ts
interface TodoItem {
  content: string;
  status: 'pending' | 'in_progress' | 'completed';
  activeForm?: string;
  level: 0 | 1; // 缺省按 0
}
interface TodoSignOff {
  stepId: number;
  content: string;
  evidenceCount: number; // input.evidence 数组长度
  createdAt: number;
}
interface TodoState {
  items: TodoItem[];
  /** content 去重后的签收记录（Map 累加，后者覆盖）。 */
  signOffs: TodoSignOff[];
  /** 是否存在至少一次 todo_write（todos: [] 也算 true）。 */
  hasList: boolean;
  /** 最后一次 todo_write 的 createdAt。 */
  updatedAt: number;
}
```

- `input` 类型为 `unknown`，提取时做窄化断言：`input as { todos?: TodoItem[] }`；`complete_step` 同理。

### 4.3 组件

- `TodoPanelContent.vue`（`components/side-panel/`）：读 useTodos，渲染清单；`data-testid="todo-panel-content"` / `todo-empty` 等。
- 复用 `t()` / `Icon` 组件；样式全走 utility class + `@theme` token，不写 `<style>` 块。

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| 无 `todo_write` | 引导态（empty + empty.hint），不显示空清单文案 |
| 最后一次 `todo_write` 传 `todos: []` | `hasList=true`、items 空 → 空清单态（empty.list），不显示引导 |
| 会话被压缩（ctxengine fold） | `todo_write` 的 tool_use 可能被折叠丢失 → 面板回退引导态（D spec §3.3 已接受该限制） |
| 多次 `todo_write`（in_progress 更新） | 最后一次为准（`items` / `updatedAt` 同步取最后一次） |
| 多个 `complete_step` 命中同一 `content` | content 去重、后者覆盖（evidenceCount 取最后一次） |
| `complete_step` 的 content 不在当前清单 | 不匹配 → 不显示徽标（记录保留但无行可配，v1 忽略） |
| 切会话 | `currentMessages` 自动切到目标 session 的消息桶 |
| `todo_write` 调用校验失败（IsError） | v1 仍按 input 渲染（args 已在消息里）；错误提示留作后续 |
| 面板未打开时事件到达 | 静默记录在消息桶，下次打开即见（不自动弹侧栏） |
| 跨平台差异 | 无（纯 renderer，无 OS 依赖） |

## 6. 涉及文件

| 文件 | 变更 |
|------|------|
| `src/renderer/composables/useTodos.ts` | **新增**，从消息桶派生 TodoState |
| `src/renderer/composables/useTodos.test.ts` | **新增**，派生逻辑单测 |
| `src/renderer/components/side-panel/TodoPanelContent.vue` | **新增**，清单渲染 |
| `src/renderer/composables/useArtifacts.ts` | `ArtifactSpecialTab.Todo` + `isSpecialTabId` 收录 |
| `src/renderer/components/side-panel/ArtifactPanel.vue` | specialTabs 增 todo 项 + 内容区渲染 |
| `src/renderer/components/chat/ComposerToolbar.vue` | `onPick` goal / todo → activateTab(Todo) |
| `src/renderer/services/i18n.ts` | zh / en 新增 keys |
| `src/renderer/styles/theme.css` | `@theme` 增 `--animate-todo-pulse` 动画 token |
| `specs/features/todo-goal-frontend-panel/` | 本 spec |

## 7. 验收标准

- [ ] 场景 1：agent 调用 `todo_write` 后，TodoPanel 显示结构化两级清单，状态更新实时
- [ ] 场景 2：PlusMenu「待办清单」与「目标设定」都打开 Todo tab 并弹侧栏
- [ ] 场景 3：`complete_step` 命中项显示「已签收 · N 项证据」
- [ ] 场景 4：切会话 / 重启后从历史还原清单
- [ ] `npm run lint` 通过
- [ ] `npm run test` 通过（`useTodos.test.ts` 覆盖：最后一次 todo_write 为准、`todos: []` 空清单态、complete_step 签收收集 + content 去重后者覆盖、content 不匹配不显示徽标、`updatedAt` 取最后一次、切 session）
- [ ] `npm start` 手测：发规划 prompt → 面板出现清单；点 plus → todo 打开面板；切会话清单跟随；plus → settings 仍无副作用（保留 warn 兜底，行为不变）
- [ ] i18n `assertSameKeys` 不报错（zh / en 同步）
