# Workspace CRUD 迭代设计文档（增/删/改/查 闭环）

## 1. 概述

### 1.1 问题 / 背景

首版 `2026-08-17-workspace-first-sessions-design.md` 已建立 workspace 的一等实体与「先选 / 建 workspace 再开会话」的前端流程，但 CRUD 尚未闭环：

| 操作 | 现状 |
|---|---|
| 增（Create） | ✅ Go / API / UI 完备 |
| 查（Read / List） | ✅ 列表 + active + 按 root 反查 |
| 改（Update） | ❌ 缺失：store 有 `UpdateName` 但未挂 handler；缺 rootPath 迁移；UI 无入口 |
| 删（Delete） | ⚠️ 仅允许删空 workspace（`agent.delete_workspace` 对非空硬拒，UI 也仅 `sessionCount===0` 时显示） |

本次迭代补齐「改」+ 「删（含级联）」，完成完整增删改查。

### 1.2 目标

1. 重命名 workspace（更新 `Name`），含重名校验与友好错误。
2. 修改 workspace 的 `RootPath`（即把整个工作区搬到另一目录），同时让 Go 运行时 sandbox / skills / tools 跟随重锚。
3. 级联删除非空 workspace（连同其下所有 session、imported_files，默认磁盘目录一并回收；用户自选目录不 rm）。

### 1.3 非目标

- workspace 元数据扩展（tags / 描述等）。
- 批量操作（一次改 / 删多个 workspace）。
- 拖拽排序、跨 workspace 迁移 session。

### 1.4 设计参照

deepseek-harness `ui-workspace/src/client/WorkspaceBrowser.tsx`：workspace 组的重命名与删除均由「browser-owned modal」承载（即重命名 / 删除模态由浏览区而非行项自身持有，跨组展开 / 收起不破坏中态）。本次沿用同一模式：重命名 + 改目录共一个模态，删除一个独立模态，二者均挂在管理页面 `WorkspacesView`，生命周期独立于卡片。

## 2. 用户场景

### 场景 1：重命名 workspace
**Given** 已在 `WorkspacesView` 看到 workspace 列表
**When** 点 workspace 卡片上的「重命名」按钮
**Then** 弹出重命名 / 改目录模态（仅名称 tab 激活），输入新名 → 校验 trim 后非空 + 不与其它 workspace 重名 → 保存 → 模态关闭 → 卡片显示新名，侧栏分组头与工作区选择器 chip 同步更新

### 场景 2：修改 workspace 目录
**Given** 同上
**When** 点「改目录」按钮（模态中或管理行上的入口）→ 模态弹系统选目录 → 选/创建 → 确认
**Then** workspace `RootPath` 更新，主进程 `client.setWorkspace` 重新锚定 sandbox / skills / tools；WorkspacePicker 与侧栏 `rootPath` 同步；该 workspace 下的 imported_files 路径仍正确（相对新根的解析不变）

### 场景 3：删除空 workspace
**Given** workspace `sessionCount === 0`
**When** 点删除按钮 → 模态确认「确认删除此工作区？」
**Then** 删除 workspace 行与默认磁盘目录（用户自选目录保留）

### 场景 4：删除含会话的 workspace（级联）
**Given** workspace `sessionCount > 0`
**When** 点删除按钮 → 模态展示「将同时删除 N 个会话（含 imported_files 与默认磁盘目录）」+ 确认按钮
**Then** 确认后：workspace 行删除；该 workspace 下所有 session 行删除（沿用现有 `agent.delete_session` 的 messages / usage / digests / imported_files 级联）；active session 若在其中则切到 `nextActiveWorkspaceId`；默认磁盘目录回收

## 3. 功能需求

### FR-1 `agent.rename_workspace`（Go JSON-RPC）

