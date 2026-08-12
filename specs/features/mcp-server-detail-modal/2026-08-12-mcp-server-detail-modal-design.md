# MCP 服务器详情弹窗设计文档

## 1. 概述

### 1.1 问题 / 背景

当前 MCP 服务器卡片把工具 / 资源 / 提示内联展开在卡片里（工具 chips + schema 展开、资源 / 提示 toggle 列表），卡片信息过载、越来越臃肿。用户希望把这三类能力展示**收进弹窗**，卡片只保留核心信息 + 一个「详情」按钮。

### 1.2 目标

- 卡片精简：移除内联的 tools / resources / prompts 展示区。
- 新增「详情」按钮（位于 测试连接 和 编辑 之间），点击弹出该服务器的工具 / 资源 / 提示详情弹窗。
- 弹窗内保留现有的安全徽章（R / D）与工具 schema 展开能力。

### 1.3 非目标

- 不改「运行日志」抽屉（留在卡片）。
- 不改失败详情展示。
- 不重做工具 schema 交互逻辑（原样搬进弹窗）。

## 2. 用户场景

### 场景 1: 查看服务器能力
**Given** MCP 页有一张服务器卡片
**When** 用户点卡片上的「详情」按钮（测试连接 和 编辑 之间）
**Then** 弹出「服务器详情」弹窗，展示 工具（含 R/D 徽章、点开 schema）、资源、提示 三个区块

### 场景 2: 卡片精简
**Given** 服务器已连接
**Then** 卡片只显示 名称 / 状态徽章 / 描述 / 传输行 / 日志抽屉 / 失败详情 + 操作按钮，不再内联工具列表

## 3. 功能需求

### FR-1: 卡片新增「详情」按钮
- `McpServerCard.vue` 底部操作区在 测试连接 之后插入「详情」按钮（`data-testid="mcp-details-<id>"`），emit `details(server)`。

### FR-2: 移除卡片内联能力展示
- 移除 card 的 tools 展示区（chips + schema）与 resources / prompts toggle 列表。
- 保留 日志抽屉、失败详情、transport 行。

### FR-3: 新增 `McpServerDetailModal.vue`
- Teleport to body 弹窗（复用 FormModal 的遮罩 / 布局模式）。
- 标题「服务器详情」，头部显示服务器名 / 传输类型 / URL。
- 三个区块：
  - **工具**：chips + R / D 安全徽章，点击展开 schema（复用卡片现有逻辑）。
  - **资源**：打开时懒加载 `listResources(id)`，空显示「（空）」。
  - **提示**：打开时懒加载 `listPrompts(id)`，空显示「（空）」。
- 工具数据源：`server.exposedTools`，缺失时 `fetchTools(id)` 兜底。
- 关闭：× 按钮 + 遮罩点击 + Esc。

### FR-4: McpView 接线
- 新增 `detailsServer` / `detailModalOpen` 状态 + `openDetails / closeDetails`，渲染 DetailModal。

## 4. 实现方案

### 4.1 `McpServerCard.vue`
- 删除 tools 相关（`displayTools` / `fallbackTools` / `onMounted fetchTools` / `expandedTool` / `toggleTool` / `schemaText`）与 resources / prompts 相关（`resourcesOpen` / `promptsOpen` / `resources` / `prompts` / `toggleResources` / `togglePrompts`）逻辑与模板区块。
- `useMcpServers` 解构只保留 `getLogs`。
- emits 增加 `details: [server: DarvinMcpServer]`。
- 操作区在 测试连接 后插入：
  ```html
  <button
    type="button"
    class="font-sans text-xs text-text-muted transition-colors hover:text-text"
    :data-testid="`mcp-details-${server.id}`"
    @click="emit('details', server)"
  >
    {{ t('mcp.action.details') }}
  </button>
  ```

### 4.2 `McpServerDetailModal.vue`（新组件）
- props: `open: boolean; server: DarvinMcpServer | null`
- emits: `close: []`
- 内部状态：
  - `tools = server.exposedTools ?? fallbackTools`（`fallbackTools` 由 `fetchTools(id)` 兜底，连接态下才拉）。
  - `resources` / `prompts`：`open` 变 true 时懒加载 `listResources` / `listPrompts`，一次性。
  - `expandedTool`：工具 chip 点击展开 schema（同卡片逻辑）。
- 模板：`Teleport to body` + 遮罩 + 居中卡片；区块头用 `mcp.detail.tools / resources / prompts`，空态用 `mcp.cap.empty`；内容区 `max-h-[70vh] overflow-y-auto`。

### 4.3 `McpView.vue`
- `import McpServerDetailModal`；新增 `detailsServer = ref<DarvinMcpServer | null>(null)`、`detailModalOpen = ref(false)`、`openDetails(server)` / `closeDetails()`。
- 卡片 `@details="openDetails"`；模板渲染 `<McpServerDetailModal :open="detailModalOpen" :server="detailsServer" @close="closeDetails" />`。

### 4.4 i18n（`src/renderer/services/i18n.ts`，dictZh / dictEn 同步）

| key | zh | en |
|-----|-----|-----|
| `mcp.action.details` | 详情 | Details |
| `mcp.detail.title` | 服务器详情 | Server details |
| `mcp.detail.tools` | 工具 | Tools |
| `mcp.detail.resources` | 资源 | Resources |
| `mcp.detail.prompts` | 提示 | Prompts |

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| 服务器未连接 | 工具空，资源 / 提示懒加载返回空，显示「（空）」 |
| `exposedTools` 缺失 | `fetchTools` 兜底（仅连接 / error 态拉取，避免无用请求） |
| 弹窗打开时服务器被删除 | 不特殊处理；弹窗持有 server 快照，展示空态，可接受 |
| 工具数量多 | 弹窗内容区滚动（`max-h-[70vh] overflow-y-auto`） |
| Esc / 遮罩点击 | 触发 `close` |

## 6. 涉及文件

| 文件 | 变更 |
|------|------|
| `src/renderer/components/mcp/McpServerCard.vue` | 移除内联能力展示 + 新增「详情」按钮 + emit |
| `src/renderer/components/mcp/McpServerDetailModal.vue` | 新增：详情弹窗（工具 / 资源 / 提示） |
| `src/renderer/views/McpView.vue` | 接线详情弹窗 |
| `src/renderer/services/i18n.ts` | 新增 5 个 key（zh / en 同步） |

## 7. 验收标准

- [ ] 场景 1：点「详情」弹出弹窗，展示 工具（含 R/D 徽章 + schema 展开）、资源、提示
- [ ] 场景 2：卡片不再内联工具 / 资源 / 提示
- [ ] 「详情」按钮位于 测试连接 和 编辑 之间
- [ ] `npm run lint` 通过
- [ ] 手动：`npm start` 打开 MCP 页，对已连接服务器点「详情」验证弹窗展示；对 tools-only 服务器（如 github）验证资源 / 提示显示空态不报错
