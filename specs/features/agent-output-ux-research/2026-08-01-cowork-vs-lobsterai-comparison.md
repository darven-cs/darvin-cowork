# Agent 输出 / 工具结果 / Token-上下文 UI 对比清单

> 调研性文档（**非 spec**）。目的：把 darvin-cowork 当前 UI 实现与 LobsterAI 对照，找出差异、列出可借鉴的具体模块，供后续 spec（Agent 输出重做 / 工具结果面板 / 上下文压缩 UI / Token 用量可视化）输入。
>
> 调研日期：2026-08-01
> 参考项目：`~/桌面/github-project/LobsterAI`（React + Redux Toolkit + Tailwind 桌面应用）
> 当前项目：`darvin-cowork`（Vue3 + Tailwind v4 + Go agent）

---

## 1. 总览：两边的差距在「完整度」而非「技术选型」

| 维度 | darvin-cowork 现状 | LobsterAI 现状 | 差距性质 |
|---|---|---|---|
| 消息类型 | 协议已扩 5 种 union（spec 00），渲染仍只画 2 种 | 5 种（user / assistant / tool_use / tool_result / system） | 协议已补齐，渲染层缺 |
| 流式渲染 | 纯文本 + 三点动画 | react-markdown + KaTeX + GFM + CodeMirror | 渲染层整层缺 |
| 工具结果 | `tool_start`/`tool_end` 事件定义存在但 UI **完全不消费** | `ToolCallGroup` + Bash/TodoWrite/Edit/Diff 多种专门渲染 | 整层缺 |
| Token 用量 | `DarvinUsage` 已补 cache 字段（spec 00），UI **完全无展示** | `CoworkMessageMetadata.usage` + `ContextUsageIndicator` 圆环可视化 | 数据已通，UI 整层缺 |
| 上下文压缩 | `compaction` 事件已进 `DarvinEvent`（spec 00 不再静默丢弃），UI 入口/动画/分隔符缺 | 圆环点击 → 手动压缩 / `status='compacting'` 旋转 / `AssistantTurnBlock` 压缩分隔符 | 协议已通，UI 缺 |
| 侧栏 | 简单会话列表 + 静态 Agent 卡片 | 树形 Agent + 会话 + 子会话 + 拖拽 + pin + parent session | 复杂，差异化重 |
| 右侧面板 | 三个 tab 全空态占位 | `ArtifactPanel` + artifactSlice + 10 种 artifact 渲染器 | 整层缺 |
| 设置 | 5 个 tab，3 个有占位 | 12 个 tab，IM/MCP/dreaming/embedding 等深度功能 | 广度差距大 |
| 首页 | Hero 问候 + QuickActions + PromptDock | 同样三件套，但 PromptDock 有草稿/附件/skill 引用 | 细节差距 |
| 主题 / token | `@theme` 块已分层 | 完整 token contract + UI/Code 字号可调 | 体系成熟度差距 |

---

## 2. 逐维度对比

### 2.1 Agent 输出

| 项 | darvin-cowork | LobsterAI |
|---|---|---|
| 协议字段 | `DarvinEvent.text_delta / thinking_delta / tool_start / tool_end / done / error / agent_end` | `CoworkMessageType = 'user' \| 'assistant' \| 'tool_use' \| 'tool_result' \| 'system'` + `CoworkMessageMetadata.usage/isStreaming/isThinking/toolName/toolInput/toolResult/error/...` |
| 消息结构 | `Message { id, sessionId, role, content, done, error?, toolLabel?, createdAt }` | `CoworkMessage { id, type, content, timestamp, metadata? }` |
| 流式 | `text_delta` 累积到 `message.content`（`useMessages`） | 同上，但走 Redux `coworkSlice.upsertMessage` + `turnById` 模型 |
| Markdown | ❌ 纯 `whitespace-pre-wrap` | ✅ `react-markdown + remark-gfm + remarkMath + rehypeKatex`（`MarkdownContent.tsx`） |
| 代码块 | ❌ 无 | ✅ CodeMirror 6（`CodeBlock.tsx`，支持高亮/折叠/搜索/diff） |
| 大文档 | ❌ 无 | ✅ 8KB 阈值切头 4KB + 尾 8KB（`shouldUseLargeMarkdownPreview()`） |
| 思考 | ✅ `thinking_delta` + `toolLabel="Darvin · 思考中"` 占位 | ✅ `ThinkingBlock` 折叠 + 流式自动展开 + 蓝色脉冲点 + 300px 滚动 |
| 图片/本地媒体附件 | ❌ 无 | ✅ `imageAttachments` / `localMediaAttachments` 元数据 + 缩略图 |
| 选中文字片段 | ❌ 无 | ✅ `selectedTextSnippets` 徽章 |
| Proposed plan 确认 | ❌ 无 | ✅ `AssistantMessageItem` 解析 `<proposed_plan>` 标签 + 确认/调整按钮 |
| Fork / 分叉 | ❌ 无 | ✅ `onFork(messageId)` hover 按钮 + `CoworkForkMode = 'none'/'conversation'/'worktree'` |
| 复制按钮 | ❌ 无 | ✅ hover 出现 `showCopyButton` |
| 时间戳 / 模型标签 | hover 显示 message id | hover 显示时间戳 + 模型名 + fork 按钮 |

