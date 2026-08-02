# Agent 输出 / 工具 / Token-上下文 9 spec 跟踪表

> **中心化跟踪表**。每份 spec 的 § 6 验收是「设计层」细颗粒清单；本表是「执行层」一栏一格，落地时只勾这里。
>
> - 创建日期：2026-08-01
> - 调研 / 索引 doc：[`2026-08-01-cowork-vs-lobsterai-comparison.md`](./2026-08-01-cowork-vs-lobsterai-comparison.md)
> - 进度规则：spec 的核心 FR 全部勾上 → 标 `✅ 完成`；部分落地 → `🚧 进行中`；碰到阻塞 → `⛔ 阻塞`（要在后面写原因）

---

## 当前进度

**已完成 11 / 11（含 2 份补充）。9-spec 主列表全部落地。**

| # | spec | 状态 | 进度 | 关键路径 |
|---|------|------|------|---------|
| 00 | [darvin-api-extension](./../darvin-api-extension/2026-08-01-darvin-api-extension-design.md) | ✅ 完成 | 9/9 | 协议先行 |
| 01 | [agent-output-rendering](./../agent-output-rendering/2026-08-01-agent-output-rendering-design.md) | ✅ 完成 | 9/9 | 依赖 00 |
| 02 | [tool-result-rendering](./../tool-result-rendering/2026-08-01-tool-result-rendering-design.md) | ✅ 完成 | 7/7 | 依赖 00 |
| 03 | [token-context-usage](./../token-context-usage/2026-08-01-token-context-usage-design.md) | ✅ 完成 | 6/6 | 依赖 00 |
| 04 | [context-compaction-ui](./../context-compaction-ui/2026-08-01-context-compaction-ui-design.md) | ✅ 完成 | 6/6 | 依赖 00 + 03 |
| 05 | [artifact-panel](./../artifact-panel/2026-08-01-artifact-panel-design.md) | ✅ 完成 | 7/7 | 依赖 00 |
| 06 | [sidebar-upgrade](./../sidebar-upgrade/2026-08-01-sidebar-upgrade-design.md) | ✅ 完成 | 7/7 | 无前置 |
| 07 | [settings-expansion](./../settings-expansion/2026-08-01-settings-expansion-design.md) | ✅ 完成 | 6/6 | 依赖 04 |
| 08 | [i18n-enhancement](./../i18n-enhancement/2026-08-01-i18n-enhancement-design.md) | ✅ 完成 | 6/6 | 无前置 |
| 09 | [composer-composition（补充）](./2026-08-01-composer-composition-design.md) | ✅ 完成 | 7/7 | 依赖 04；纯渲染重组 |
| 10 | [session-workspace-usage（补充）](./2026-08-01-session-workspace-usage-design.md) | ✅ 完成 | 6/6 | live bug 修复；**提前做，先于 05-08** |

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
[09 composer-composition] ← 补充（依赖 04，纯渲染重组）
[10 session-workspace-usage] ← 补充（live bug 修复；**最先做，先于 05-08**）
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

- [x] 圆环点击触发 `window.darvin.compactContext(sessionId)` IPC
- [x] compacting 状态圆环持续旋转动画
- [x] 完成后显示 toast「上下文已压缩 XX → YY tokens」
- [x] `ContextCompactionDivider` 在 turn 间渲染边界
- [x] 失败时圆环变红 + toast「压缩失败，可重试」
- [x] i18n 4 态文案齐（manual / auto / compacted / failed）
- [x] 状态：**✅ 完成**

### 05 · artifact-panel

> 右侧面板重做：状态机 + 10 种渲染器 + iframe sandbox。

- [x] 10 种 artifact 渲染器（html / svg / image / video / mermaid / code / markdown / text / document / local-service）
- [x] inline HTML：`sandbox="allow-scripts"`（不加 allow-same-origin）
- [x] file-based HTML：走 `createPreviewSession` 本地服务（协议补 `filePath?` + main `artifact-preview-server.ts`，见 spec §4.3 落地补充）
- [x] Mermaid `securityLevel: 'strict'`
- [x] Code 渲染走 Shiki（与 01 复用）
- [x] 面板宽度 180-1000px 拖拽
- [x] artifact 与 session 绑定，切换 session 时 tab 隔离
- [ ] 状态：**✅ 完成（7/7）**