- 入参 `{ workspaceId: string, name: string }`。
- 校验：trim 后 `name` 非空（空名 → `CodeInvalidParams`）；与其它 workspace（除自身外）重名 → `CodeConflict`（新增）返回 `name taken`。
- 行为：写 `Name` 与 `UpdatedAt`，返回 `WorkspaceWire`（`SessionCount` 重新统计）。
- 错误码：复用现有 `CodeInvalidParams` / `CodeInternalError`；新增 `CodeConflict = -32014`。

### FR-2 `agent.update_workspace_root`（Go JSON-RPC）

- 入参 `{ workspaceId: string, rootPath: string }`。
- 校验：绝对路径（`filepath.IsAbs`）→ `CodeInvalidParams`；与其它 workspace 的 `rootPath` 冲突 → `CodeConflict`；dir 不存在则 `os.MkdirAll` 兜底。
- 行为：
  1. `SQLiteWorkspaceStore.UpdateRoot(ctx, id, rootPath)`。
  2. 调 `h.SetWorkspaceRoot(rootPath)` 让 sandbox / project skills / MCP roots 跟随重锚。
  3. 返回新 `WorkspaceWire`（`sessionCount` 保留）。
- 新增 store 方法：`UpdateRoot(ctx, id, rootPath string) error`。

### FR-3 级联删除 `agent.delete_workspace`

- 入参新增可选 `{ force?: boolean }`（默认 false 保持兼容）。
- 行为：
  - `force === false` 且 `CountSessions > 0` → 仍返回 `CodeInvalidParams "workspace is not empty"`（与现状一致）。
  - `force === true`：先批量调用 `agent.delete_session` 删该 workspace 下所有 session（沿用现有级联：messages / usage / digests / imported_files）；再 `agent.delete_workspace` 删 workspace 行；返回 `DeleteWorkspaceResult` 加 `deletedSessionCount`。
- 出参：`{ deleted: bool, nextActiveWorkspaceId: string | null, deletedSessionCount: number }`。

### FR-4 main IPC 与缓存

- 新增 IPC handler：
  - `darvin:rename_workspace(req: { workspaceId, name })`：透传 Go + 刷新 cache + `broadcastWorkspacesChanged()`。
  - `darvin:update_workspace_root(req: { workspaceId, rootPath })`：透传 Go + 刷新 cache + 重锚本地 `workspaceLoc.rootPath`（若被改的为 active workspace）+ `broadcastWorkspacesChanged()`。
- 改造 `darvin:delete_workspace`：支持 `force` 参数；`force=true` 时直接调 Go 的 `delete_workspace { force: true }`（Go 端级联）；`force=false` 保持现状（仍然依赖前端 UI 限制）；返回 `deletedSessionCount` 转发给 renderer。
- `cleanupEmptyWorkspace`（会话删除后回收空 workspace）仍走原路径（`force=false`）。

### FR-5 共享 API

- `DarvinApi` 新增方法：
  - `renameWorkspace(req: { workspaceId: string, name: string }): Promise<DarvinWorkspace>`
  - `updateWorkspaceRoot(req: { workspaceId: string, rootPath: string }): Promise<DarvinWorkspace>`
  - `deleteWorkspace(workspaceId: string, opts?: { force?: boolean }): Promise<DarvinDeleteWorkspaceResponse>`
- `DarvinDeleteWorkspaceResponse` 加 `deletedSessionCount: number`。
- 新增 `DarvinCodeConflict = -32014` 供 renderer 区分错误码（命名按 dsh 风格，仅作常量）。

### FR-6 renderer：`useWorkspaces` 扩展

```ts
async function renameWorkspace(workspaceId: string, name: string): Promise<DarvinWorkspace>;
async function updateWorkspaceRoot(workspaceId: string, rootPath: string): Promise<DarvinWorkspace>;
async function deleteWorkspace(workspaceId: string, opts?: { force?: boolean }): Promise<DarvinDeleteWorkspaceResponse>;
```

### FR-7 renderer UI（`WorkspacesView` 模态）