**关键 darvin-cowork 文件**：
- `src/shared/darvin-api.ts` — 协议 union（spec 00 已扩 5 种 type + cache / attachment / compaction / context_usage / artifact）
- `src/renderer/composables/useMessages.ts` — `appendToBucket` 只处理 text/thinking/done/error
- `src/renderer/components/chat/MessageItem.vue` — 纯文本 bubble
- `src/renderer/components/chat/StreamingText.vue` — 纯文本白板

**LobsterAI 借鉴目标**：
- `src/renderer/components/MarkdownContent.tsx`
- `src/renderer/components/CodeBlock.tsx`
- `src/renderer/components/cowork/ThinkingBlock.tsx:12-72`
- `src/renderer/components/cowork/AssistantMessageItem.tsx:54-237`
- `src/renderer/components/cowork/ConversationTurnsView.tsx:21-147`
- `src/renderer/components/cowork/AssistantTurnBlock.tsx`（含 `ContextCompactionDivider`）

---

### 2.2 工具结果

| 项 | darvin-cowork | LobsterAI |
|---|---|---|
| 协议事件 | `tool_start { tool, input }` / `tool_end { tool, output }`（已定义） | `CoworkMessageType = 'tool_use' \| 'tool_result'` + `metadata.toolUseId / toolInput / toolResult / isError` |
| 工具归一化 | ❌ 无 | ✅ `getToolDisplayName()`：`Read/ReadFile → Read`，`Bash/Exec/Shell → Bash` |
| 折叠 / 展开 | ❌ 无 | ✅ 默认折叠；`shouldExpandByDefault = isMediaStatusPoll(group)` 时自动展开 |
| Input / Result 分区 | ❌ 无 | ✅ 两个 section + `max-h-72` / `max-h-48` 不同限高 |
| Bash 专用渲染 | ❌ 无 | ✅ 仿终端三色圆点 + `$ ` 前缀 + 黑底 inset |
| TodoWrite 专用渲染 | ❌ 无 | ✅ 三态 checkbox（completed/in_progress/pending） |
| Edit / Diff 专用渲染 | ❌ 无 | ✅ `DiffView` 红绿对比 |
| 状态指示 | ❌ 无 | ✅ 蓝脉冲（流中）/ 蓝实心（无 result）/ 绿（成功）/ 红（错误） |
| 错误展示 | ❌ 无 | ✅ `text-red-500` + 无 detail 时显示 `coworkToolNoErrorDetail` i18n |
| 大文本截断 | ❌ 无 | ✅ `getToolResultCollapsedDisplay()` 折叠摘要 + `getLargeToolResultSummary()` 显示 KB/MB |
| 行数摘要 | ❌ 无 | ✅ `getToolResultLineCountSummary()` |
| 复制 / 重试 | ❌ 无 | ✅ 复制按钮 + 重试可派生 |
| SidePanel tools tab | 空态占位"暂未发生工具调用" | n/a（LobsterAI 把工具嵌在 turn 内部，不在 side panel） |

**LobsterAI 借鉴目标**：
- `src/renderer/components/cowork/ToolCallGroup.tsx:83-387`（核心）
- `src/renderer/components/cowork/messageDisplayUtils.ts:93-189`（formatToolInput / getToolResultDisplay / getToolResultCollapsedDisplay / getLargeToolResultSummary）
- `src/renderer/components/cowork/TodoWriteInputView.tsx`
- `src/renderer/components/cowork/DiffView.tsx`

---

### 2.3 Token / 上下文用量

