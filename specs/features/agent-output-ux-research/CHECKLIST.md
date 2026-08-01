# Agent 输出 / 工具 / Token-上下文 9 spec 跟踪表

> **中心化跟踪表**。每份 spec 的 § 6 验收是「设计层」细颗粒清单；本表是「执行层」一栏一格，落地时只勾这里。
>
> - 创建日期：2026-08-01
> - 调研 / 索引 doc：[`2026-08-01-cowork-vs-lobsterai-comparison.md`](./2026-08-01-cowork-vs-lobsterai-comparison.md)
> - 进度规则：spec 的核心 FR 全部勾上 → 标 `✅ 完成`；部分落地 → `🚧 进行中`；碰到阻塞 → `⛔ 阻塞`（要在后面写原因）

---

## 当前进度

**已完成 0 / 9。下一个该做的：00（darvin-api-extension）**（前置协议，必须先动）。

| # | spec | 状态 | 进度 | 关键路径 |
|---|------|------|------|---------|
| 00 | [darvin-api-extension](./../darvin-api-extension/2026-08-01-darvin-api-extension-design.md) | ⏳ 待启动 | 0/5 | 协议先行 |
| 01 | [agent-output-rendering](./../agent-output-rendering/2026-08-01-agent-output-rendering-design.md) | ⬜ 未启动 | 0/8 | 依赖 00 |
| 02 | [tool-result-rendering](./../tool-result-rendering/2026-08-01-tool-result-rendering-design.md) | ⬜ 未启动 | 0/7 | 依赖 00 |
| 03 | [token-context-usage](./../token-context-usage/2026-08-01-token-context-usage-design.md) | ⬜ 未启动 | 0/6 | 依赖 00 |
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

- [ ] `DarvinMessage` 改为 discriminated union（5 种 type）
- [ ] `DarvinToolKind` 枚举（bash / read / write / edit / todowrite / web_search / web_fetch / image_gen / video_gen / 兜底 string）
- [ ] `DarvinUsage` 补 `cacheReadTokens` / `cacheWriteTokens`
- [ ] 新增 `DarvinContextUsage` 类型（含 status 五态 + compactionCount + model）
- [ ] `DarvinEvent` 新增 3 个 union 成员：`compaction` / `context_usage` / `artifact`
- [ ] `DarvinAttachment` 类型（user 消息附件）
- [ ] `client.ts:245` `LIFECYCLE_EVENT_TYPES` 移除 `'compaction'` 静默丢弃
- [ ] `assertNever(msg)` 兜底编译检查
- [ ] `mock-data.ts` 不报错（向后兼容）
- [ ] 状态：**⏳ 待启动**

### 01 · agent-output-rendering

> 消息渲染升级：Markdown / Shiki / ThinkingBlock / turn 模型 / hover 元信息。

- [ ] `MarkdownContent` 组件（markdown-it + Shiki + KaTeX + DOMPurify）
- [ ] 代码块支持 10+ 种语言高亮
- [ ] `ThinkingBlock` 流式自动展开 / 手动折叠 / 蓝色脉冲
- [ ] `TurnMeta` hover 显示 4 操作（时间戳 / 模型 / 复制 / fork）
- [ ] 大文档截断（>8KB 切头 4KB + 尾 8KB）
- [ ] user 消息 `imageAttachments` 缩略图 chip
- [ ] `useChatActions` 暴露 `copy()` / `regenerate()`
- [ ] `npm run lint` + `npm run test` 通过
- [ ] DevTools 手动验证 1 次长 prompt 流式无掉帧
- [ ] 状态：**⬜ 未启动**

### 02 · tool-result-rendering

> 工具结果落地：`ToolCallGroup` + Bash/TodoWrite/Edit 专门渲染。

- [ ] 6 个内置 kind 全部有专门渲染器（bash / read / write / edit / todowrite / web_search）
- [ ] 默认折叠 + 用户展开 / 折叠状态记忆
- [ ] 大文本（>4KB）截断预览 + KB/MB 大小摘要 + 「展开」按钮
- [ ] 状态点 4 色（蓝脉冲 / 蓝实心 / 绿 / 红）
- [ ] `getToolDisplayName` 归一化单测（Read/ReadFile → Read 等）
- [ ] `useMessages` 接管 `tool_start` / `tool_end`，按 `toolUseId` 配对，单测覆盖
- [ ] 错误展示：红色 + `tool.error.noDetail` 兜底
- [ ] 状态：**⬜ 未启动**

### 03 · token-context-usage

> 单条消息 token + chat header 圆环可视化。

- [ ] `TurnMeta` 显示 token 三元组（in / out / cache）
- [ ] `ContextUsageIndicator` 圆环（28×28 SVG）
- [ ] 5 态颜色（unknown / normal / warning / danger / compacting）
- [ ] tooltip 显示百分比 + 数字 + 上下文窗口
- [ ] 圆环可点（点击事件由 04 落地；本 spec 只占位回调）
- [ ] `useMessages.contextUsageBySessionId` 单测覆盖
- [ ] 状态：**⬜ 未启动**

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

（暂无记录）
