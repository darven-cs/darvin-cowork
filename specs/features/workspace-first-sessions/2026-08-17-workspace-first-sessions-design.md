# 工作区先行的会话创建流程设计文档

## 1. 概述

### 1.1 问题 / 背景

当前 darvin 的 workspace 与 session 是**一一对应**关系：`WorkspaceLocation { rootPath, workspaceId }`，且 `workspaceId === sessionId`（`src/main/libs/user-paths.ts:89-94`）。workspace 并非一等实体：

- 没有独立的 workspace 记录（无名称 / 无列表 / 无独立创建），默认目录直接由 `resolveWorkspaceRoot(sessionId)` 派生为 `userData/workspaces/<sid>`（或 `workspace-mapping.json` 里的 `sessionId → path` 映射）。
- main 进程全局只有一个「当前 workspace」`workspaceLoc`，随 active session 切换而重锚（`followActiveWorkspace`，`src/main/index.ts:185-203`）。
- Go 端 `handleCreateSession`（`handler_session.go:314`）只建 session 记录，workspace 完全由 sessionId 派生，用户无感知；首页发消息 → `createSession({newSession:true})` → workspace 隐式生成。
- 后果：无法让多个会话共享同一个项目目录；「先定工作目录、再开对话」的自然顺序被倒置为「先开对话、目录才被动生成」。

### 1.2 目标

1. workspace 成为一等、可持久化的实体（`workspaces` 表），有独立 id / 名称 / rootPath。
2. 调整创建顺序：**先创建 / 选择 workspace，再创建 session**；一个 workspace 承载多个会话（1 工作区 : N 会话）。
3. 新增「工作区列表页 / 首屏」：无 active workspace 时先选或建工作区，再进入聊天；首页聊天框交互参照 deepseek-harness 的 hero 空态 + workspace chip + 自增高输入框。
4. 兼容存量数据：旧 session 无 workspace 归属，迁移时按现有目录反建 workspace 并绑定，不丢 imported_files / 对话历史。

### 1.3 非目标

- 工作区的完整 CRUD 管理 UI 打磨（重命名 / 详细设置 / 配额展示仅在必需范围内，SVG 图标等美术细节不在此 spec 内）。
- 每工作区的独立模型 / skills / MCP 配置隔离（workspace root 变化触发 set_workspace 重锚即满足现状，不做 per-workspace 配置表）。
- 多窗口并发操作同一 workspace 的分布式一致性（单窗口 Electron 原型）。

### 1.4 设计参照：deepseek-harness 首页聊天框

`packages/client/ui-conversation/src/client/skeleton/` 的编排为本 spec 的 UI 参照基准：

- **Hero 空态**（`HeroShell` + `HeroGlow` + `WorkspaceChip`）：居中标题 + 光晕背景；聊天输入框驻留在底部区域。
- **WorkspaceChip**：`文件夹图标 + 名称 + chevron` 的常驻 chip。首条消息前恒定可切换；无 workspace 时显示占位态「Choose workspace」（点它或 Enter 触发 `onRequestWorkspace`）。
- **selectWorkspace → 落空会话**：`ConversationRoot` 里 pick 一个 workspace 后，把 New Session（blank）落进该 workspace（`workspaces.items.find(w => w.sessionIds.includes(sessionId))` 反查归属，1 workspace : N sessions）。
- **输入框**（`InputBar`）：textarea 自增高（mirror 层撑起完整高度、cap 约 14 行）、Enter 发送 / Shift+Enter 无条件换行 / IME 组合期 Enter 不发送、Send/Stop 主按钮随 `running` 切换。
- **chip 标题解析顺序**：pending 选中 > 冷启动无 session（占位）> 所在 workspace 标题 > cwd basename 桥接 > 占位。

## 2. 用户场景

### 场景 1: 首次启动（无任何 workspace）
**Given** 冷启动，sessions 表为空，无 active workspace
**When** 应用打开
**Then** 落入「工作区」首屏空态：展示「创建第一个工作区」引导 + 新建入口；填名称（可选选目录）→ 点创建 → 建好 workspace 并设为 active → 自动进入该工作区的聊天空态（hero + 聊天框）→ 输入消息发送 = 在该 workspace 下新建 session。