| 项 | darvin-cowork | LobsterAI |
|---|---|---|
| 协议 | `DarvinUsage { inputTokens, outputTokens, cacheReadTokens?, cacheWriteTokens?, totalTokens }`（spec 00 已补 cache 字段） | `CoworkMessageMetadata.usage { inputTokens?, outputTokens?, cacheReadTokens?, cacheWriteTokens? }` |
| 事件 | `done` 携带 `usage?`；`context_usage` 事件已进 union（spec 00） | `done` 携带 `usage` + 额外 push `CoworkContextUsage` |
| 上下文容量 | ❌ 无 | ✅ `CoworkContextUsage { usedTokens, contextTokens, percent, compactionCount, status, latestCompactionCheckpointId, latestCompactionReason, latestCompactionCreatedAt, model, updatedAt }` |
| 状态机 | ❌ 无 | ✅ `status: 'unknown' \| 'normal' \| 'warning' \| 'danger' \| 'compacting'` |
| 圆环可视化 | ❌ 无 | ✅ `ContextUsageIndicator` SVG 圆环（`RADIUS=7, CIRCUMFERENCE=43.96`），12 点方向起笔 |
| 颜色 | ❌ 无 | ✅ 状态映射颜色（normal/warning/danger/compacting） |
| 数字展示 | ❌ 无 | ✅ tooltip 显示 `coworkContextUsagePercent` + `coworkContextUsageTokens` |
| 手动压缩 | ❌ 无 | ✅ 圆环点击 → `onCompact()` |
| 自动压缩 | ❌ 无 | ✅ 状态 `compacting` 时圆环持续旋转动画 |
| 压缩历史 | ❌ 无 | ✅ `compactionCount` + `latestCompaction*` 字段 |
| 压缩边界可视化 | ❌ 无 | ✅ `ContextCompactionDivider`（`AssistantTurnBlock.tsx` 内部） |
| 压缩后 i18n | ❌ 无 | ✅ `coworkContextCompacting / AutoCompacted / ManualCompacted / CompactionFailed` |

**LobsterAI 借鉴目标**：
- `src/renderer/components/cowork/ContextUsageIndicator.tsx:40-121`
- `src/renderer/types/cowork.ts:103-116`（`CoworkContextUsage`）
- `src/shared/cowork/constants.ts`（`CoworkContextUsageSource: 'live' | 'cache' | 'unavailable'`、`CoworkContextUsageRefreshMode: 'auto' | 'manual' | 'postRun'`）
- `specs/features/cowork-context-compaction/2026-05-08-cowork-context-compaction-design.md`
- `specs/features/cowork-context-compaction/2026-06-09-cowork-context-compaction-quality-optimization-design.md`

---

### 2.4 侧栏

| 项 | darvin-cowork | LobsterAI |
|---|---|---|
| 宽度 | 220px 固定 | 220-420px 可拖拽，MIN 220 / MAX 420 |
| 折叠 | ✅ 折叠时整列 `0px`（会跳） | ✅ 折叠时图标模式 |
| 导航 | 6 个：新建任务/搜索/定时任务/专家套件/技能/MCP（scheduled / skill / mcp 仅 warn） | 5 个：cowork / skills / scheduledTasks / kits / mcp + 全部有真实面板 |
| 会话列表 | ✅ `SessionList` + `SessionItem` | ✅ `CoworkSessionList` + `CoworkSessionItem`（含 status: idle/running/completed/error, pinned, parentSessionId, forkMode, goal） |
| 拖拽排序 | ❌ 无 | ✅ 支持 |
| Agent 卡片 | ❌ 静态 `SidebarAgentCard` | ✅ 树形 `MyAgentSidebarTree` → `AgentTreeNode` → `AgentTaskRow`（多层嵌套 + batch selection） |
| 会话搜索/过滤 | ❌ 无 | ❌ 无（搜索在主区，不在侧栏） |
| 底部操作 | Login / Settings | 用户信息 + 登出 + 设置 |
| 快捷键 | ❌ 6 个绑定面板只展示 | ✅ `ShortcutAction.OpenCowork/ScheduledTasks/Kits/Skills/Mcp` = `CommandOrControl+1-5` |

**LobsterAI 借鉴目标**：
- `src/renderer/components/Sidebar.tsx`
- `src/renderer/components/agentSidebar/MyAgentSidebarTree.tsx`
- `src/renderer/components/agentSidebar/AgentTreeNode.tsx`
- `src/renderer/components/agentSidebar/AgentTaskRow.tsx`

