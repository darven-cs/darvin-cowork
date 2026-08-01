# Agent 输出渲染重做

> 编号 **01**，覆盖 assistant / user / thinking 消息的渲染升级。**依赖 00-darvin-api-extension**。

## 1. 背景

当前 `MessageItem.vue` + `StreamingText.vue` 只渲染 `whitespace-pre-wrap` 纯文本 + 三点动画。LobsterAI 的 assistant 消息支持 Markdown / KaTeX / GFM / CodeMirror 语法高亮 / 折叠 / 大文档截断 / hover 元信息 / Thinking 块折叠 / 图片附件 / Proposed plan 确认 / Fork。

## 2. 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | assistant 消息走 Markdown 渲染（KaTeX + GFM + 代码块） | 列表 / 表格 / 任务列表 / 数学公式 / 链接全部能渲染 |
| G2 | 代码块走语法高亮（基于 Shiki，**不引入 CodeMirror 6**，避免和 darvin-cowork 的轻量原则冲突） | TS / Python / Bash / JSON 等常见语言可用 |
| G3 | thinking 内容以折叠块展示，流式时自动展开 | ThinkingBlock 组件 + 蓝色脉冲点 |
| G4 | hover 显示时间戳 / 模型标签 / 复制按钮 / fork 按钮 | 4 个 hover 操作全部可见 |
| G5 | 消息列表按 turn 建模（user + 后续 assistant/tool 组成一个 turn） | `ConversationTurnsView` 等价组件 |
| G6 | 大文档（>8KB）切头 4KB + 尾 8KB + 「展开」按钮 | 阈值与 LobsterAI 对齐 |
| G7 | user 消息支持图片附件缩略图 | `imageAttachments` 渲染为 chip |
| G8 | 复制按钮、Regenerate（重生）按钮 | 在 `useChatActions` 加 copy / regenerate 工具方法 |

## 3. 非目标

- 不实现 Fork 会话（`onFork` 只接 UI 钩子，不实际切分 session）
- 不实现 proposed plan 标签解析（Go agent 当前没输出）
- 不引入 `react-markdown` / `CodeMirror 6` / `@uiw/react-codemirror`（darvin-cowork 用 Vue3 + 自研组件原则）
- 不实现图片本地预览（缩略图点击由 Artifact spec 接管）

## 4. 设计要点

### 4.1 组件树

```
MessageList.vue
└── ConversationTurn.vue  (一个 turn = user + assistantItems[])
    ├── UserMessage.vue
    │   ├── TextContent.vue
    │   └── ImageAttachmentChips.vue        ← 新
    └── AssistantTurnBlock.vue
        ├── ThinkingBlock.vue              ← 新（折叠 + 脉冲）
        ├── MarkdownContent.vue            ← 新（替换 StreamingText）
        │   ├── MarkdownBlock.vue
        │   ├── CodeBlock.vue              ← 新（Shiki 高亮）
        │   ├── TableBlock.vue
        │   └── MathBlock.vue              ← KaTeX
        └── TurnMeta.vue                   ← 新（hover 时间戳/模型/复制/fork）
```

### 4.2 Markdown 库选型

- **Markdown**：`markdown-it`（轻量、Vue 生态熟）
- **GFM 扩展**：`markdown-it-task-lists` / `markdown-it-mark`（表格用内置）
- **数学公式**：`markdown-it-katex`
- **代码高亮**：`shiki`（构建时生成高亮，运行时零成本） + 兜底 `highlight.js`
- **HTML 净化**：`dompurify`（只允许白名单标签）

### 4.3 ThinkingBlock 行为

```ts
const props = { content: string; isCurrentlyStreaming: boolean; maxCollapsedLines?: number }
const expanded = ref(props.isCurrentlyStreaming);  // 流式时强制展开
```

### 4.4 大文档截断阈值

| 阈值 | 行为 |
|---|---|
| `content.length <= 8KB` | 完整渲染 |
| `content.length > 8KB` | 头 4KB + 尾 8KB + 折叠占位 + 「展开」按钮 |

## 5. 用户场景

### 场景 1：agent 输出含代码块的回复

**Given** LLM 返回 ```ts\nconst foo = 1;\n```

**When** 走 `text_delta` 累积 → `done`

**Then** `MarkdownContent` 把代码块路由到 `CodeBlock`；高亮显示；右上角复制按钮可点

### 场景 2：流式中点开 thinking 块

**Given** 收到 `thinking_delta` 事件累积内容；`isCurrentlyStreaming=true`

**When** 渲染到一半用户点 ThinkingBlock header

**Then** 立即折叠；后续 thinking_delta 不再自动展开（用户已表态）

### 场景 3：超大回复（> 20KB）不卡 UI

**Given** assistant 消息 `content.length = 25KB`

**When** 渲染时

**Then** `MarkdownContent` 检测阈值；截断为头 4KB + 尾 8KB + 折叠占位；首屏 < 100ms

## 6. 验收

- [ ] `MarkdownContent` 渲染 LLM 真实输出无报错（包含：列表、表格、代码块、链接、LaTeX）
- [ ] 代码块支持至少 10 种语言高亮
- [ ] `ThinkingBlock` 流式自动展开，结束后保持展开，用户可手动折叠
- [ ] hover 4 个操作全部可见、可点击
- [ ] 大文档截断阈值生效
- [ ] `useChatActions` 暴露 `copy(content)` / `regenerate(messageId)`
- [ ] `npm run lint` + `npm run test` 通过
- [ ] 在 DevTools 手动验证 1 次长 prompt 流式无掉帧

## 7. 依赖

- **前置**：00-darvin-api-extension（必须先扩 `DarvinMessage.type`）
- **可并行**：02-tool-result-rendering（共用 turn 模型 + MarkdownContent）

## 8. 参考

### darvin-cowork
- `src/renderer/components/chat/MessageItem.vue` — 现状（要被替换）
- `src/renderer/components/chat/StreamingText.vue` — 现状
- `src/renderer/composables/useMessages.ts` — `appendToBucket` / `Message` 类型
- `src/renderer/composables/useChatActions.ts`

### LobsterAI（借鉴）
- `src/renderer/components/MarkdownContent.tsx` — 渲染主入口 + `shouldUseLargeMarkdownPreview`
- `src/renderer/components/CodeBlock.tsx` — CodeMirror 6（**darvin-cowork 改用 Shiki**）
- `src/renderer/components/cowork/ThinkingBlock.tsx:12-72`
- `src/renderer/components/cowork/AssistantMessageItem.tsx:54-237` — hover 元信息
- `src/renderer/components/cowork/UserMessageItem.tsx:178-347` — 图片附件徽章
- `src/renderer/components/cowork/ConversationTurnsView.tsx:21-147` — turn 模型
- `src/renderer/components/cowork/AssistantTurnBlock.tsx`
- `src/renderer/components/cowork/messageDisplayUtils.ts` — 工具归一化（在 02 用）

## 9. 关联调研

`specs/features/agent-output-ux-research/2026-08-01-cowork-vs-lobsterai-comparison.md` § 2.1「Agent 输出」+ § 3「协议层 gap」