### 场景 2: 已有 workspace，进入聊天
**Given** 已存在 workspace A（含若干会话）
**When** 用户在工作区首屏选择 workspace A
**Then** workspace A 设为 active，进入该工作区的首页：显示其会话列表（侧栏）+ 当前会话内容；发消息在该 workspace 下新建会话或接续 active session。

### 场景 3: 在已有 workspace 内新建会话
**Given** 处于 workspace A 的聊天页，已有 session 1，正在流式回复中
**When** 用户点「新建任务 / 新对话」
**Then** 在 workspace A 下创建新的空 session 2（workspace 不变），进入其空态；session 列表按 workspace 过滤，只显示 A 的会话。

### 场景 4: 切换 workspace
**Given** 处于 workspace A 的聊天页（active session 为 A 的某会话）
**When** 用户点顶部/侧栏的 workspace chip，切到 workspace B
**Then** 切到 B：active session 变为 B 最近更新的会话（或空态），main 重锚 workspace 根（set_workspace），imported_files / 文件操作 / 项目 skills 全部跟随 B 的目录。

### 场景 5: 迁移存量数据
**Given** 升级前已有若干 session（无 workspace 归属，目录为 `workspaces/<sid>`）
**When** 首次启动新版本
**Then** 自动为每个旧 session 反建 workspace（名称取 session 标题，rootPath 保持原 `workspaces/<sid>`），绑定 `workspace_id`；侧栏会话与 imported_files 原样可见，不丢数据。

## 3. 功能需求

### FR-1 workspace 实体与持久化（Go）
- 新增 `store.Workspace` 模型，表名 `workspaces`：
  - `ID` nanoid（复用现有 id 生成器语义）、`Name` / `Title`（用户可见名，可为空则回退 basename）、`RootPath`（绝对路径，唯一索引，唯一强烈约束）、`CreatedAt` / `UpdatedAt`。
- `session` 表新增 `workspace_id` 列（`gorm:"index"`），建模 `Session.WorkspaceID string`。
- 迁移（`runtime/database.go` 的 `AutoMigrate` 清单）追加 `&store.Workspace{}`；新增列由 GORM 幂等补齐。
- 存量回填：启动迁移后执行一次性 backfill——对 `workspace_id IS NULL` 的 session，按 `resolveWorkspaceRoot(sessionID)` 的旧目录（含 user 自定义映射）建 workspace 并写回 `workspace_id`。幂等（升级前已回填则跳过）。

### FR-2 workspace 会话归属
- `store.SQLiteStore.ListAll` 支持按 `workspace_id` 过滤；新建 `ListByWorkspace(ctx, workspaceID)`。
- `handleCreateSession` 参数新增可选 `WorkspaceID`；为空时回退 active workspace；两者皆无则报错 `workspace_required`（Code 用现有 `CodeInvalidParams` 语义或新增 `CodeWorkspaceRequired`）。
- `SessionWire` / `DarvinSession` 新增 `workspaceId` 字段。

### FR-3 workspace IPC / JSON-RPC
Go 网关新增 handler（命名遵循 `handle<Domain>`）：

| 方法 | 入参 | 出参 | 语义 |
|------|------|------|------|
| `agent.list_workspaces` | — | `{ workspaces: WorkspaceWire[] }` | 全量列表（含 sessionCount、label） |
| `agent.create_workspace` | `{ name?, rootPath? }` | `{ workspace: WorkspaceWire }` | 无 rootPath 则建默认 `userData/workspaces/<wid>` 并 mkdir |
| `agent.get_active_workspace` | — | `{ workspaceId: string\|null }` | app_state 读取 |
| `agent.set_active_workspace` | `{ workspaceId }` | `{ workspaceId }` | 持久化 active + 触碰 updated_at |
| `agent.delete_workspace` | `{ workspaceId }` | `{ deleted, nextActiveWorkspaceId }` | 级联：改绑其下 session（或校验非空可删） |