---

### 2.5 设置面板

| Tab | darvin-cowork | LobsterAI |
|---|---|---|
| 通用 | ❌ | ✅ language/autoLaunch/sqliteBackup/proxy/notifications |
| 外观 | ✅ 主题 + 语言 radio | ✅ 主题（light/dark/system）+ UI 字号 11-16 + 代码字号 8-24 + 主题色 |
| 模型 | ✅ Provider(硬编码 anthropic) + API Key + Base URL | ✅ `ModelSettingsSection`（119KB），多 provider + API 格式选择 |
| Agent 引擎 | ❌ | ✅ OpenClaw 引擎状态 + 启动/重启/修复 |
| 记忆 | ❌ | ✅ memory enabled + embedding 配置 |
| 梦境 | ❌ | ✅ `DreamingSettingsSection`（频率/模型/时区） |
| 浏览器 Web 访问 | ❌ | ✅ |
| 快捷键 | ✅ 6 个仅展示 | ✅ `ShortcutConfig` 真绑定 |
| IM | ❌ | ✅ 钉钉/飞书/微信/QQ/Telegram/Discord/WeCom/NIM/POPO + email（161KB） |
| Email | ❌ | ✅ `EmailSkillConfig` |
| 插件 | ❌ | ✅ `PluginsSettings` |
| 关于 | ✅ 版本/架构/许可 | ✅ 版本/手册/社群/条款/导出日志 |
| 账号 | ✅ username/email 只读 + logout 仅 warn | 隐含在通用设置里 |
| 总 tab 数 | 5 | 12 |

**LobsterAI 借鉴目标**：
- `src/renderer/components/Settings.tsx`（242KB，需要看入口与 tab 切分，不要全读）
- `src/renderer/components/Settings/SettingsOpenOptions` 入口参数模式

---

### 2.6 右侧工具面板 / Artifact Panel

| 项 | darvin-cowork | LobsterAI |
|---|---|---|
| 容器 | ✅ `SidePanel.vue` 300px | ✅ `ArtifactPanel.tsx`（258KB，宽度 180-1000 可调） |
| Tab | tools / thinking / artifact（tools 空态占位；artifact 待 spec 05 内嵌 LobsterAI 式面板） | 3 个特殊 tab：`fileList` / `browser` / `subagents` + 每 artifact 一个 preview tab |
| 状态 | 单一 `isOpen` ref + localStorage | `artifactSlice` 完整状态机：artifactsBySession / previewTabsBySession / activePreviewTabIdBySession / panelOpenBySession / panelWidth / selectedArtifactId |
| 内容 | 空 | 10 种 artifact 渲染器 |
| 持久化 | localStorage 一项 | Redux 状态 + session 维度 |
| 拖拽改宽 | ❌ | ✅ 180-1000px 拖拽 |
| Panel 关闭 | ✅ 单一 ref 控制 | ✅ `togglePanel` / `closePanel` action |

**LobsterAI 借鉴目标**：
- `src/renderer/components/artifacts/ArtifactPanel.tsx`（入口 + tab 切分 + 拖拽）
- `src/renderer/store/slices/artifactSlice.ts:46-54`（状态机）
- `src/renderer/services/artifactParser.ts`（类型分派）
- `src/renderer/components/artifacts/ArtifactRenderer.tsx:22-54`（switch-case 路由）
- `src/renderer/components/artifacts/renderers/*`（10 个渲染器）
- `src/renderer/components/artifacts/renderers/HtmlRenderer.tsx:112-131`（sandbox=allow-scripts）
- `src/renderer/components/artifacts/renderers/MermaidRenderer.tsx`（securityLevel: 'strict'）

---

### 2.7 首页 / 提示输入

