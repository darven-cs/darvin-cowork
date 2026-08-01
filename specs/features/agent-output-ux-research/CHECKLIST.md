# Agent 输出 / 工具 / Token-上下文 9 spec 跟踪表

> **中心化跟踪表**。每份 spec 的 § 6 验收是「设计层」细颗粒清单；本表是「执行层」一栏一格，落地时只勾这里。
>
> - 创建日期：2026-08-01
> - 调研 / 索引 doc：[`2026-08-01-cowork-vs-lobsterai-comparison.md`](./2026-08-01-cowork-vs-lobsterai-comparison.md)
> - 进度规则：spec 的核心 FR 全部勾上 → 标 `✅ 完成`；部分落地 → `🚧 进行中`；碰到阻塞 → `⛔ 阻塞`（要在后面写原因）

---

## 当前进度

**已完成 4 / 9。下一个该做的：04（context-compaction-ui）。**

| # | spec | 状态 | 进度 | 关键路径 |
|---|------|------|------|---------|
| 00 | [darvin-api-extension](./../darvin-api-extension/2026-08-01-darvin-api-extension-design.md) | ✅ 完成 | 9/9 | 协议先行 |
| 01 | [agent-output-rendering](./../agent-output-rendering/2026-08-01-agent-output-rendering-design.md) | ✅ 完成 | 9/9 | 依赖 00 |
| 02 | [tool-result-rendering](./../tool-result-rendering/2026-08-01-tool-result-rendering-design.md) | ✅ 完成 | 7/7 | 依赖 00 |
| 03 | [token-context-usage](./../token-context-usage/2026-08-01-token-context-usage-design.md) | ✅ 完成 | 6/6 | 依赖 00 |
| 04 | [context-compaction-ui](./../context-compaction-ui/2026-08-01-context-compaction-ui-design.md) | ⬜ 未启动 | 0/6 | 依赖 00 + 03 |
| 05 | [artifact-panel](./../artifact-panel/2026-08-01-artifact-panel-design.md) | ⬜ 未启动 | 0/7 | 依赖 00 |
| 06 | [sidebar-upgrade](./../sidebar-upgrade/2026-08-01-sidebar-upgrade-design.md) | ⬜ 未启动 | 0/7 | 无前置 |
| 07 | [settings-expansion](./../settings-expansion/2026-08-01-settings-expansion-design.md) | ⬜ 未启动 | 0/6 | 依赖 04 |
| 08 | [i18n-enhancement](./../i18n-enhancement/2026-08-01-i18n-enhancement-design.md) | ⬜ 未启动 | 0/6 | 无前置 |

**图例**：⏳ 待启动 / 🚧 进行中 / ✅ 完成 / ⛔ 阻塞

---

## 启动顺序

```
[00 darvin-api-extension]  ← 必须先做（不写 UI）
        │
        ├──────────────────────┐
        ▼                      ▼
[01 agent-output]  [02 tool-result]  [03 token-context]
        │              │              │
        └──────┬───────┘              ▼
               ▼                  [04 compaction-ui]
        [06 sidebar]                     │
        [08 i18n]                        ▼
                                    [07 settings]
[05 artifact-panel] ← 独立（仅依赖 00）
```

---

## 各 spec 核心 FR

### 00 · darvin-api-extension

> 协议层扩展，是后续 8 份的前置。

- [x] `DarvinMessage` 改为 discriminated union（5 种 type）
- [x] `DarvinToolKind` 枚举（bash / read / write / edit / todowrite / web_search / web_fetch / image_gen / video_gen / 兜底 string）
- [x] `DarvinUsage` 补 `cacheReadTokens` / `cacheWriteTokens`
- [x] 新增 `DarvinContextUsage` 类型（含 status 五态 + compactionCount + model）
- [x] `DarvinEvent` 新增 3 个 union 成员：`compaction` / `context_usage` / `artifact`
- [x] `DarvinAttachment` 类型（user 消息附件）
- [x] `client.ts` `LIFECYCLE_EVENT_TYPES` 移除 `'compaction'` 静默丢弃
- [x] `assertNever(msg)` 兜底编译检查
- [x] `mock-data.ts` 不报错（向后兼容）
- [x] 状态：**✅ 完成**

### 01 · agent-output-rendering

> 消息渲染升级：Markdown / Shiki / ThinkingBlock / turn 模型 / hover 元信息。