`WorkspaceWire`：`{ id, name, label, rootPath, sessionCount, createdAt, updatedAt }`（`label` = basename(rootPath)，供 UI 展示；rootPath 仅在 renderer 需要文件操作的对象内使用，与现有 `getWorkspaceRoot` 一致）。

app_state 新增 key：`active_workspace_id`；与 `active_session_id` 需保持一致性（切 workspace 时把 active session 置为该 workspace 最近会话或空）。

### FR-4 main 进程改造
- `WorkspaceLocation.workspaceId` 语义从「= sessionId」改为「= workspace id」；`resolveWorkspaceRoot(workspaceID)` 改为读 `workspaces` 表的 RootPath（不再用 `workspace-mapping.json` 的 sessionId 键）。
- `followActiveWorkspace(sessionId)` → 改为 `followActiveWorkspace(workspaceId)`：按 active workspace 校验目录、`client.setWorkspace(rootPath)` 重锚、广播 `WorkspaceChanged`。
- 启动期 `DARVIN_AGENT_WORKSPACE` env：由 active workspace 的 RootPath 提供（无 active 时用默认/首个 workspace，仍无则临时指向 userData 下占位目录并在 UI 层引导）。
- 新增 IPC：`darvin:list_workspaces` / `darvin:create_workspace` / `darvin:set_active_workspace` / `darvin:delete_workspace`；`switchSession` 保持按 session 切，但内部改为「先确保该 session 所属 workspace 成为 active」再重锚。
- 删除 session 的磁盘清理逻辑：仅当 workspace 下已无其他 session 时才允许回收默认目录；用户自选目录仍不 rm。

### FR-5 renderer 视图与 composable
- 新增 `useWorkspaces` composable：`workspaces / activeWorkspaceId / createWorkspace / switchWorkspace / deleteWorkspace`，订阅 `WorkspacesChanged` push（或复用现有 sessions push 时序 + 新增 push）。
- 新增 `WorkspacesView`（工作区首屏）：
  - 空态：hero 标题 + 「创建第一个工作区」按钮 → 内联表单（名称输入 + 目录选择，复用 `FolderPicker`）→ 创建
  - 列表：workspace 卡片（名称 / label / 会话数 / 更新时间）+「进入」按钮 + 「新建工作区」
  - 选择/新建后：`set_active_workspace` → `navigate('chat')`。
- `HomeView` 改造：仅在有 active workspace 时展示；顶部加 workspace chip（名称 + chevron，点击回工作区首屏或展开切换）；空态 hero 参考 deepseek `HeroShell/HeroGlow` 的视觉骨架（标题 + 光晕 + chip + 输入框）。
- `PromptDock` 微调：补 IME 守卫（composition 期 Enter 不发送）、Shift+Enter 换行显式处理；`onSend` 仍 `newSession:true`，但 session 创建绑定 active workspace。
- 侧栏：顶部加 workspace 切换入口；会话列表按 active workspace 过滤（list_sessions 传 activeWorkspaceId）。
- `useViewMode` 新增 `'workspaces'` 模式；`AppShell.vue` 的 switch 登记 `WorkspacesView`；冷启动无 active workspace 时默认导航 `workspaces`。

### FR-6 i18n
- `workspace.*` 字典簇：`workspace.empty.title` / `workspace.empty.cta` / `workspace.new.name` / `workspace.new.pick` / `workspace.new.submit` / `workspace.enter` / `workspace.switch` / `workspace.delete` / `workspace.placeholder.label` 等（zh/en 同步）。
- 涉及含数字拼接处一律 `formatNumber`（如会话数）。

## 4. 实现方案

### 4.1 Go 数据层（`internal/agents/store`）

新增 `models.go` 内 `Workspace` 模型 + `workspace_store.go`（`SQLiteWorkspaceStore`：`List` / `GetByID` / `GetByRoot` / `Create` / `UpdateTitle` / `Delete` / `CountSessions`）；`Session` 加 `WorkspaceID string ` + 新方法 `ListByWorkspace`。

### 4.2 Go 网关（`internal/gateway`）

