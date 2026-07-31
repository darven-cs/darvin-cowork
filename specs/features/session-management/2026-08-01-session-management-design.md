# 会话管理设计文档

## 1. 概述

### 1.1 问题 / 背景

会话管理目前只有「列表展示 + 切换」，缺少基本的增删改查能力，且新建会话的时机不符合直觉。用户反馈分四块：

1. **会话缺少操作**：不能修改名字、不能删除、不能查询/搜索。
2. **新建会话时机不对**：点侧栏「新建任务」立即在 main 端建一个空会话（`Sidebar.onNavigate` 直接 `createSession()`），但此时用户还没发任何内容，导致一堆空会话堆积在列表里。
3. **侧栏布局**：「会话展示无法滑动，操作无法预览」——会话列表在高窗口下不出现滚动条、底部分布被裁掉；会话项 hover 没有任何可点操作，无法预览能做什么。
4. **聊天区溢出**：「聊天区域超过之后聊天消失」——消息变长后内容被裁掉，看不到也滚不动。

### 1.2 目标

- 会话支持**重命名 / 删除 / 搜索**，操作入口在会话项 hover（或 active）时可见。
- 点击「新建任务」不再建空会话，只进入 compose 态；**发出第一条消息时才真正创建会话**，并用首条消息作标题。
- 修复侧栏会话列表与聊天区的滚动/溢出问题，保证任意消息量下都可滚动查看。

### 1.3 非目标

- 不做会话归档（`status: archived` 目前仅落库，无 UI）。
- 不做会话批量操作、不做删除撤销（undo）。
- 搜索页不做按时间区间/模型过滤等高级筛选，先覆盖「标题 + 消息内容」两类匹配。

## 2. 用户场景

### 场景 1: 懒创建会话
**Given** 用户点击侧栏「新建任务」
**When** 系统进入 compose 态（不创建任何 session 记录）
**Then** 用户输入第一条消息并发送，此时才创建一个新会话（标题取首条消息），随后进入 chat 视图；侧栏列表多出该会话。

### 场景 2: 重命名会话
**Given** 侧栏有若干会话
**When** 用户 hover 某会话项，点击「⋯」→ 选择「重命名」
**Then** 标题变成内联输入框，Enter/失焦保存、Esc 取消；保存后列表与 ChatHeader 标题同步更新。

### 场景 3: 删除会话
**Given** 侧栏有会话，其中一个是当前 active
**When** 用户对该会话点「删除」，并在下拉确认层点「确认删除」
**Then** 会话与消息从 SQLite 级联删除；若删的是 active，main 自动切到下一个会话；若删完没剩会话，UI 退空态。

### 场景 4: 查询/搜索会话（完整搜索页）
**Given** 用户点击侧栏「搜索任务」导航项，进入搜索页
**When** 用户在搜索框输入关键字
**Then** 按会话标题 + 消息内容匹配，分组展示结果；点击任一结果切换到对应会话并进入聊天视图。

### 场景 5: 聊天滚动
**Given** 单个会话有大量/超长消息
**When** 消息超出可视区域
**Then** 消息区出现滚动条，可滚动到最早/最新消息；输入框与底栏保持固定在视口内。

## 3. 功能需求

### FR-1: 懒创建会话
- 侧栏「新建任务」点击只进入 compose 态（`draftMode`），不调用 main `createSession`。
- `send()`（HomeView 与 ChatView 共用）在 `draftMode` 或 active session 为空时，先 `createSession({ title: 首条消息 })` 再 `prompt`。
- 点击已有会话（`switchSession`）时退出 `draftMode`。

### FR-2: 重命名会话
- 新增 IPC `darvin:rename_session(sessionId, title)`，main 复用 `SessionStore.updateTitle`，成功后 `broadcastSessions()`。
- 会话项 hover 显示「⋯」菜单（active 项常显），菜单含「重命名」/「删除」。
- 重命名：内联编辑，Enter/失焦提交、Esc 取消；空标题回退原值。

### FR-3: 删除会话
- 复用已有 `darvin:delete_session`。
- 删除需确认：菜单内先显示「删除后不可恢复」确认层，点确认才删除。
- 删除后清掉 renderer 对应 message bucket。