- [x] `MarkdownContent` 组件（markdown-it + Shiki + KaTeX + DOMPurify）
- [x] 代码块支持 10+ 种语言高亮
- [x] `ThinkingBlock` 流式自动展开 / 手动折叠 / 蓝色脉冲
- [x] `TurnMeta` hover 显示 4 操作（时间戳 / 模型 / 复制 / fork）
- [x] 大文档截断（>8KB 切头 4KB + 尾 8KB）
- [x] user 消息 `imageAttachments` 缩略图 chip
- [x] `useChatActions` 暴露 `copy()` / `regenerate()`
- [x] `npm run lint` + `npm run test` 通过
- [x] DevTools 手动验证 1 次长 prompt 流式无掉帧
- [x] 状态：**✅ 完成**

### 02 · tool-result-rendering

> 工具结果落地：`ToolCallGroup` + Bash/TodoWrite/Edit 专门渲染。

- [x] 6 个内置 kind 全部有专门渲染器（bash / read / write / edit / todowrite / web_search）
- [x] 默认折叠 + 用户展开 / 折叠状态记忆
- [x] 大文本（>4KB）截断预览 + KB/MB 大小摘要 + 「展开」按钮
- [x] 状态点 4 色（蓝脉冲 / 蓝实心 / 绿 / 红）
- [x] `getToolDisplayName` 归一化单测（Read/ReadFile → Read 等）
- [x] `useMessages` 接管 `tool_start` / `tool_end`，按 `toolUseId` 配对，单测覆盖
- [x] 错误展示：红色 + `tool.error.noDetail` 兜底
- [x] 状态：**✅ 完成**

### 03 · token-context-usage

> 单条消息 token + chat header 圆环可视化。

- [x] `TurnMeta` 显示 token 三元组（in / out / cache）
- [x] `ContextUsageIndicator` 圆环（28×28 SVG）
- [x] 5 态颜色（unknown / normal / warning / danger / compacting）
- [x] tooltip 显示百分比 + 数字 + 上下文窗口
- [x] 圆环可点（点击事件由 04 落地；本 spec 只占位回调）
- [x] `useMessages.contextUsageBySessionId` 单测覆盖
- [x] 状态：**✅ 完成**

### 04 · context-compaction-ui

> 手动压缩 + 自动压缩动画 + 压缩边界。

- [ ] 圆环点击触发 `window.darvin.compactContext(sessionId)` IPC
- [ ] compacting 状态圆环持续旋转动画
- [ ] 完成后显示 toast「上下文已压缩 XX → YY tokens」
- [ ] `ContextCompactionDivider` 在 turn 间渲染边界
- [ ] 失败时圆环变红 + toast「压缩失败，可重试」
- [ ] i18n 4 态文案齐（manual / auto / compacted / failed）
- [ ] 状态：**⬜ 未启动**

### 05 · artifact-panel

> 右侧面板重做：状态机 + 10 种渲染器 + iframe sandbox。

- [ ] 10 种 artifact 渲染器（html / svg / image / video / mermaid / code / markdown / text / document / local-service）
- [ ] inline HTML：`sandbox="allow-scripts"`（不加 allow-same-origin）
- [ ] file-based HTML：走 `createPreviewSession` 本地服务
- [ ] Mermaid `securityLevel: 'strict'`
- [ ] Code 渲染走 Shiki（与 01 复用）
- [ ] 面板宽度 180-1000px 拖拽
- [ ] artifact 与 session 绑定，切换 session 时 tab 隔离
- [ ] 状态：**⬜ 未启动**

### 06 · sidebar-upgrade

> 侧栏升级：树形 Agent / 拖拽改宽 / 多 tab 真实入口 / 快捷键。

- [ ] 侧栏宽度 220-420px 拖拽（CSS variable 驱动）
- [ ] 宽度持久化（localStorage）
- [ ] 5 nav tab 全部可点（即使内容是空态面板，不 warn）
- [ ] `Cmd+1-5` / `Ctrl+1-5` 快捷键生效（统一 `useShortcuts` 注册）
- [ ] 会话项 5 种 status（idle / running / completed / error / pinned）
- [ ] 折叠态：220px → 56px 紧凑模式
- [ ] `npm run lint` 通过
- [ ] 状态：**⬜ 未启动**

### 07 · settings-expansion

> 设置面板广度扩展：7 tab 拆分。

- [ ] 7 个 tab 全部有内容（不再是空态）
- [ ] 外观 tab：UI 字号 11-16 滑块 + 代码字号 8-24 滑块 + 主题色 3 选 1
- [ ] 模型 tab：至少 2 个 provider（Anthropic / OpenAI）
- [ ] 快捷键 tab：与 06 同步实际绑定
- [ ] 关于 tab：显示压缩次数 + 最近压缩时间
- [ ] tab 切换支持深链（`?tab=models`）
- [ ] 状态：**⬜ 未启动**