- 卡片右侧操作组：图标按钮「重命名」/「改目录」/「删除」。
- 重命名 + 改目录**共用一个模态**（`WorkspaceEditModal.vue`）：
  - 标题 `t('workspace.edit.title')`。
  - 字段：`name`（input，校验非空 + 重名）、`rootPath`（只读展示 + 「选择目录」按钮触发 `darvin:set_workspace_root` 对话框回填）。
  - 「保存」按钮：保存两项的最新值；`name` 变更调 `renameWorkspace`，`rootPath` 变更调 `updateWorkspaceRoot`；两项都未变更不调接口。
  - 错误：在模态内 inline 显示 Go 返回的 `CodeConflict` / `CodeInvalidParams` 文本。
- 删除**独立模态**（`WorkspaceDeleteModal.vue`）：
  - `sessionCount === 0`：简洁「确认删除此工作区？默认磁盘目录一并删除」。
  - `sessionCount > 0`：红色警告 + 列出数字「将同时删除 N 个会话」+ 「取消」/「确认删除」按钮（确认调用 `deleteWorkspace(id, { force: true })`）。
- 模态期间不销毁卡片（按 dsh「browser-owned dialog outlives row unmount」原则）。
- i18n 新键（zh/en 对齐）：`workspace.edit.title` / `workspace.edit.name` / `workspace.edit.rootPath` / `workspace.edit.pick` / `workspace.edit.save` / `workspace.edit.cancel` / `workspace.rename.action` / `workspace.edit.rename` / `workspace.edit.move` / `workspace.delete.empty` / `workspace.delete.cascade` / `workspace.delete.confirm` / `workspace.delete.confirmForce` / `workspace.errors.conflict`。

### FR-8 行为一致性

- `WorkspacePicker.vue`（轻量下拉）保持只读 + 新建入口，不引入重命名 / 改目录入口。
- 侧栏 `SessionList.vue` workspace 分组头不引入重命名入口（保持只读浏览，重命名仍走管理页）。
- 改名 / 改目录后若该 workspace 为 active，主进程已锚定的 `workspaceLoc.rootPath` 同步更新，`client.setWorkspace` 已重锚 sandbox（FR-2 + FR-4）。

## 4. 实现方案

### 4.1 Go 数据层

- `internal/agents/store/workspace_store.go` 新增 `UpdateRoot(ctx, id, rootPath string) error`：与 `UpdateName` 同形，写 `RootPath` + `UpdatedAt`。
- `internal/agents/store/sqlite_store.go` / `memory.go` 同步（`MemoryStore` 当前无 Update 方法，因只有 handler-test stub 用；为对齐接口新增）。

### 4.2 Go 网关

- `handler_workspace_crud.go`：
  - 新增 `handleRenameWorkspace` / `handleUpdateWorkspaceRoot`，注册 dispatch。
  - 改造 `handleDeleteWorkspace`：读 `params.force`，force=true 时先批量删除 sessions（顺序：先非 active 会话，最后 active 避免 race）。
  - 新增 `CodeConflict = -32014`（`jsonrpc.go`）。
- `internal/gateway/handlers.go` 注册新 dispatch cases。

### 4.3 main 进程

- 新增 `darvin:rename_workspace` / `darvin:update_workspace_root` IPC handlers；`darvin:delete_workspace` 支持 `force`。
- `refreshWorkspaceCache()` + `broadcastWorkspacesChanged()` 在上述 IPC 出口调用。
- `update_workspace_root` 后若 `workspaceId === activeWorkspaceId`，同步 `workspaceLoc.rootPath`。

### 4.4 共享 API + preload

- `src/shared/darvin-api.ts`：新增 `DarvinCodeConflict` 常量 + 3 个方法签名 + `DarvinDeleteWorkspaceResponse` 加 `deletedSessionCount`。
- `src/preload/index.ts`：转发新增 3 个方法。

### 4.5 renderer