### FR-4: 查询/搜索会话（完整搜索页）
- 新增视图 `SearchView`，接入 `useViewMode` 的 `search` 模式；侧栏「搜索任务」导航项点击进入。
- 新增 IPC `darvin:search_sessions(query)`，main 在 SQLite 中按**会话标题**与**消息内容**子串匹配（LIKE，大小写不敏感）。
- 结果分组展示：「会话」（标题命中）与「消息」（内容命中，附所属会话标题）。
- 点击任一结果 → `switchSession(该会话)` + 进入 chat 视图。
- 空查询显示引导文案；无命中显示「无匹配结果」。

### FR-5: 侧栏滚动修复
- 固定区（Brand / Nav / AgentCard / Bottom）加 `shrink-0`，会话区保持 `min-h-0 flex-1`，会话列表 `flex-1 overflow-y-auto`。
- aside 加 `overflow-hidden`，防止内容溢出视口。

### FR-6: 聊天区滚动修复
- `MessageList` / HomeView 问候区根节点加 `min-h-0`，避免 column flex 子项因 `min-height:auto` 撑破父容器、把底栏挤出视口。
- **实测补充（验收发现）**：仅给 `MessageList` 加 `min-h-0` 不够——AppShell 网格项（视图根，`<component class="col-start-2">`）默认 `min-height:auto`，长内容时整列被撑到视口外（注入 2400px 后根容器长到 2901px，输入框被挤到 3014px）。修复：网格项加 `min-h-0`，消息区恢复 `scrollH > clientH` 正常滚动，输入框固定可见。

### FR-7: 会话操作事件转发（验收发现）
- `SessionItem` 的 `rename` emit 为双参 `(id, title)`，但 `SessionList` 用 `@rename="emit('rename', $event)"` 转发时 `$event` 只取第一个参数，`title` 丢失 → IPC 里 `undefined.trim()` 抛错、重命名静默失败。
- 修复：`SessionList` 改为显式转发 `@rename="(id, title) => emit('rename', id, title)"`。

## 4. 实现方案

### 4.1 契约层：新增 rename IPC

`src/shared/darvin-api.ts`：
- 新增响应类型 `DarvinRenameSessionResponse { session: DarvinSession }`。
- `DarvinApi` 增加 `renameSession(sessionId: string, title: string): Promise<DarvinRenameSessionResponse>`。

`src/preload/index.ts`：`renameSession` → `ipcRenderer.invoke('darvin:rename_session', sessionId, title)`。

`src/main/index.ts`：
```ts
ipcMain.handle('darvin:rename_session', (_e, sessionId: string, title: string) => {
  const ok = store.updateTitle(sessionId, title.trim() === '' ? '新建会话' : title.trim());
  if (!ok) throw new Error('session not found');
  broadcastSessions();
  return { session: store.getSession(sessionId) };
});
```

### 4.2 useSession：draftMode + rename + delete 清理

`src/renderer/composables/useSession.ts`：
- 新增 `draftMode = ref(false)`。
- `startNewTask()`：`draftMode.value = true`（不碰 main）。
- `renameSession(id, title)`：调 IPC，成功后原地替换 `sessions.value` 中的项。
- `deleteSession(id)`：删除后调 `useMessages().removeSession(id)` 清 bucket；若删除的恰好是 active 且 `nextActiveSessionId === null`，视图由 main push 的空 active 自然退回空态。

`src/renderer/composables/useMessages.ts`：新增 `removeSession(sessionId)`，删掉 `messagesBySessionId` 对应 bucket，并从 `streamingSessionIds` / `unreadSessionIds` 中剔除。

### 4.3 send 懒创建

`src/renderer/composables/useChatActions.ts`：
```ts
async function send(content, busyRef) {
  if (!content.trim()) return;
  let sessId = session.activeSessionId.value;
  if (session.draftMode.value || sessId === null) {
    const created = await session.createSession({ title: content.trim().slice(0, 30) });
    sessId = created.id;
    session.draftMode.value = false;
  }
  // 原逻辑：append user msg → prompt → start assistant msg
}
```

### 4.4 Sidebar 接线

