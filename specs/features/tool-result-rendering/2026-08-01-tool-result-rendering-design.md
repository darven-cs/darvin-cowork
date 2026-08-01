# 工具结果渲染

> 编号 **02**，把 `tool_start` / `tool_end` 事件落地为可视组件。**依赖 00-darvin-api-extension**；与 01 共用 turn 容器。

## 1. 背景

`darvin-api.ts` 已定义 `tool_start { tool, input }` / `tool_end { tool, output }` 事件（事件侧 `toolUseId` 补丁见 §4.5），但 `useMessages.appendToBucket` 完全不消费这两个事件，`SidePanelContent.vue` 的 tools tab 是空态占位。LobsterAI 在 turn 内部按 `toolKind` 分发专门渲染器（Bash 仿终端 / TodoWrite checkbox / Edit DiffView）。

## 2. 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 协议扩展：`tool_use` / `tool_result` 消息类型 + `toolUseId` 配对 | 00 已落地消息类型；事件侧 `toolUseId` 由本 spec 前置补丁落地（见 §4.5） |
| G2 | `ToolCallGroup` 组件：按 `toolKind` 选渲染器，默认折叠 | Bash / Read / Edit / TodoWrite / Web 5 个分支 |
| G3 | Bash 渲染：仿终端（三色圆点 + 黑底 + `$ ` 前缀） | `kind='bash'` 触发 |
| G4 | TodoWrite 渲染：三态 checkbox（completed/in_progress/pending） | `kind='todowrite'` 触发 |
| G5 | Edit 渲染：DiffView 红绿对比 | `kind='edit'` 触发 |
| G6 | 通用渲染：Input / Result 两分区 + 不同限高 | 其他 kind 走通用 |
| G7 | 状态指示：蓝脉冲（流中）/ 蓝实心（无 result）/ 绿（成功）/ 红（错误） | `isError` + 配对状态 |
| G8 | 大文本截断：`>4KB` 折叠显示前 200 字符 + KB/MB 大小摘要 | `formatToolResult()` |
| G9 | 工具名称归一化：`Read/ReadFile → Read`、`Bash/Exec/Shell → Bash` | `getToolDisplayName()` |

## 3. 非目标

- 不实现工具权限确认弹窗（`permissions` 是后续 spec）
- 不实现工具结果搜索
- 不实现工具调用重试（仅 UI 钩子）

## 4. 设计要点

### 4.1 组件树

```
AssistantTurnBlock.vue
└── ToolCallGroup.vue                    ← 新（核心）
    ├── ToolCallHeader.vue                ← 工具名 + 状态点 + 折叠按钮
    ├── ToolCallInput.vue                 ← Input 折叠区
    │   ├── BashInput.vue
    │   ├── TodoWriteInput.vue
    │   ├── EditInput.vue
    │   └── GenericInput.vue
    ├── ToolCallResult.vue                ← Result 折叠区
    │   ├── BashResult.vue
    │   ├── EditResult.vue                ← DiffView
    │   ├── TodoWriteResult.vue
    │   └── GenericResult.vue
    └── ToolCallFooter.vue                ← 复制 / 重试 / 折叠全部
```

### 4.2 工具名称归一化

```ts
const ALIAS_MAP: Record<string, DarvinToolKind> = {
  ReadFile: 'read', WriteFile: 'write', EditFile: 'edit',
  Exec: 'bash', Shell: 'bash', Run: 'bash',
  ListDir: 'read', Glob: 'read',
  WebSearch: 'web_search', WebFetch: 'web_fetch',
};

export function getToolDisplayName(tool: string): string {
  return ALIAS_MAP[tool] ?? tool;
}

export function getToolKind(tool: string): DarvinToolKind {
  return ALIAS_MAP[tool] ?? (tool as DarvinToolKind);
}
```

### 4.3 状态点颜色映射

| 条件 | 样式 |
|---|---|
| `tool_use` 收到，`tool_result` 未到 | `bg-blue-500 animate-pulse` |
| `tool_use` 收到，`tool_result` 未到，且 stream 收尾 | `bg-blue-500` |
| `tool_result` 收到，`isError=false` | `bg-green-500` |
| `tool_result` 收到，`isError=true` | `bg-red-500` |

### 4.4 大文本截断策略

```ts
const COLLAPSE_THRESHOLD = 4 * 1024;
const PREVIEW_LINES = 12;

export function getToolResultCollapsedDisplay(output: unknown): {
  preview: string;
  sizeLabel: string;
  lineCount: number;
  isTruncated: boolean;
} {
  const text = typeof output === 'string' ? output : JSON.stringify(output, null, 2);
  const isTruncated = text.length > COLLAPSE_THRESHOLD;
  return {
    preview: text.split('\n').slice(0, PREVIEW_LINES).join('\n'),
    sizeLabel: formatBytes(text.length),  // KB / MB
    lineCount: text.split('\n').length,
    isTruncated,
  };
}
```