- `useWorkspaces.ts`：加 `renameWorkspace` / `updateWorkspaceRoot` / `deleteWorkspace(force)`。
- `WorkspacesView.vue`：每张卡片右侧操作组按钮 + 两个模态（`WorkspaceEditModal.vue` / `WorkspaceDeleteModal.vue`）。
- `i18n.ts`：zh/en 对齐新增键。

## 5. 边界情况

| 场景 | 处理方式 |
|---|---|
| 重名 trim 后为空 | 「名称不能为空」inline 错误，不调接口 |
| 重名与他 workspace 撞名 | Go 返 `CodeConflict`；前端转译为「名称已被使用」 |
| rootPath 与另一 workspace 撞根 | Go 返 `CodeConflict`；前端转译为「该目录已被其它工作区占用」 |
| rootPath 选已存在但当前为空 | `os.MkdirAll` 兜底；不报错 |
| rootPath 是用户自选目录（非默认） | 删除时仍走 `force` 级联，但**不** `fs.rm` 根目录（保留用户数据） |
| 级联删除 active workspace | 删除后切到 `nextActiveWorkspaceId`（Go handler 已计算）；main 端同步 `workspaceLoc=null` + `activeWorkspaceId=null`，再 `broadcastWorkspacesChanged()` |
| 改名 / 改目录期间 session 被并发删除 | 后端按 id 写入，无强一致性问题；UI 通过 `WorkspacesChanged` 推送刷新 |

## 6. 涉及文件

| 文件 | 变更说明 |
|---|---|
| `src/darvin-agent/internal/agents/store/workspace_store.go` | 新增 `UpdateRoot` |
| `src/darvin-agent/internal/agents/store/memory.go` | 新增 `UpdateRoot`（对齐接口，handler-test stub 用） |
| `src/darvin-agent/internal/gateway/handler_workspace_crud.go` | 新增 `rename_workspace` / `update_workspace_root` handlers；改造 `delete_workspace` 支持 `force` 级联 |
| `src/darvin-agent/internal/gateway/jsonrpc.go` | 新增 `CodeConflict = -32014` |
| `src/darvin-agent/internal/gateway/handlers.go` | 注册 2 个新 dispatch case |
| `src/darvin-agent/internal/gateway/handlers_workspace_test.go` | 新增 rename / update_root / cascade-delete 用例 |
| `src/shared/darvin-api.ts` | 新增 3 个方法 + `DarvinCodeConflict` + `deletedSessionCount` 字段 |
| `src/preload/index.ts` | 转发新增 3 个方法 |
| `src/main/index.ts` | 新增 2 个 IPC + `darvin:delete_workspace` 支持 `force`；refresh + broadcast |
| `src/renderer/composables/useWorkspaces.ts` | 新增 `renameWorkspace` / `updateWorkspaceRoot` / `deleteWorkspace(force)` |
| `src/renderer/views/WorkspacesView.vue` | 卡片操作组按钮 + 引入两个模态 |
| `src/renderer/components/workspaces/WorkspaceEditModal.vue` | 新增（重命名 + 改目录共用） |
| `src/renderer/components/workspaces/WorkspaceDeleteModal.vue` | 新增（含级联确认） |
| `src/renderer/services/i18n.ts` | 新增 zh/en 对齐键 |

## 7. 验收标准

- [ ] 场景 1~4 全部通过
- [ ] `npm run lint` 通过
- [ ] `cd src/darvin-agent && go build/vet/test` 通过（含 `handlers_workspace_test.go` 新增 rename / update_root / cascade-delete 用例）
- [ ] `vite build` main + preload + renderer 三目标通过
- [ ] 手动走查：`npm start` 起应用 → 打开 WorkspacesView → 重命名 / 改目录 / 删除（含级联确认）三个动作流程通畅，侧栏与 Picker 同步更新

> 实现引导：按 4.1 → 4.2 → 4.3 → 4.4 → 4.5 切层落地，每层跑对应 lint/单测；先入 store / handler，再 main / preload / renderer，最后接 UI 模态。