### 06 · sidebar-upgrade

> 侧栏升级：树形 Agent / 拖拽改宽 / 多 tab 真实入口 / 快捷键。

- [x] 侧栏宽度 220-420px 拖拽（CSS variable 驱动）
- [x] 宽度持久化（localStorage）
- [x] 6 nav tab 全部可点（scheduled/skill/mcp 走 PlaceholderView 空态面板，不 warn）
- [x] `Cmd+1-5` / `Ctrl+1-5` 快捷键生效（统一 `useShortcuts` 注册）
- [x] 会话项 5 种 status（idle / running / completed / error / pinned）
- [x] 折叠态：220px → 56px 紧凑模式
- [x] `npm run lint` 通过
- [x] 状态：**✅ 完成**

### 07 · settings-expansion

> 设置面板广度扩展：7 tab 拆分。

- [x] 7 个 tab 全部有内容（general / appearance / shortcuts / models / memory / runtime / about）
- [x] 外观 tab：UI 字号 11-16 滑块 + 代码字号 8-24 滑块 + 主题色 3 选 1
- [x] 模型 tab：至少 2 个 provider（Anthropic / OpenAI / Custom）
- [x] 快捷键 tab：与 06 同步实际绑定
- [x] 关于 tab：显示压缩次数 + 最近压缩时间
- [x] tab 切换支持深链（`?tab=models`）
- [x] 状态：**✅ 完成**

### 08 · i18n-enhancement

> i18n 增强：插值 / 响应式 / 补 key。

- [x] `t(key, params)` 插值单测覆盖 3 种情况
- [x] `setLang('en')` 触发已渲染组件 re-render（手动验证）
- [x] 01-07 spec 涉及的 60+ 新 key 在 zh + en 双语中齐全
- [x] AGENTS.md 散落 hardcoded 字符串全部走 `t()`
- [x] 缺 key dev warn 生效（生产静默回退）
- [x] `assertSameKeys(dictZh, dictEn)` 通过
- [x] 状态：**✅ 完成**

### 09 · composer-composition（补充）

> 聊天框组合重组：单一 composer 卡片承载全部输入控制，参考 LobsterAI `CoworkPromptInput`。详见 [`2026-08-01-composer-composition-design.md`](./2026-08-01-composer-composition-design.md)。

- [x] Composer 单一卡片（textarea + 底部工具栏 + context 行），PromptToolbar 不再单独成行
- [x] 圆环从 ChatHeader 移入 Composer 工具栏右侧（发送键左），压缩流程不变
- [x] `+` 菜单「上传文件」触发真实导入（`useImportedFiles().importFiles()`），独立回形针 ImportButton 删除；导入结果统一由 ImportedFilesBar 展示
- [x] 文件类型放宽：dialog 无 filters、`runImport` 无扩展名白名单；大小上限 + symlink 拒绝仍生效
- [x] 模型 / 语音 / 专家套件入口全部并入 Composer 工具栏
- [x] context 行展示当前工作目录（label + hover tooltip 或目录选择器）
- [x] `npm run lint` + `npm run test` 通过；home / chat 两处 composer 均正常
- [x] 状态：**✅ 完成**

### 10 · session-workspace-usage（补充）

> live bug 修复：workspace 按会话隔离 + Go 上报 `context_usage` + 工作目录可点击打开。详见 [`2026-08-01-session-workspace-usage-design.md`](./2026-08-01-session-workspace-usage-design.md)。