- 新建 `handler_workspace_crud.go` 放 5 个 `handle*Workspace*`（符合 F2 god file 按业务域拆分）。
- `handler_session.go`：`CreateSessionParams` 加 `WorkspaceID`；创建时将 `WorkspaceID` 写入 `store.Session`；`toSessionWire` 带上 workspaceId。
- `Handler` 结构增加 `WorkspaceStore`；`runtime/database.go` 的 `Stores` 与装配点注入。

### 4.3 main 进程（`src/main/index.ts` + `libs/user-paths.ts` + `workspace-map.ts`）

- 新增 `libs/workspaces.ts`：封装 workspaces IPC 调用 + 本地缓存 + `activeWorkspaceId` 维护。
- `resolveWorkspaceRoot(workspaceId)` 改为查 Go `list_workspaces`/缓存；`workspace-mapping.json` 保留用于兼容读取（迁移后不再写入新键）。
- `workspaceLoc.workspaceId = workspaceId`；`followActiveWorkspace(workspaceId)` 重写。

### 4.4 renderer（`src/renderer/`）

- `composables/useWorkspaces.ts`（沿用 useSession 的「thin view + 订阅 push」模式）。
- `views/WorkspacesView.vue` + `components/workspaces/WorkspaceCard.vue` / `NewWorkspaceForm.vue`。
- `views/HomeView.vue` 顶部加 `components/home/WorkspaceChip.vue`（视觉参照 deepseek WorkspaceChip：folder icon + label + chevron）。
- `components/home/PromptDock.vue` 补 IME / Shift+Enter 处理。
- `services/i18n.ts` 加 `workspace.*` keys。

### 4.5 迁移与兼容

版本号不变、无独立 migration 文件：GORM AutoMigrate 幂等补列建表。**存量回填由 main 进程启动期执行**（不在 Go runtime 装配期）：Go 的 `config.UserDataDir()` 落在 `.../darvin-agent` 子目录，推导不出旧默认路径 `userData/workspaces/<sid>`，只有 main 知道旧布局与 `workspace-mapping.json` 自定义映射。main 启动时对 `workspaceId` 为空的 session 逐个 `create_workspace`（沿用旧路径）→ `bind_session_workspace`，幂等（已迁移则跳过）。