| 项 | darvin-cowork | LobsterAI |
|---|---|---|
| 问候语 | ✅ `HeroGreeting.vue` 按小时段 | ✅ `resolveHomeGreetingKey()` 同模式，更精致 |
| 快捷操作 | ✅ 4 个 qa-tile（PPT/数据/文档/搜索） | ✅ `quick-actions/QuickActionBar.tsx` |
| 提示输入 | ✅ `PromptDock.vue` + `Composer.vue`（多 modal 共用） | ✅ `CoworkPromptInput.tsx`（156KB）支持草稿/附件/skill 引用/kit 引用 |
| 工具栏 | ✅ PlusMenu/MicButton/ModelPicker | ✅ 同上 + skill/kit 徽章显示 |
| 草稿持久化 | ❌ 无 | ✅ `draftAttachments` / `draftSkillIds` |
| 附件 | ✅ `ImportButton` + `ImportedFilesBar` | ✅ 同上 + `displayImageAttachments` |
| 模型选择 | ✅ `ModelPicker` 3 个 hardcoded | ✅ `MediaModelPicker.tsx`（46KB）多模态 |
| 录音 | ❌ MicButton stub | ✅ 真实录音 + 转写 |

**LobsterAI 借鉴目标**：
- `src/renderer/components/cowork/CoworkPromptInput.tsx`（仅看草稿/附件/skill 引用逻辑）
- `src/renderer/components/cowork/CoworkView.tsx:50-56`（问候语）

---

### 2.8 主题 / 设计 Token

| 项 | darvin-cowork | LobsterAI |
|---|---|---|
| 主题切换 | ✅ `useTheme.ts` dark/light + localStorage | ✅ 同上 + 'system' 选项 + ThemeManager engine |
| Token 来源 | ✅ `styles/theme.css` `@theme` 块 | ✅ `theme/tokens/contract.ts` + `theme/engine/theme-manager.ts` + CSS Generator |
| Token 命名 | `--color-bg / surface / text / accent / agent-*` | `--lobster-{category}-{name}`（brand/accent/surface/chat/text/border/status） |
| 字体 | ✅ `--font-display / sans / mono` 3 套（Fraunces / Inter Tight / JetBrains Mono via Google Fonts CDN） | ✅ 同 3 套 |
| 字号 | ❌ 写死 | ✅ UI 11-16 / Code 8-24 可调，存 `FontPreferences` |
| 用户自定色 | ❌ 无 | ✅ `appearance` 选主题色 |

**LobsterAI 借鉴目标**：
- `src/renderer/theme/tokens/contract.ts`（命名规范）
- `src/renderer/config.ts:59-66`（字号范围）

---

### 2.9 i18n

| 项 | darvin-cowork | LobsterAI |
|---|---|---|
| 结构 | `dictZh` + `dictEn` 平铺 key-value，约 140 key | 单文件 `translations: Record<LanguageType, Record<string, string>>`，zh+en 等量 |
| 切换 | `setLang()` 改 ref，已响应式（`t()` 在 render 期读 `ref.value`，Vue 自动 re-render） | 响应式（依赖 Redux + ref 订阅） |
| 校验 | ✅ dev-mode `assertSameKeys` | 同 |
| 插值 | ❌ 仅字面量 | ✅ `replace('{placeholder}', value)` |
| 覆盖范围 | app/sidebar/chat/sidepanel/home/expert/settings/model/plus/imported | cowork/tool/skill/mcp/im/scheduled/agent/artifacts/context 全面 |

**LobsterAI 借鉴目标**：
- `src/renderer/services/i18n.ts`（314KB，看头部结构即可）

---

## 3. 协议层 gap 摘要（写 spec 时第一关要解决的）

> 状态标记：`✅ 已落地（spec 00）` / `⚠️ 部分（spec 02 §4.5 补齐）` / `⛔ 仍缺`

darvin-cowork 的 `DarvinEvent` 在 `darvin-api.ts` 定义 `tool_start` / `tool_end`，逐项 gap 状态如下：

1. **`messageId ↔ toolUseId` 关联** ✅ 已落地（spec 02 §4.5）：`parseDarvinEvent` 从 Go 的 `message.id` 提升出 `toolUseId`，`useMessages` 按它配对 `tool_use → tool_result`。
2. **tool 类型** ✅ 已落地（渲染层）：spec 00 提供 `DarvinToolKind`；spec 02 `getToolKind()` 把事件侧裸 `tool: string` 归一化成 kind 分发（`Exec/Shell → bash` 等）。Go 事件仍发裸 string，但渲染层已收敛。
3. **`isError` 字段** ✅ 已落地（spec 02）：`tool_result` 消息类型带 `isError`；`tool_end` 事件渲染层从 output 推断（`inferToolEndError`：error 字段 / 非零 exitCode / stderr / `<tool_use_error>` 标签）。
4. **cache token** ✅ 已落地：`DarvinUsage` 已补 `cacheReadTokens` / `cacheWriteTokens`。
5. **compaction 事件** ✅ 已落地：`DarvinEvent.compaction` 正式成员 + `client.ts` 不再静默丢弃。
6. **`contextUsage` push** ✅ 已落地（协议层）：`DarvinEvent.context_usage` 成员 + `DarvinContextUsage` 类型。
7. **`isThinking` / `isStreaming` 元数据** ✅ 已落地（协议层）：`DarvinMessage.assistant` 成员含 `isStreaming` / `isThinking`。
8. **artifact 事件** ✅ 已落地：`DarvinEvent.artifact` 成员 + `DarvinArtifactKind`。
9. **image / local-media attachment 元数据** ✅ 已落地（协议层）：`DarvinAttachment` 类型 + `DarvinMessage.user.attachments?`。