### 08 · i18n-enhancement

> i18n 增强：插值 / 响应式 / 补 key。

- [ ] `t(key, params)` 插值单测覆盖 3 种情况
- [ ] `setLang('en')` 触发已渲染组件 re-render（手动验证）
- [ ] 01-07 spec 涉及的 60+ 新 key 在 zh + en 双语中齐全
- [ ] AGENTS.md 散落 hardcoded 字符串全部走 `t()`
- [ ] 缺 key dev warn 生效（生产静默回退）
- [ ] `assertSameKeys(dictZh, dictEn)` 通过
- [ ] 状态：**⬜ 未启动**

---

## 状态变更日志

> 每次勾完一组 FR，在此处记一行：日期 / spec / 「完成说明」。

- 2026-08-01 · 00 darvin-api-extension · 协议层完成：`DarvinMessage` union 化（5 态）+ `DarvinToolKind` / `DarvinContextUsage` / `DarvinAttachment` + `DarvinEvent` 新增 compaction / context_usage / artifact + `client.ts` 移除 compaction 静默丢弃 + `assertNever` 兜底。lint / test 通过；⏳ 待人工 `npm start` 验证现有会话 / 历史消息渲染行为不变。
- 2026-08-01 · 01 agent-output-rendering · 8/9 落地：`MarkdownContent`（markdown-it 15 + Shiki v4 core 按需 19 语言 + KaTeX + DOMPurify）+ `CodeBlock`（Shiki 高亮 + 复制）+ `ThinkingBlock`（流式自动展开 / 蓝色脉冲 / 手动折叠）+ `TurnMeta` hover 4 操作 + turn 建模（`buildConversationTurns`）+ 大文档截断（>8KB 头 4KB + 尾 8KB）+ user 图片附件 chip + `useChatActions.copy/regenerate`。新依赖：markdown-it / markdown-it-task-lists / markdown-it-mark / @vscode/markdown-it-katex / katex / shiki / dompurify。lint / test（24 用例）/ vite build 通过。
- 2026-08-01 · 01 agent-output-rendering · **人工验证 + 修复 2 bug → ✅ 9/9**。playwright 驱动 Electron 实测：markdown 标题/加粗/列表/表格/KaTeX 内联+块级矩阵/任务清单/代码块 Shiki 高亮/复制按钮（剪贴板确认）/TurnMeta hover/ThinkingBlock 流式自动展开+蓝色脉冲/实时流式全部正常，console 0 错误。修复：(1) `TurnMeta.vue` 漏 import `IconButton`（非全局注册组件）→ hover 按钮全灭；(2) **流式消息被覆盖**——`useMessages` 7+ 调用点各建一个 immediate watch，组件在 active 已设后挂载即触发 `loadMessages` 覆盖正在流式的 bucket（debug 实证 `startAssistantMessage bucketLen=18` → 事件 `found=false listLen=17`），修复为模块级只建一次 watch。读 darvin-agent 源码确认 Go 侧事件时序正确，根因在渲染层。
- 2026-08-01 · 02 tool-result-rendering · ✅ 7/7 落地。协议层 §4.5：`DarvinEvent.tool_start` / `tool_end` 补 `toolUseId?: string` + `parseDarvinEvent` 从 Go 的 `message.id` 提升 + `tool_end` 的 output 从 `raw.output ?? raw.tool` 兜底（Go 把输出内容塞在 tool 字段）。渲染层：`ToolCallGroup`（默认折叠 / 状态记忆）+ `ToolCallHeader`（状态点 4 色：蓝脉冲 / 蓝实心 / 绿 / 红）+ `ToolCallInput`（bash `$` 命令行 / todowrite 三态 checkbox / edit 文件路径 / generic JSON）+ `ToolCallResult`（edit → DiffView 红绿 LCS diff；>4KB 截断预览 + KB/MB 大小摘要 + 展开；错误红色 + `tool.error.noDetail` 兜底）+ bash 仿终端（三色圆点 + 黑底）。`useMessages` 接管 `tool_start` / `tool_end` 按 `toolUseId` 配对 + `buildConversationTurns` 产出 `tool_group` item。纯函数 `toolDisplay.ts`（归一化 / 截断 / todo 解析 / diff）+ 新 icon `terminal.svg` + 9 个 i18n key（zh/en）。新测试：`client.test.ts`（parseDarvinEvent 6 例）/ `toolDisplay.test.ts`（19 例）/ `useMessages.test.ts`（tool 配对 8 例）。lint / test（57 用例）/ renderer vite build 通过。
- 2026-08-01 · 02 tool-result-rendering · **人工验证 + 修复 2 bug → ✅ 7/7**。playwright 驱动 Electron 实测：prompt「运行 bash pwd 和 ls -la」触发 2 个 Bash 工具组；「不存在的命令」触发错误工具组（红点 `bg-red-500` + 终端红字 `text-red-400`）；「find /usr/lib -type f」输出 3.9MB → 截断预览「输出过大 · 3.9 MB」+ 展开按钮 → 展开显示完整输出；新会话 write+edit 触发 Write/Edit/Read 三组，Edit 展开显示 DiffView 红删 `第一行` / 绿增 `修改后的第一行`。状态点绿/红/运行中（`Bash 运行中…`）均正确，折叠/展开状态记忆正常，console 0 错误。修复：(1) **工具组渲染顺序错位**——`startAssistantMessage` 把 assistant 消息建在 bucket 最前（thinking 与 content 同属一条消息），tool 条目随后 append 到尾部，导致工具组和 TurnMeta 排在答案下方；修复为 `AssistantTurnBlock` 三阶段渲染：thinking 段 → 工具组 → content 段 + TurnMeta（实测顺序：思考中 → Bash×2 → 答案 → TurnMeta）；(2) **`inferToolEndError` 识别不到字符串错误**——Go 的 `ToolEndEvent.Result.IsError` 存在但 `mapEventToTS` 没序列化（spec 规定不改 Go），白名单拒绝输出 `tool "shell": argument "command" must be one of [...]` 被误判成功（绿点）；扩展 TOOL_ERROR_PATTERNS（`command not allowed` / `must be one of` / `command not found` / `no such file or directory` / `permission denied` / `not found` / 行首 `error:` / `failed:`），单测覆盖 5 种真实文案 + 普通输出不误报。TodoWrite 渲染器当前 agent 无此工具无法 live 触发，由单测覆盖。lint / test（57 用例）通过。
- 2026-08-01 · 03 token-context-usage · ✅ 6/6 落地。新建 `services/tokenFormat.ts`（`formatTokenCount` 1k/M 短标签 + `deriveContextStatus` 5 态：显式 status 优先，unknown 时按 percent 阈值 normal<60% / warning 60-85% / danger>85%）。`useMessages`：`Message` 加 `usage` + done 事件写入 + 历史 `toMessage` 保留；新增 `contextUsageBySessionId` ref 消费 `context_usage` 事件（key 用 `usage.sessionId` 兜底 event.sessionId），`removeSession` / `reset` 清理；`context_usage` 不触发 unread 红点（纯用量快照）。`TurnMeta` hover 增 token 三元组行（`1.2k in · 0.3k out · 0.5k cache`）。新建 `ContextUsageIndicator.vue`：28×28 SVG 圆环（radius 7 / dasharray 百分比）+ 5 态颜色 + compacting `animate-spin` + tooltip（百分比/已用/上下文窗口/接近上限提示）+ 点击 emit compact 占位（04 落 IPC）。`ChatHeader` 挂圆环（activeSessionId）+ 占位 `handleCompact`。i18n 新增 `context.usage.*` 5 key（zh/en）。新测试：tokenFormat.test.ts（9 例）/ useMessages.test.ts context 块（7 例）。lint / test（73 用例）/ renderer vite build 通过。
- 2026-08-01 · 03 token-context-usage · **人工 live 验证 → ✅ 6/6（无渲染层 bug）**。playwright 驱动 Electron：发 `ping` 触发真实 done 事件带 `usage {inputTokens:1160, outputTokens:17}` → TurnMeta 渲染 `1.2k in · 17 out`，hover 容器 opacity=1。圆环 5 态用**合成 `context_usage` 注入**验证（Go 不推该事件）：normal 30% → `text-text-muted` 灰 / warning 78% → `text-warning` 橙 + tooltip「接近上限，可手动压缩」/ danger 95% → `text-danger` 红 / compacting 100% → `text-accent` + `animate-spin` + `cursor-default` +「正在压缩上下文…」/ 无数据整颗不渲染。tooltip hover `display:block`（`78% 已用 78k / 上下文 100k 接近上限，可手动压缩`）；点击 emit compact 走 `handleCompact` 占位 no-op，console 0 错误。**2 个后端 gap（非渲染 bug）**：(1) Go 完全不推 `context_usage` 事件 → 真实会话圆环不会出现，等 Go 补事件或 04 接手动数据源；(2) Go `done` usage 只序列化 input/output/total、无 cacheReadTokens，且历史消息不持久化 usage → token 三元组只有 live 流式完成的才显示、cache 段永不显示。