`src/renderer/components/sidebar/Sidebar.vue`：
- `currentId` 派生时若 `draftMode` 则返回 `''`（active 高亮不显示）。
- `onNavigate('new_task')`：`session.startNewTask()` + `emit('navigate', 'home')`，去掉 `createSession()` 与无监听的 `emit('new-chat')`。
- `onSelect(id)`：`switchSession` 后 `session.draftMode.value = false`。
- 给 `<SidebarBrand /> <SidebarNav /> <SidebarAgentCard /> <SidebarBottom />` 加 `shrink-0`；aside 加 `overflow-hidden`。

`src/renderer/components/chat/ChatHeader.vue`：标题派生时 `draftMode` 显示 `t('app.new_chat')`。

### 4.5 搜索页 + 会话项操作菜单

**搜索页**：
- 新增 `src/renderer/views/SearchView.vue`，接入 `useViewMode`（`ViewMode` 加 `'search'`），`AppShell` 的 `currentView` 增加 `case 'search'`。
- `SessionStore` 新增：
  ```ts
  searchSessions(query: string): DarvinSession[]       // title LIKE
  searchMessages(query: string): SearchMessageHit[]    // content LIKE, join 会话标题
  ```
- `src/main/index.ts` 新增 `darvin:search_sessions` handler，返回 `{ sessions, messages }`。
- `SearchView` 输入框 debounce 300ms 后调 `window.darvin.searchSessions(query)`，分组渲染；点击结果 `switchSession(id)` + `goChat()`。

**会话项操作菜单**：
- `SessionList.vue` 保持「列表 + 暂无会话空态」，不加搜索框。
- `src/renderer/components/sidebar/SessionItem.vue`：
- hover（或 active 常显）显示「⋯」按钮（`more` icon），用现有 `Dropdown` 包菜单。
- 菜单：「重命名」「删除」。
  - 重命名 → `editing` ref 置真，标题变 `<input>`，Enter 提交 `emit('rename', id, title)`、Esc 取消、失焦提交；空值回退。
  - 删除 → 菜单内切换成确认层（文案 + 取消/确认按钮），确认后 `emit('delete', id)`。
- 新增 emits：`rename: [id, title]`、`delete: [id]`。

`Sidebar.vue` 接收这些事件，调 `session.renameSession(...)` / `session.deleteSession(...)`。

### 4.6 滚动/溢出修复

- `src/renderer/components/chat/MessageList.vue`：根节点 `flex-1 overflow-y-auto px-6 py-8` → 加 `min-h-0`。
- `src/renderer/views/HomeView.vue`：问候区 `flex-1 overflow-y-auto` → 加 `min-h-0`。
- `src/renderer/components/sidebar/Sidebar.vue`：如上加 `shrink-0` / `overflow-hidden`。
- `src/renderer/layout/AppShell.vue`：视图组件 `class="col-start-2"` → `class="col-start-2 min-h-0"`。**这是聊天溢出的真正根因**：网格项默认 `min-height:auto`，长内容时撑破 800px 视口把底栏挤出。实测注入 2400px 长内容后 `MessageList` 容器 `scrollH == clientH == 2901`（不滚动）、输入框 `bottom=3014`；加 `min-h-0` 后 `scrollH=2901 > clientH=610`、输入框 `bottom=723` 固定在视口内。

### 4.8 会话操作事件转发修复（验收发现）

- `src/renderer/components/sidebar/SessionList.vue`：`@rename="emit('rename', $event)"` 会丢掉第二个参数 `title`，改为 `@rename="(id, title) => emit('rename', id, title)"`。实测修复前 UI 重命名无效、IPC 抛 `undefined.trim()`；修复后重命名生效且 DB 持久化。

### 4.7 图标

新增 `src/renderer/assets/icons/`：
- `edit.svg`（铅笔）
- `trash.svg`（垃圾桶）
- `more.svg`（⋯ 水平省略号）