---

## 4. 已拆分 spec 清单

> 落地跟踪表见 [`CHECKLIST.md`](./CHECKLIST.md)。每份 spec 的核心 FR + 进度状态在那里维护；本调研 doc 只做索引与交叉参考，不再承载可执行 checklist。

下表是把上方"对比" + "协议层 gap" 内容拆成 9 份独立 spec 的索引。每份 spec 自带「参考 / 依赖 / 借鉴文件」段落；本调研 doc 仅作总览与交叉参考。

| # | spec | 一句话范围 | 依赖 | 优先级 |
|---|------|-----------|------|--------|
| 00 | [darvin-api-extension](../darvin-api-extension/2026-08-01-darvin-api-extension-design.md) | ✅ 已完成：扩 `DarvinMessage` 为 discriminated union + 新增 `compaction` / `context_usage` / `artifact` 事件 + 补 cache / toolUseId / isError | — | **P0（前置）** |
| 01 | [agent-output-rendering](../agent-output-rendering/2026-08-01-agent-output-rendering-design.md) | ✅ 已完成：Markdown（markdown-it+KaTeX） / Shiki 代码块 / ThinkingBlock / turn 模型 / hover 元信息 / 大文档截断 / 图片附件 | 00 | P1 |
| 02 | [tool-result-rendering](../tool-result-rendering/2026-08-01-tool-result-rendering-design.md) | ✅ 已完成：`ToolCallGroup` + Bash 仿终端 / TodoWrite checkbox / Edit DiffView / 状态点 4 色 / 折叠 / 大文本截断 / 工具归一化 | 00 | P1 |
| 03 | [token-context-usage](../token-context-usage/2026-08-01-token-context-usage-design.md) | 单条消息 token 展示 + chat header 圆环可视化 + 5 态颜色 + tooltip | 00 | P1 |
| 04 | [context-compaction-ui](../context-compaction-ui/2026-08-01-context-compaction-ui-design.md) | 手动压缩入口 + 自动压缩动画 + `ContextCompactionDivider` + 失败回退 | 00 + 03 | P1 |
| 05 | [artifact-panel](../artifact-panel/2026-08-01-artifact-panel-design.md) | 状态机重做 + 10 种 artifact 渲染器 + iframe sandbox + 面板宽度拖拽 | 00 | P2 |
| 06 | [sidebar-upgrade](../sidebar-upgrade/2026-08-01-sidebar-upgrade-design.md) | 树形 Agent / 220-420px 拖拽 / 5 tab 真实入口 / `Cmd+1-5` 快捷键 / session 5 态 status | — | P2 |
| 07 | [settings-expansion](../settings-expansion/2026-08-01-settings-expansion-design.md) | 7 个 tab 拆分 / 字号可调 / 多 provider 模型 / 压缩次数显示 | 04 | P2 |
| 08 | [i18n-enhancement](../i18n-enhancement/2026-08-01-i18n-enhancement-design.md) | 插值 / 响应式切换 / 补齐 60+ 新 key / `Intl.NumberFormat` / 缺 key 告警 | — | P1 |

### 启动顺序

1. **00**（协议先行，**不写 UI**）
2. **01 / 02 / 03** 并行（基础渲染 + 工具 + token 展示，互不依赖）
3. **04**（依赖 03 圆环组件）
4. **05 / 06 / 07 / 08** 并行（05 依赖 00；07 依赖 04；06 / 08 无前置）

### 调研 doc 的角色

本文件仅作为"对比 + 协议层 gap"的源头记录；后续开发请直接读对应 spec 文件，每个 spec 内部的「LobsterAI 借鉴文件」段落是当前 checklist 的实际落点。