> 实现偏离记录（相对初始设计）：① 回填从「Go runtime 装配期」改为「main 启动期」，原因见上；② `create_workspace` 的 rootPath 由 main 计算并必传（仅起名时 main 用 `userData/workspaces/<uuid>` 兜底），Go 不自行推导默认路径；③ `set_workspace_root_to` 语义从「重锚当前 session 目录」改为「创建或切换到以该目录为根的工作区」（选目录 = 选工作区）；④ 迁移前遗留 session 的 imported_files 因目录未变而原样保留。

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| 无 active workspace 冷启动 | 直接进入 `workspaces` 首屏，不误建默认 workspace |
| workspace 目录被手动删除 | 进入时 `ensureWorkspaceRoot` 重建空目录（不恢复文件）；文件丢失不叫错 |
| 删除 workspace 时其下仍有会话 | v0 视为「非空不可删」，UI 提示先清理其会话（或提供级联确认） |
| 旧 session 自定义目录已失效 | 沿用现有 `resolveWorkspaceRoot` 失效清理逻辑，迁移时仍建 workspace 记录但标记重建目录 |
| 空 workspace（无会话）进入 | 直接落 hero 空态 + 聊天框，发首条消息即建首个 session |
| 中文/空格路径 | rootPath 全程绝对路径透传，不参与字符串拼接定位；FolderPicker 已支持 |
| 切换 workspace 时在途流式 | 保留现有「不重启 Go 子进程、仅 set_workspace 重锚」策略，其他会话 in-memory 上下文不丢 |
| 多窗口同时操作 | 不做分布式锁；以最后一次写为准（原型可接受） |

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/darvin-agent/internal/agents/store/models.go` | 新增 `Workspace` 模型；`Session` 加 `WorkspaceID` |
| `src/darvin-agent/internal/agents/store/workspace_store.go` | 新增 workspace CRUD store |
| `src/darvin-agent/internal/agents/store/session_store.go` | 新增 `ListByWorkspace` |
| `src/darvin-agent/internal/runtime/database.go` | AutoMigrate 追加 `Workspace`；backfill |
| `src/darvin-agent/internal/runtime/runtime.go` | Stores 装配 WorkspaceStore + 启动 env workspace 派生 |
| `src/darvin-agent/internal/gateway/handler_workspace.go` | 保留（set_workspace 重锚沙箱） |
| `src/darvin-agent/internal/gateway/handler_workspace_crud.go` | 新增 5 个 workspace handler + bind_session_workspace + WorkspaceWire |
| `src/darvin-agent/internal/gateway/handlers_workspace_test.go` | 新增 workspace-first 流程单测 + workspace 硬门禁单测 |
| `src/darvin-agent/internal/gateway/handler_session.go` | create_session 支持 workspaceId；toSessionWire 带出 |
| `src/darvin-agent/internal/gateway/handlers.go` | Handler 注入 WorkspaceStore |
| `src/shared/darvin-api.ts` | DarvinSession 加 workspaceId；workspace 请求/响应类型；DarvinApi 新方法；WorkspacesChanged push |
| `src/preload/index.ts` | 暴露 workspace API |
| `src/main/index.ts` | workspaceLoc 语义改造 + followActiveWorkspace + 新 IPC + 启动 env |
| `src/main/libs/user-paths.ts` | resolveWorkspaceRoot 改走 workspace 记录 |
| `src/main/libs/workspace-map.ts` | 兼容读取，不再写新键 |
| `src/main/runtime/manager.ts` | spawn env 传 active workspace root |
| `src/renderer/composables/useWorkspaces.ts` | 新增 composable |
| `src/renderer/composables/useSession.ts` | createSession 带 workspaceId |
| `src/renderer/composables/useChatActions.ts` | send 新建分支绑定 active workspace |
| `src/renderer/views/WorkspacesView.vue` | 新增工作区首屏 |
| `src/renderer/views/HomeView.vue` | 顶部 workspace chip + 无 active 时引导 |
| `src/renderer/components/home/WorkspaceChip.vue` / `NewWorkspaceForm.vue` | 新增组件 |
| `src/renderer/components/chat/PromptDock.vue` | IME / Shift+Enter 处理 |
| `src/renderer/layout/AppShell.vue` + `composables/useViewMode.ts` | 登记 workspaces 视图 |
| `src/renderer/components/sidebar/Sidebar*.vue` | 顶部 workspace 切换 + 会话按工作区过滤 |
| `src/renderer/services/i18n.ts` | `workspace.*` keys |

## 7. 验收标准

- [ ] 场景 1~5 的 Given/When/Then 全部通过
- [ ] 冷启动无 workspace → 落在工作区首屏，创建后自动进入聊天并新建会话即建 session
- [ ] 一个 workspace 内可开多个会话，会话列表按工作区过滤，切工作区会话跟随切换
- [ ] 首页聊天框行为对齐 deepseek 参照：workspace chip 常驻可切换 / 输入框自增高、Enter 发送、Shift+Enter 换行、IME 组合期不误发
- [ ] 旧数据迁移后会话与 imported_files 原样可见，无报错
- [ ] 通过 `npm run lint`（TS/Vue 全量）
- [ ] 通过 `cd src/darvin-agent && go build ./... && go vet ./...`（Go 端）
- [ ] `npm run test`（vitest：user-paths / IPC wire 解析的单测补覆盖）
- [ ] 手动走查：`npm start` 起 Electron，反复进出工作区 + 发消息 + 断 Go 进程重启，确认 workspace 重锚与 imported_files 跟随正确

> 实现引导：写完本 spec 后，先按「4.1 → 4.2 → 4.3 → 4.5 → 4.4」切层落地（Go 数据/网关 → main → 迁移 → renderer），每层跑对应 lint/单测；如实现中发现本 spec 有遗漏或偏差，回头更新本文件再继续。