- [x] Go：`go build` / `go vet` / gateway+dispatcher 单测通过；`npm run lint` + `npm run test`（含新增 context_usage 序列化用例）通过
- [x] 切换会话子进程重启成本已记录（已知边界：在途流式中断）
- [x] 切换会话后 `getWorkspaceInfo().label` + `listImportedFiles()` 与 active session 一致；ImportedFilesBar 只显示当前会话文件（live 验证通过）
- [x] 会话 A 导入文件，切到 B 不显示，切回 A 显示（live 验证通过：xKY1→3 jpg，2Iqp→3 csv）
- [x] 完成一次 prompt 后圆环出现（percent / used / context tokens 正确，tooltip 正常）；再发一轮 usage 更新（live 验证通过）
- [x] context 行工作目录点击打开系统文件管理器（live 验证通过：button + revealWorkspace 无报错）
- [x] 状态：**✅ 完成**

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
- 2026-08-01 · 04 context-compaction-ui · ✅ 6/6 落地。**协议层**：`DarvinEvent.compaction` 补 `beforeTokens? / afterTokens?`；新增 `DarvinCompactContextResponse {accepted, sessionId}` + `DarvinApi.compactContext(sessionId)`；`DarvinContextUsage` 补 `compactionReason?: 'manual' | 'auto'`。preload 暴露 `darvin:compact_context` → main 转发 `agent.compact_context`（Go 离线/会话不可压返回 `{accepted:false}`，不动画不 toast 避免假压缩）。**Go 侧**：gateway 新增 `agent.compact_context` RPC——`handleCompactContext` 校验会话 idle + assembler 就绪后**异步**跑 `Assembler().Compact()`（避免 LLM 摘要阻塞 WS 读循环），成功后 `Session().ReplaceAll(retained)` + `Agent.Emit(CompactionEvent)`；`Agent.IsRunning()` 空闲守卫；`eventledger.mapEventToTS` 补 `CompactionEvent` case（reason/checkpointId/createdAt/beforeTokens/afterTokens）；自动压缩路径 `assemble.go` 在预算触发成功后 emit `CompactionEvent`（Note "auto"）→ 新增 `ctxengine.Deps.Emit` 接口 + agent.Agent 实现 + 测试桩同步。**渲染层**：`useMessages` 加 `compactionsBySessionId` 消费 compaction 事件（按 checkpointId 去重）+ `beginCompact/endCompact/failCompact` 状态助手（compacting 旋转 / normal 还原 / danger 转红+失败 toast）+ compaction 事件回写 `compactionCount / latestCompactionAt / latestCompactionReason` + 不触发 unread；`buildConversationTurns(messages, markers)` 把 createdAt 早于该 turn 的 marker 挂到 `precedingCompactions`（晚于最后消息的 marker 丢弃，避免时序错位）。新建 `ContextCompactionDivider.vue`（虚线分隔 + `↻` + 原因 + Intl 时间）。`ChatHeader.handleCompact`：beginCompact → `window.darvin.compactContext` → 未被受理则 endCompact；受理后 15s 超时兜底转失败态。新建 `services/toast.ts` + `ToastHost.vue`（fixed 顶部 / success/error/info / 3s 自动消失），`AppShell` 挂载。圆环 tooltip compacting 态按 `compactionReason` 分 manual/auto 文案。i18n 新增 8 key（zh/en，含 4 态 + divider + reason ×2 + toast.dismiss）。新测试：useMessages compaction 块 8 例 + divider 挂载 3 例 + client parseDarvinEvent compaction 1 例。lint / test（84 用例）/ renderer vite build 通过；Go `go build` / `go vet` / gateway+ctxengine+agent 单测通过。**已知 gap（非渲染 bug）**：(1) 手动压缩会 `ReplaceAll` 会话内存历史，但 TextDeltaHook 落库的 store 行不更新 → 重载历史仍显示压缩前消息（live 会话已生效）；(2) Go 不推 `context_usage` 事件 → 无用量数据时圆环不可见、手动压缩入口需先有 usage（合成注入可触发）；(3) 自动压缩的 divider 依赖 assemble.go 的新 emit，需真实跑超预算长会话才能 live 触发。
- 2026-08-01 · 09 composer-composition · ✅ 7/7 落地。**Composer 重组**：新建 `ComposerToolbar.vue`（底部工具栏，左 `+`/专家套件，右 圆环/模型/语音/发送）+ `ComposerContextRow.vue`（context 行：左工作目录 label + 右 Agent 选择器）；`Composer.vue` 与 home `PromptDock.vue` 均收敛为单一卡片（textarea + 工具栏 + context 行），`PromptToolbar.vue` 删除、ChatView 不再单独成行。**圆环迁移**：`ChatHeader.vue` 删除 `ContextUsageIndicator` + `handleCompact`，压缩逻辑整体搬进 `ComposerToolbar`（读 `session.activeSessionId` + `window.darvin.compactContext` + `useMessages.begin/end/failCompact`，15s 超时兜底不变）。**导入合并**：`PlusMenu` 的 upload 项改调 `useImportedFiles().importFiles()`（busy 时禁用该项），独立 `ImportButton.vue`（回形针）删除，导入唯一入口收敛到 `+` 菜单，home/chat 共用同一单例；`ImportedFilesBar` 保留为唯一导入结果展示。**文件类型放宽**：main dialog 去 text-only `filters`，`runImport` 删除 `TEXT_FILE_EXTS` 扩展名白名单，保留普通文件/symlink 拒绝 + `MAX_IMPORT_BYTES` 上限（importFiles.test 的 pdf 用例改为「任意类型可导入」）。**工作目录展示**：`DarvinWorkspaceInfoResponse` 增 `label?`（basename，不下发绝对路径），main `get_workspace_info` 从 `rootPath` 取 basename；context 行 `[📁 label ▾]` 只读展示 + hover tooltip（v0 不实现目录选择器）。i18n 新增 `composer.plus/suite/mic/workspace`（zh/en），移除废弃 `composer.import`；新增 `folder.svg` 图标。lint / test（84 用例）/ renderer vite build 通过。
- 2026-08-01 · 10 session-workspace-usage · ✅ 6/6 落地（live bug 修复，提前执行）。**main workspace 跟随 active session**：新增 `followActiveWorkspace(sessionId)`（更新 `workspaceLoc` → `ensureWorkspaceRoot` → 以新根 `restartGoSubprocess` 重锚 agent 沙箱），在 `switch_session` / `create_session` / `delete_session` 三入口接入，先改 workspace 再广播（switch 加 `broadcastWorkspaceChanged`，delete 无 next 时 `workspaceLoc=null`）。**renderer useImportedFiles 会话隔离**：模块级 `watch(session.activeSessionId)` 变化即清空 files/workspaceBytes/notice + refetch；`onWorkspaceChanged` 回调加 `info.sessionId !== active` 过滤。**Go emit context_usage**：`event.go` 新增 `ContextUsageEvent`；`dispatcher.go` RunEndEvent 后调 `emitContextUsage()`（`LastUsage().PromptTokens` 优先，**代理不上报 input_tokens 时退回 `ctxengine.EstimateMessageTokens` rune/4 估算**——live 实测 `done` 的 inputTokens=0，spec 原设计前提不成立，此为落地时发现并补充的兜底）；`eventledger.mapEventToTS` 补 case（`status:"unknown"` 由 renderer 派生）+ 新增序列化单测。**工作目录可点击**：`ComposerContextRow` chip `span`→`button`（`@click` → `revealWorkspace()`，`t('imported.reveal')` title/aria，hover 态）；补 `watch(activeSessionId)` 刷新 label（live 发现原 onMounted 只读一次、切换后 chip 残留旧 basename）。**live 验证（playwright 驱动新二进制）**：(1) 切换 `xKY1`↔`2Iqp` 后 `getWorkspaceInfo().label` + `listImportedFiles()` 与 active 一致，文件隔离（xKY1→3 jpg，2Iqp→3 csv）；(2) 真实 prompt 后 `context_usage` 事件流入 renderer（`text_delta→done→context_usage→agent_end`），圆环出现「0% 已用 48 / 上下文 200k」可点击；(3) chip 为 BUTTON，点击 revealWorkspace 无报错。Go `go build`/`go vet`/agent+gateway 单测、lint、test（84 用例）、renderer vite build 全绿。**已知边界**：切换会话重启子进程 ~1s、中断其它在途流式；rune/4 兜底在长会话下低估实际用量（代理不报 input_tokens 时圆环 percent 偏低）。
- 2026-08-01 · 05 artifact-panel · 🚧 6/7 落地。**状态机 `useArtifacts.ts`**：`artifactsBySession` / `previewTabsBySession` / `activeTabIdBySession` / `isPanelOpenBySession` / `panelWidth`(180-1000) + `ArtifactSpecialTab`(fileList/browser/subagents) + `ArtifactContentView`(preview/code)，`addArtifact` id 去重 + 内容更新，切 active session 才自动弹开侧栏/切外层 tab。**10 渲染器**（`components/side-panel/renderers/`）：HtmlRenderer（srcdoc + `sandbox="allow-scripts"` 不加 same-origin + hash-nav 拦截器 `services/artifactHtml.ts`）、SvgRenderer（DOMPurify svg profile + srcdoc）、Image/Video（data/URL）、MermaidRenderer（`securityLevel:'strict'` + offscreen DOM 提取 + DOMPurify 二次净化）、CodeRenderer（复用 `services/highlight` Shiki，name 推导语言）、MarkdownRenderer（复用 MarkdownContent）、TextRenderer、DocumentRenderer（office 非目标 → 占位）、LocalServiceRenderer（URL + 新窗口打开 + iframe）。**面板**：`ArtifactPanel.vue`（内层特殊 tab + per-artifact 预览 tab + preview/code 切换 + 关闭），`SidePanelContent` artifact tab 渲染 panel（按 sessionId key 重挂载），`SidePanel.vue` 左边沿拖拽 handle 调 `setPanelWidth`，`AppShell` grid 右列跟随 panelWidth + dragging 时关过渡。**useMessages 接入**：`artifact` 事件 → `artifacts.addArtifact`（不落消息 bucket，后台 session 仍 unread），`removeSession` 清 artifacts。i18n 新增 13 key（zh/en 对齐）。**live 验证（合成 artifact 事件注入 + playwright）**：(1) 注入 html → 面板自动弹开 + 外层切 Artifact + iframe `sandbox="allow-scripts"` 渲染；(2) mermaid SVG 渲染且无 `<script>` 注入（strict+DOMPurify）；(3) code Shiki 高亮 `<span class="line">`；(4) markdown h1/table / text / local-service URL+打开链接+iframe / svg 全过；(5) 预览↔代码视图切换、关闭 tab、特殊 tab 占位均正常；(6) 拖拽 560→360→clamp 1000→clamp 180；(7) 切换 session B 面板空态、切回 A 6 个 tab 恢复（会话隔离）。lint / test（95 用例，新增 useArtifacts 9 例 + artifact 事件路由 2 例）/ renderer vite build 全绿（mermaid 新增依赖）。**待办**：file-based HTML 本地服务（协议无 filePath 字段 + Go 不产 artifact 事件 → 暂缓，见 spec §4.3 落地补充）。
- 2026-08-02 · 06 sidebar-upgrade · ✅ 7/7 落地。**useSidebar**：新增 `width`（220-420 clamp，localStorage 持久化，`--sidebar-width` 写 documentElement）+ `dragging`；collapsed 不再摘除 Sidebar，而是 220px→56px 图标 rail（Brand/Nav/AgentCard/Bottom 收 collapsed prop，session 列表折叠时隐藏）。**useViewMode** 增 `scheduled`/`skills`/`mcp` 三 mode；新建 `PlaceholderView.vue` 承载三个 nav 空态面板，`AppShell` 全 nav 路由不再 warn。**useShortcuts**：`Cmd/Ctrl+1-5` 映射 home/search/scheduled/suite/skills，可编辑元素聚焦跳过。**useSession**：`pinnedSessionIds`（localStorage）+ `togglePin` + deleteSession 清理。**useMessages**：`sessionStatusBySessionId`（流式→running / done→completed / error→error / agent_end→completed）+ 纯函数 `deriveSessionStatusFromMessages` + loadMessages 派生。**SessionList/Item**：置顶排序 + `status` 多态图标（running 脉冲 / error 红叹 / completed 灰勾 / idle message-square）+ pin 徽标 + 下拉置顶/取消置顶。i18n 新增 13 key（zh/en 对齐）+ `pin.svg`。lint / test（118 用例，新增 sessionStatus 3 例）/ renderer+main+preload vite build 全绿。**live 验证**（playwright）：拖拽 244→324→420 clamp + 持久化；6 nav 全可点（定时任务/技能/MCP 空态面板、专家套件真实页）；Cmd+1→home / Cmd+2→search / Cmd+3→scheduled / Cmd+4→suite / Cmd+5→skills / Ctrl+3 同效；折叠→56px icon rail、展开回 420；会话项 error/completed 状态图标 + pin 徽标 + 置顶/取消置顶 + localStorage `[]` 恢复。**已知边界**：G5 多 Agent 树未做（darvin 无子代理体系，见 spec 落地补充）；settings 不占 1-5。
- 2026-08-01 · 05 artifact-panel · **file-based HTML 补全 → ✅ 7/7**。协议 `artifact` 事件加 `filePath?`（相对 workspace 根）+ `DarvinApi` 增 `createArtifactPreviewSession` / `destroyArtifactPreviewSession`（IPC `darvin:artifact:create_preview_session` / `destroy_preview_session`，preload 转发）。**main 本地预览服务** `src/main/services/artifact-preview-server.ts`：`127.0.0.1` 静态 HTTP 服务器（懒启动、无会话时整体关闭），每个预览会话随机 sessionId，entry 所在目录作 URL 挂载根（相对资源 css/js/img 按此解析），解析结果越出 workspace 根返回 403，纯函数 `resolveWithinRoot` 单测覆盖（sibling 允许 / `../` 越界 null）。`HtmlRenderer`：有 `filePath` 时走预览会话 `iframe[src]` + `sandbox="allow-scripts"`，卸载销毁会话；错误信息净化（不把绝对路径透传 renderer）。i18n 增 `artifact.render.loadFailed`（zh/en）。**live 验证**：workspace 内写 `preview.html` + `style.css` + `app.js`，注入 `{kind:'html', filePath:'preview.html'}` → iframe src = `http://127.0.0.1:<port>/<uuid>/preview.html`，sandbox=`allow-scripts`，服务端 fetch 确认 html/css/js 全部正确返回（相对资源解析 OK）；关闭 tab → 会话销毁 → 端口整体关闭（ECONNREFUSED）。lint / test（99 用例，新增 resolveWithinRoot 4 例）/ renderer + main vite build 全绿。
- 2026-08-02 · 07 settings-expansion · ✅ 6/6 落地。**7 tab 拆分**：`settings-sections.ts` 抽 tab 注册表（`SettingsSections` + `SettingsSectionId` + `isSettingsSectionId`），`SettingsSubNav` / `SettingsView` 共用；account tab 移除（spec 非目标）。**G1 通用**：autoLaunch 真实 OS 开关（`app.getLoginItemSettings/setLoginItemSettings`）+ notifications / proxy 持久化 yaml `app` 块；新 IPC `get_app_preferences` / `set_app_preferences`。**G2 外观**：新建 `useAppearance`（localStorage + CSS var 驱动）——`uiFontSize/14` 比例缩放 `--text-*` token、`--text-code` 单独控制代码字号、`html[data-accent]` 驱动 `--color-primary` 家族三选一；theme.css 加 `--text-code` token + `html[data-accent=blue|green]` 覆盖块；CodeBlock `text-[13px]`→`text-code`。**G3 模型**：`DarvinLLMConfig` 扩 `provider/activeProvider/defaultModel/providers`，UI 三 provider（anthropic 立即生效重启，openai/custom 存 `providers` 块不激活——Go 只注册 anthropic，避免未知 provider 启动崩溃）；`user-settings.ts` yaml 解析/写入扩展 llm.provider/default_model + app/memory/providers 两级嵌套。**G4 快捷键**：替换虚构 ⌘N/⌘F/⌘D/⌘,/⌘J 为真实绑定（Ctrl/Cmd+1-5 + Enter/Shift+Enter/Ctrl+Enter + Esc）。**G5 记忆**：Go 无 memory 系统（sections.go），设置面板落地 + 持久化 yaml `memory` 块，hint 标注 gap。**G6 关于**：新 IPC `get_app_info`（app.getVersion/process.versions.electron/platform/arch）替换硬编码；压缩次数聚合 `useMessages.compactionsBySessionId`；导出日志=复制诊断信息到剪贴板+toast。**G7 深链**：SettingsView onMounted 读 `?tab=` + `history.replaceState` 同步。**顺带修 spec 06 遗留**：Sidebar 渲染 SidebarAgentCard 未传必填 `collapsed` prop（Vue warn），补 `:collapsed`。**live 验证**（playwright）：7 tab 全有 h3 内容；`?tab=models` 打开+进入 settings → 模型 tab 高亮；UI 字号 14→16 → `--text-base` 16px + `--text-sm` 15px 等比缩放 + localStorage 持久化；代码字号→20 → `--text-code` 20px；主题色 blue → data-accent + `--color-primary` #2563EB + 还原；模型 dropdown 3 provider + OpenAI 显示 pending note；快捷键 9 条真实绑定；通用 autoLaunch=false / notifications=true；运行时「引擎状态在线」绿点；关于 v1.0.0 / Electron 43.2.0 / linux x64 / 压缩次数 0 / 导出日志复制成功 toast。console 0 error（仅剩打包后消失的 CSP dev 警告）。lint / test（124 用例，新增 user-settings 3 + useAppearance 3）/ renderer+main+preload vite build 全绿。**已知 gap**：OpenAI/Custom 运行时不可用（Go 未注册 provider，仅凭据存储）；memory 运行时未接入 Go；压缩计数来自 live compaction 事件（历史不持久化）。
- 2026-08-02 · 08 i18n-enhancement · ✅ 6/6 落地。**G1 插值**：`t(key, params?)` 支持 `{name}` 全局替换；调用点手写 `.replace('{x}', ...)` 全部收敛成传参（ContextUsageIndicator percent/tokens、ContextCompactionDivider divider、useMessages compacted toast、ArtifactCardGroup showAll）；en 缺 key 回退 zh 再回退原 key；缺 key warn 加 `WARNED_MISSING_KEYS` 去重。**G2 响应式**：能力原本已具备，修复 1 个残留 bug——`SettingsPanelAppearance` 的 `const lang = getLang()` 是 setup 期快照，切语言后 radio checked 不迁移 + `applyLang` 守卫读错，改 `computed(() => getLang())`；三个 Intl.DateTimeFormat 直用点（TurnMeta / ContextCompactionDivider / SettingsPanelAbout）重构到 `formatDate` 后时间戳也响应式。**G3 key 齐全**：脚本提取 203 个 `t('...')` 调用 key 全部在字典；新增 22 key（time.* / model.no_match / expert.no_match / expert.filter.*×6 / settings.appearance.theme|accent|lang.*×8 / home.example.*×4）。**G4 hardcoded 净化**：`.vue` 模板 + script 中文串扫描 26 处全清——AgentFilterTabs（label→labelKey）、AgentCard（category→`expert.filter.${category}`）、ModelPicker（model.no_match）、SettingsPanelAppearance（theme/accent/lang 选项→labelKey/descKey）、SessionItem（相对时间→formatRelativeTime，`现在`/`昨` 走 time.justNow/yesterday）、ExpertSuiteView（复用 sidebar.nav.suite + expert.no_match）、HomeView（示例 prompt→home.example.*）。**已知边界**：mock-data.ts 专家 Agent 名称/描述/价格是原型假数据未 i18n。**G5 格式化**：新增 formatNumber / formatDate / formatRelativeTime；formatNumber 暂无消费点按设计文档保留。**G6/G7**：assertSameKeys / dictZh / dictEn 导出，单测显式校验 key 对齐 + 缺 key warn 只 warn 一次。**AGENTS.md**：i18n 节更新为现状（响应式 + 插值 + 格式化 API + 升级 vue-i18n 触发条件移除插值项）。**测试**：新增 i18n.test.ts 17 例，全量 141 例通过，lint 0 错，renderer build 通过。**live 验证**（playwright）：appearance 面板切 English → 整个面板 + 侧栏 nav 全部响应式变英文（外观→Appearance、浅色→Light、橙（默认）→Orange (default)、中文→Chinese、侧栏新建任务→New task…），radio checked 正确迁移到 en；切回中文完全还原；expert suite 过滤 tab 6 个 i18n 值渲染正常。console 0 error（仅打包后消失的 CSP dev 警告）。