### 4.5 事件侧 `toolUseId` 补丁（spec 00 遗留，本 spec 的前置）

spec 00 把 `tool_use` / `tool_result` 加进了 `DarvinMessage` 消息 union（自带 `toolUseId`），但**没有改 `tool_start` / `tool_end` 事件类型**。当前事件 wire 与「按 `toolUseId` 配对」的需求不一致：

| 层 | tool 事件字段 |
|---|---|
| Go `eventledger.go` | `tool_start { tool, input, message: { id: CallID } }` / `tool_end { tool, output, message: { id } }` |
| TS `DarvinEvent.tool_start / tool_end` | 声明 `messageId: string`（字段名与 Go 的 `message.id` 不一致，且无 `toolUseId`） |

**决议**：

1. 扩展 `DarvinEvent` 的 `tool_start` / `tool_end` 成员，增加 `toolUseId?: string`。
2. `parseDarvinEvent` 对这两个 type 从 raw 的 `message.id`（Go 注入的 CallID）提升出 `toolUseId` 附加到事件对象上，**不改 Go**。
3. `useMessages` 接管 `tool_start` / `tool_end`，按 `toolUseId` 配对 `tool_use → tool_result`（参考 LobsterAI `cowork/messageDisplayUtils.ts:540-552` 按 `message.metadata.toolUseId` 分组）。

> 实现顺序：先改 `src/shared/darvin-api.ts` 事件类型 + `src/main/runtime/client.ts` 的 `parseDarvinEvent`，再写 `ToolCallGroup` 配对逻辑。`toolUseId` 缺失时按 messageId 兜底（保持向后兼容）。

## 5. 用户场景

### 场景 1：agent 跑 bash 命令

**Given** LLM 调 `bash` tool，input `{ command: 'ls -la /tmp' }`，output `total 12\ndrwxr-xr-x ...`

**When** 两条消息按 `toolUseId` 配对

**Then** 渲染为仿终端（黑底 + 三色圆点 + `$ ls -la /tmp` 命令行 + output 区）；默认折叠；点开看完整 output

### 场景 2：Edit 工具改文件 → 展示 diff

**Given** `edit` tool input `{ file: 'foo.ts', old_string: 'a', new_string: 'b' }`，output `{ success: true }`，`isError=false`

**When** 渲染

**Then** `EditResult` 走 `DiffView`，红删 / 绿增；header 状态点绿色

### 场景 3：工具失败

**Given** `bash` tool output `{ exitCode: 1, stderr: 'command not found' }`，`isError=true`

**When** 渲染

**Then** 状态点红色；Result 区显示 stderr；error message 走 `t('tool.error.noDetail')` 兜底

## 6. 验收

- [ ] Bash / Read / Write / Edit / TodoWrite / WebSearch 6 个内置 kind 全部有专门渲染
- [ ] 默认折叠；用户能展开/折叠；状态记忆
- [ ] 大文本（>4KB）显示截断预览 + 大小摘要 + 「展开」按钮
- [ ] 状态点 4 色行为正确
- [ ] `getToolDisplayName` 归一化单元测试覆盖
- [ ] `useMessages` 接管 `tool_start` / `tool_end` 事件，配对逻辑单元测试覆盖

## 7. 依赖

- **前置**：00-darvin-api-extension
- **可并行**：01-agent-output-rendering
- **后置**：03 / 04 / 05 都会引用 `ToolCallGroup`

## 8. 参考

### darvin-cowork
- `src/shared/darvin-api.ts` — `tool_start` / `tool_end` 事件（§4.5 需补 `toolUseId`）
- `src/renderer/composables/useMessages.ts` — `appendToBucket`（当前不处理工具事件）
- `src/renderer/components/side-panel/SidePanelContent.vue` — 空态占位

### LobsterAI（借鉴）

> 参考项目根目录：`~/桌面/github-project/LobsterAI`（下述路径均相对该项目根）。组件实现遇阻时直接查该项目源码。

- `src/renderer/components/cowork/ToolCallGroup.tsx:83-387` — 核心
- `src/renderer/components/cowork/messageDisplayUtils.ts:93-189` — `getToolDisplayName` / `formatToolInput` / `getToolResultDisplay` / `getToolResultCollapsedDisplay` / `getLargeToolResultSummary`
- `src/renderer/components/cowork/DiffView.tsx`
- `src/renderer/components/cowork/TodoWriteInputView.tsx`

## 9. 关联调研

`specs/features/agent-output-ux-research/2026-08-01-cowork-vs-lobsterai-comparison.md` § 2.2「工具结果」+ § 3「协议层 gap」