遵循项目图标规则：`viewBox="0 0 34 34"`、`stroke="currentColor"`、round caps、`stroke-width` 与现有图标保持一致。

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| 重命名为空字符串 / 全空白 | 回退为「新建会话」并提交 |
| 删除当前 active 会话 | main `deleteSession` 已自动切下一个 active 并 broadcast；renderer 同步清 bucket |
| 删除最后一个会话 | `nextActiveSessionId = null`，UI 退空态，home/chat 显示空 |
| 删除正在 streaming 的会话 | main 删除前先按 (sessionId, runId) abort（已有逻辑）；renderer 清 streaming/unread 标记 |
| draftMode 下点击已有会话 | `switchSession` + 退出 draftMode |
| draftMode 下直接发消息 | `send()` 先建会话再发，标题取首条消息 |
| 搜索空查询 | 显示引导文案，不发起 IPC |
| 搜索无结果 | 分组各自显示「无匹配」空态 |
| 小窗口高度不足 | 固定区 `shrink-0` + 会话区 `min-h-0` 滚动；极端小窗固定区占满时会话区可缩到 0，接受 |
| 离线 / Go agent 未起 | session 读写走 main 本地 SQLite，不受影响；`prompt` 失败已有 error 气泡兜底 |

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/shared/darvin-api.ts` | 新增 `DarvinRenameSessionResponse` + `renameSession`；搜索请求/响应类型 + `searchSessions` |
| `src/preload/index.ts` | 新增 `renameSession` / `searchSessions` 转发 |
| `src/main/index.ts` | 新增 `darvin:rename_session` / `darvin:search_sessions` handler |
| `src/main/store/SessionStore.ts` | 新增 `searchSessions` / `searchMessages` |
| `src/renderer/composables/useSession.ts` | 新增 `draftMode` / `startNewTask` / `renameSession`；`deleteSession` 接清理 |
| `src/renderer/composables/useMessages.ts` | 新增 `removeSession` |
| `src/renderer/composables/useChatActions.ts` | `send()` 懒创建会话 |
| `src/renderer/composables/useViewMode.ts` | `ViewMode` 增加 `'search'` |
| `src/renderer/views/SearchView.vue` | 新增完整搜索页 |
| `src/renderer/layout/AppShell.vue` | `currentView` 增加 `case 'search'`；视图组件加 `min-h-0`（聊天溢出根因修复） |
| `src/renderer/components/sidebar/Sidebar.vue` | new_task 懒创建、search 导航接线、draftMode 处理、`shrink-0`/`overflow-hidden`、会话操作事件转发 |
| `src/renderer/components/sidebar/SessionList.vue` | 空态文案 i18n（暂无会话）；`rename` 事件显式转发 `(id, title) => emit(...)` 修复丢参 |
| `src/renderer/components/sidebar/SessionItem.vue` | hover「⋯」菜单 + 重命名内联编辑 + 删除确认 |
| `src/renderer/components/chat/ChatHeader.vue` | draftMode 标题 |
| `src/renderer/components/chat/MessageList.vue` | `min-h-0` 溢出修复 |
| `src/renderer/views/HomeView.vue` | 问候区 `min-h-0` |
| `src/renderer/services/i18n.ts` | zh/en 各新增会话操作/搜索文案 key |
| `src/renderer/assets/icons/edit.svg` | 新增 |
| `src/renderer/assets/icons/trash.svg` | 新增 |
| `src/renderer/assets/icons/more.svg` | 新增 |
| `src/renderer/assets/icons/x.svg` | 新增（搜索清空按钮） |

## 7. 验收标准

- [x] 场景 1：点「新建任务」不产生会话；发首条消息后才创建，标题取首条消息（CDP 实测通过）
- [x] 场景 2：hover/active 项可见「⋯」，重命名内联编辑 Enter/失焦保存、Esc 取消，标题同步到列表与 ChatHeader（CDP 实测通过，含 4.8 丢参 bug 修复）
- [x] 场景 3：删除有确认层；删 active 自动切下一个；删空退空态；对应 message bucket 清理（CDP 实测通过）
- [x] 场景 4：「搜索任务」进入搜索页，按标题/消息内容命中分组展示，点击结果切换会话并进 chat（CDP 实测通过）
- [x] 场景 5：长消息下消息区滚动、底栏固定；侧栏会话多时可滚动（CDP 实测通过，含 4.6 网格项 min-h-0 修复）
- [x] `npm run lint` 通过（0 error / 0 warning）
- [x] i18n zh/en key 集合一致（`assertSameKeys` dev 校验通过）
- [x] 手动 `npm start`：DevTools 无 console error（仅 dev 模式 Electron CSP 安全警告，与本次改动无关）；新建→发消息→重命名→搜索→删除 全流程经 CDP 自动化验证
