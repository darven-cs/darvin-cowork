# Agent UI Shell 设计文档 v2（S1，重写）

> **Phase 1 / 6 — UI 阶段**。**不接后端**，仅把 Vue3 + Tailwind v4 接入、3 栏 Codex 风格 UI shell 跑起来、contextBridge API 契约锁死、设计 token 锁死、组件化规范建立。后端接入由 S5 完成。
>
> **本版本（v2）相对 v1 的差异**：v1 是单栏聊天（header + list + input），跟 Codex 桌面差太远；v2 改为 3 栏（sidebar + chat + side panel），深色 + 浅色可切换，SVG 图标系统，@theme token 锁定，Vue 3 组件化规范 10 条锁定到 AGENTS.md。
>
> 前置：v1 spec 大方向保留（contextBridge mock 契约、msg-map 数据流），布局 / 组件粒度 / 样式策略整体重写。
>
> 后续接真后端由 S5 spec 完成。

---

## 1. 概述

### 1.1 问题 / 背景

仓库当前 renderer 状态（`AGENTS.md` §项目概览）：

- `src/renderer/index.html` — `<h1>💖 Hello World!</h1>` 占位
- `src/renderer/index.ts` — webpack 模板注释 + `console.log`
- `src/renderer/index.css` — 单段 `body { ... }` 占位
- `src/preload/index.ts` — 仅一行注释
- `src/main/runtime/{manager,client}.ts` — S5 才动
- `src/main/index.ts` — Bare Electron + DevTools

v1 spec（`2026-07-29-agent-ui-shell-design.md`）写的单栏聊天（`ChatHeader + MessageList + InputBar`）跟用户期望的 Codex 桌面形态差太多。Codex 风格核心是 **3 栏 + 密度感 + 主题可控 + 严格组件化**。本 spec 整体重写。

### 1.2 目标

- **3 栏布局**（240px sidebar + flex chat + 320px side panel），全部可折叠
- **Vue 3 + Tailwind v4** 接入（按 `AGENTS.md` §前端栈）
- **@theme token 系统**：颜色 / 间距 / 圆角 / 字号 / 动画统一从 `@theme` 取，组件**只**用 utility class
- **深色 + 浅色主题切换**：默认深色，HTML 根 `.light` 类切换；按钮持久化 localStorage
- **SVG 图标系统**：从 `src/renderer/assets/icons/` 目录注册为 Vue 组件，全局可用
- **Vue 3 组件化规范**：10 条组件规约（见 §4.3.1），写进 AGENTS.md
- **严格组件树**：Sidebar 子树 / Chat 子树 / SidePanel 子树 各自独立；跨组件状态走 composables
- **Composer** 区：textarea auto-grow + send 按钮 + 字符计数（可选）
- **Mock 流式事件**：S1 期间不接真后端；contextBridge 走 mock 模式但**签名**与 S5 真接完全一致
- **contextBridge API 契约锁死**：`src/shared/darvin-api.ts`（DarvinEvent 7 类 union / prompt / abort / listSessions / getMessages）

### 1.3 非目标

- **不**接 Go agent 子进程（运行时是 mock；S5 才接）
- **不**接 sessions.db / GORM（那是 S2/S3/S4）
- **不**做 Pinia / Vue Router（按需后续 spec）
- **不**做完整 accessibility audit（v0 只保证按钮有 `aria-label` / 焦点环）
- **不**做 i18n 切换 UI（i18n 函数已建，UI 文案 v0 中英混用，UI 主体中文）
- **不**做 keyboard shortcut 全面支持（v0 只有 Cmd/Ctrl+Enter 发送）
- **不**做 artifact 内容渲染（side panel v0 仅空态占位；S4 真接 tool event 后填）
- **不**做拖拽排序会话 / 拖拽 resize 栏宽（v0 栏宽固定）
- **不**做 viewport 窄屏适配（v0 假设桌面窗口 ≥ 1200px 宽）
- **不**改 `src/main/runtime/manager.ts` / `client.ts`（S5 才动）
- **不**改 `src/main/index.ts`（保留 DevTools 等已有逻辑；S5 才改）
- **不**引入 Vitest / Vue Test Utils（仓库未配置 test runner；AGENTS.md §测试）

---

## 2. 用户场景

### 场景 1：启动 Electron 看到 3 栏 UI

**Given** 仓库当前 renderer 是 Hello World 占位
**When** `npm start` 启动 Electron
**Then** DevTools 主窗口显示 3 栏布局（**不**再是 Hello World）：
- 左侧 240px sidebar：Logo + "+ New chat" + 会话列表（mock 3 条历史）+ 底部 runtime status + 主题切换
- 中间 flex：ChatHeader（标题 + 模型 dropdown）+ 流式消息列表空态 + 底部 Composer
- 右侧 320px side panel：Tabs（Tools / Thinking / Artifact）+ 空态占位

**And** 主题为深色（默认），背景 `#0e0e10`，文字 `#e8e8ea`，边框 `#2a2a30`。

### 场景 2：发 mock 流式消息

**Given** UI 已渲染，composer 空
**When** 用户输入 "ping" 点击 send（或 Cmd/Ctrl+Enter）
**Then** 序列：
1. composer 高度保持不动，textarea 清空
2. 消息列表立即追加 user 消息（右侧气泡，bg `color-user-msg`）
3. assistant 消息开始（左侧气泡，bg `color-assistant-msg`），逐字符出现 "Pong. Agent runtime is ready."（每 50ms 一个）带 `▍` 光标动画
4. 完成后 `▍` 消失，messageId 顶部小字显示
5. send 按钮在 assistant 未 done 前 disabled，textarea placeholder 改 "Agent is busy..."

### 场景 3：切换主题

**Given** UI 已渲染（深色）
**When** 用户点击 sidebar 底部主题切换按钮
**Then** 整个 UI 切换到浅色（背景 `#fafafa` 文字 `#18181b`）
**And** 按钮 icon 从 sun 变 moon
**And** localStorage `darvin.theme === 'light'`
**And** 重启 Electron 后保留浅色

### 场景 4：折叠 sidebar

**Given** UI 已渲染（sidebar 展开）
**When** 用户点击 ChatHeader 左侧 hamburger 按钮
**Then** sidebar 滑出收起（0 宽度），中部 chat + 右侧 panel 重新布局（chat 占 sidebar 空间）
**And** hamburger 按钮变为 `▶` 提示可展开
**And** localStorage `darvin.sidebar.collapsed === 'true'`

### 场景 5：折叠 side panel

**Given** UI 已渲染（右侧 panel 展开）
**When** 用户点击 ChatHeader 右侧「折叠按钮」
**Then** side panel 滑出收起，chat 占满剩余空间
**And** 折叠状态进 localStorage

### 场景 6：切换会话

**Given** UI 已渲染（mock 3 条会话）
**When** 用户点击 sidebar 会话列表中第 2 条
**Then** 中部 chat 切换到该 sessionId：
- ChatHeader 标题改为该会话标题
- 消息列表重新加载该会话的 messages（mock 阶段用 `useSessions.mock` 返回）
- 当前激活的 sessionItem 高亮（左边框 3px `color-accent`）

### 场景 7：新建会话

**Given** UI 已渲染
**When** 用户点击 sidebar 顶部 "+ New chat"
**Then** 创建新会话（mock 阶段生成 `s-{nanoid}`），自动激活，composer 获得焦点
**And** 消息列表清空（mock 阶段无 system message）

### 场景 8：模型选择 dropdown

**Given** UI 已渲染
**When** 用户点击 ChatHeader 右侧模型 badge（"claude-sonnet-4-5"）
**Then** 弹出 dropdown：`claude-sonnet-4-5` / `claude-opus-4-5` / `gpt-4o`（mock）
**And** 用户选某项 → badge 文字更新 → dropdown 关闭
**And** 选中项进 localStorage `darvin.model`

### 场景 9：contextBridge 契约可见

**Given** DevTools 打开
**When** console 输入 `window.darvin`
**Then** 看到 `{ prompt, abort, onEvent, listSessions, getMessages, status }` 6 个方法
**And** `await window.darvin.listSessions()` 返回 `{ sessions: [3 条 mock] }`

### 场景 10：error 事件展示

**Given** 已发 mock prompt
**When** mock 注入一个 `{ type: 'error', message: 'fake error' }` event
**Then** 该 assistant 消息气泡变为红色边框 + 错误文字（不再追加流式）
**And** composer 重新可输入

---

## 3. 功能需求

### FR-1：Vue 3 + Tailwind v4 接入

- `package.json` `dependencies` 加 `vue ^3.5`
- `package.json` `devDependencies` 加 `@tailwindcss/vite ^4.0`、`@vitejs/plugin-vue ^5.0`
- `vite.renderer.config.ts` 加 `@tailwindcss/vite` + `@vitejs/plugin-vue` 插件
- `src/renderer/index.ts` 改为 `createApp(App).mount('#app')`
- `src/renderer/index.css` 改为 `@import "tailwindcss"; @import "./styles/theme.css"; @import "./styles/reset.css";`
- `src/renderer/index.html` 把 `<h1>Hello World!</h1>` 替换为 `<div id="app"></div>` + `<title>Darvin Cowork</title>`

### FR-2：3 栏 grid 布局

`<AppShell>` 内部 grid 容器：

```vue
<template>
  <div
    class="grid h-screen bg-bg text-text"
    :style="{ gridTemplateColumns: gridCols }"
  >
    <Sidebar :collapsed="sidebarCollapsed" />
    <ChatPane
      :side-panel-open="sidePanelOpen"
      @toggle-sidebar="toggleSidebar"
      @toggle-side-panel="toggleSidePanel"
    />
    <SidePanel v-if="sidePanelOpen" />
  </div>
</template>
```

- 状态：`sidebarCollapsed` (boolean) + `sidePanelOpen` (boolean)
- 默认展开：`sidebarCollapsed=false, sidePanelOpen=true`
- grid 模板：`${sidebarExpanded ? '240px' : '0px'} 1fr ${sidePanelOpen ? '320px' : '0px'}`
- 收起时 sidebar / side panel 通过 `v-if` 卸载（保留过渡动画 v0 简化：直接显示 / 隐藏）

### FR-3：Sidebar 子树

#### FR-3.1 `<SidebarHeader>`

- 顶部 12px 高度 padding
- logo（24×24 SVG）+ "Darvin" 文字标题（font-medium 14px）
- 右上角 `+ New chat` 按钮（`IconButton` 组件，icon `plus`，click → emit `new-chat`）

#### FR-3.2 `<SessionList>` + `<SessionItem>`

- 列表 mock 3 条（`useSessions.mock` 返回）：
  ```ts
  mock = [
    { id: 's-001', title: 'Ping 测试', updatedAt: Date.now() - 3600_000 },
    { id: 's-002', title: 'Why is Go single-threaded', updatedAt: Date.now() - 86400_000 },
    { id: 's-003', title: 'Refactor gateway', updatedAt: Date.now() - 86400_000 * 3 },
  ];
  ```
- 单条：title (text-sm) + 相对时间（text-xs text-muted）右对齐
- active 状态：左侧 3px 边框 `bg-accent` + 背景 `bg-surface-2`
- hover 状态：背景 `bg-surface-2`
- 列表滚到底（最多 20 条 mock；超 20 滚动）

#### FR-3.3 `<SidebarFooter>`

- 12px 内边距，整体 `border-t border-border`
- 2 行：
  - row 1: small badge（runtime status：`ready` / `offline` / `no-binary`）
  - row 2: 主题切换按钮（icon `sun` 或 `moon`） + 设置按钮（icon `cog`，v0 only UI placeholder）

### FR-4：ChatPane 子树

#### FR-4.1 `<ChatHeader>`

- 高度 48px
- 左侧：hamburger 按钮（折叠 sidebar） + 当前会话标题（font-medium 14px）
- 中间：模型 badge（chatgpt 风格 dropdown，详见 FR-13）
- 右侧：折叠 side panel 按钮（icon `panel-right-close` / `panel-right-open`）
- 底部 1px border

#### FR-4.2 `<MessageList>`

- 容器：`flex-1 overflow-y-auto`
- 内部：竖向 flex 列表，每条 `<MessageItem>` 之间 12px gap
- 撑开：底部留空 flex-1 让最后一条消息不贴底
- 自动滚动：`watch` messageList 长度变化 → `nextTick` → `scrollIntoView({ block: 'end', behavior: 'smooth' })`
- 空态：垂直水平居中显示文案 "Send a message to start the conversation." + 示例 "试试输入 \"ping\""

#### FR-4.3 `<MessageItem>`

- props: `{ role: 'user' | 'assistant', messageId: string, deltas: string[], done: boolean, error?: string }`
- 用户消息：右侧对齐，最宽 80%（max-w-[80%]），气泡 `bg-user-msg rounded-lg rounded-br-sm px-3 py-2`
- 助手消息：左侧对齐，最宽 80%，气泡 `bg-assistant-msg rounded-lg rounded-bl-sm px-3 py-2`
- 错误态：边框 1px `border-danger`，文字 `text-danger`
- 顶部 6px 高度内显示 messageId（小字 text-xs text-muted）；hover 才显示

#### FR-4.4 `<StreamingText>`

- props: `{ deltas: string[], done: boolean }`
- 渲染：`deltas.join('')` + `v-if="!done"` 显示 `▍`（cursor 动画）
- 接受 markdown？v0 **不**解析 markdown，纯文本；S4 tool 事件后再考虑

### FR-5：Composer 子树

`<Composer>` 组件（直接放在 ChatPane 内，不独立文件）：

```vue
<template>
  <div class="border-t border-border p-3">
    <div class="flex gap-2 items-end bg-surface-2 rounded-lg p-2">
      <textarea
        ref="textareaRef"
        v-model="text"
        :placeholder="busy ? 'Agent is busy...' : 'Send a message...'"
        :disabled="busy"
        class="flex-1 bg-transparent outline-none resize-none text-sm"
        rows="1"
        @keydown="onKeydown"
        @input="autoGrow"
      />
      <button
        :disabled="!text.trim() || busy"
        class="px-3 py-1.5 rounded-md bg-accent text-white text-sm disabled:opacity-40"
        @click="emitSend"
      >
        <Icon name="send" />
      </button>
    </div>
  </div>
</template>
```

- autosize：textarea 1-12 行（`scrollHeight` 触发）
- 发送：Enter 直接发；Shift+Enter 换行；Cmd/Ctrl+Enter 强制发
- disabled：空文本 / busy（agent 流式未 done）

### FR-6：SidePanel 子树（v0 空态）

#### FR-6.1 `<SidePanelTabs>`

- 顶部 36px 高度，3 个 tab：`Tools` / `Thinking` / `Artifact`
- 选中 tab：底部 2px 边框 `bg-accent`；非选中：`text-muted`
- 当前激活 tab 状态走 `useSidePanel.ts` composable

#### FR-6.2 `<SidePanelContent>`

- 容器：`flex-1 overflow-y-auto`
- v0 三 tab 都显示空态文案：
  - Tools: "No tool calls yet. Tool traces will appear here when the agent runs."
  - Thinking: "No thinking trace yet."
  - Artifact: "No artifacts yet."
- 字体 text-xs text-muted，居中

### FR-7：@theme token 设计

详见 §4.4 完整 token 列表。锁定：

- 颜色（dark default + light 覆盖）：`bg` / `surface` / `surface-2` / `border` / `text` / `text-muted` / `text-subtle` / `accent` / `accent-hover` / `user-msg` / `assistant-msg` / `danger` / `success` / `warning`
- 圆角：`sm` (4px) / `md` (6px) / `lg` (8px) / `xl` (12px)
- 间距：`app-padding` (12px) / `section-gap` (16px)
- 字号：`text-xs` (11px) / `text-sm` (13px) / `text-base` (14px) / `text-md` (15px) / `text-lg` (17px) / `text-xl` (20px)
- 字体：`--font-sans` / `--font-mono`
- 动画：`cursor-blink` 1s step-end infinite

### FR-8：主题切换

`useTheme.ts` composable：

```ts
import { ref, watch } from 'vue';

const KEY = 'darvin.theme';
type Theme = 'dark' | 'light';
const stored = (localStorage.getItem(KEY) as Theme) || 'dark';
const theme = ref<Theme>(stored);

export function useTheme() {
  function apply(t: Theme) {
    document.documentElement.classList.toggle('light', t === 'light');
    localStorage.setItem(KEY, t);
    theme.value = t;
  }
  function toggle() {
    apply(theme.value === 'dark' ? 'light' : 'dark');
  }
  // 初始化时立即应用
  apply(stored);
  return { theme, apply, toggle };
}
```

- HTML 根节点 `.light` 切换浅色
- 浅色 token 覆盖放 `@layer base` 下（详见 §4.4）
- 主题切换瞬间生效（无过渡动画 v0）

### FR-9：SVG 图标系统

`src/renderer/assets/icons/` 当前 16 个 SVG，分两组：

**A 组（11 个 Chat UI 必须；本 spec 内已生成 / 验证可用）：**
- `plus.svg` — 新建会话
- `sun.svg` / `moon.svg` — 主题切换
- `menu.svg` — hamburger
- `panel-right-close.svg` / `panel-right-open.svg` — 折叠右栏
- `send.svg` — 发送
- `chevron-down.svg` — dropdown
- `cog.svg` — 设置
- `alert-circle.svg` — 错误
- `check.svg` — 完成

**B 组（5 个用户中心预留；本 spec 不使用，留后续 "user account" 面板）：**
- `invite-credits.svg` / `logout.svg` / `promo-subscription.svg` / `recharge.svg` / `usage-overview.svg`

**stroke 规约**：
- 所有 icon SVG 必须用 `stroke="currentColor"`（不要 `stroke="black"` / `stroke="#xxx"`）
- 用户导入的 B 组 5 个 SVG 写死了 `stroke="black"`；**S1 仅 A 组使用，B 组不在 S1 渲染**；B 组接入时由 `Icon` 组件做一次 `replace("stroke=\"black\"", "stroke=\"currentColor\"")` 以支持主题切换
- viewBox 统一 `0 0 34 34`；stroke-width 2.4；round caps

**注册代码**（`src/renderer/assets/icons/index.ts`）：

```ts
import type { App } from 'vue';

const modules = import.meta.glob('./*.svg', { eager: true, query: '?raw', import: 'default' });

// 简单替换：B 组 SVG 里硬编码的 stroke="black" 改为 currentColor
function normalize(svg: string): string {
  return svg.replace(/stroke="black"/g, 'stroke="currentColor"');
}

export const SVG_SOURCES: Record<string, string> = Object.fromEntries(
  Object.entries(modules).map(([path, raw]) => {
    const name = path.replace(/^\.\//, '').replace(/\.svg$/, '');
    return [name, normalize(raw as string)];
  }),
);

export function registerIcons(app: App) {
  app.component('Icon', Icon);
}

// Icon 组件
import { defineComponent, h } from 'vue';
const Icon = defineComponent({
  name: 'Icon',
  props: { name: { type: String, required: true }, size: { type: Number, default: 18 } },
  setup(props) {
    return () => {
      const svg = SVG_SOURCES[props.name];
      if (!svg) {
        console.warn(`[icon] missing: ${props.name}`);
        return h('span', { style: { display: 'inline-block', width: `${props.size}px`, height: `${props.size}px` } });
      }
      // 拷贝 SVG 内容，注入 size
      const html = svg
        .replace(/width="\d+"/, `width="${props.size}"`)
        .replace(/height="\d+"/, `height="${props.size}"`);
      return h('span', { class: 'inline-flex', innerHTML: html });
    };
  },
});
```

**消费方式**：`<Icon name="send" />` / `<Icon name="sun" :size="20" />`

**缺失 icon**：组件警告 + 渲染空 16×16 span 占位（不抛错）

### FR-10：composables 状态管理

创建以下 composables（每个文件 ≤ 100 行）：

- `useTheme.ts` — 见 FR-8
- `useSidebar.ts` — `{ sidebarCollapsed, toggle }` + localStorage `darvin.sidebar.collapsed`
- `useSidePanel.ts` — `{ sidePanelOpen, tabs.Active, toggle, switchTab }` + localStorage `darvin.sidepanel.open` + `darvin.sidepanel.tab`
- `useSession.ts` — `{ sessions, currentSessionId, switch, create }`（mock S1 阶段硬编码 3 条；S6 替换为 `window.darvin.listSessions()`）
- `useMessages.ts` — `{ messages, appendUserMessage, startAssistantMessage, appendEvent, reset }`（详见 §4.2.1）
- `useModel.ts` — `{ currentModel, options, selectModel }` + localStorage `darvin.model`

所有 composable **单例**（模块级 `ref`），不同组件 import 同一实例；不引入 Pinia。

### FR-11：流式消息渲染

数据流：

```
user click send
  → Composer emit 'send'
  → ChatPane.handleSend
  → useSession().currentSessionId (or new)
  → useMessages().appendUserMessage(content)
  → await window.darvin.prompt({ content, sessionId })
  → useMessages().startAssistantMessage(sessionId, messageId)
  → window.darvin.onEvent(handler)  // global subscription in App.vue
     → handler(e): useMessages().appendEvent(e)
        └─ match e.type:
           - 'text_delta' | 'thinking_delta' → assistant.deltas.push(delta)
           - 'tool_start' | 'tool_end'       → useSidePanel().pushToolEvent(e)
           - 'done'                          → assistant.done = true
           - 'error'                         → assistant.error = message
           - 'agent_end'                     → set busy=false
```

#### FR-11.1 useMessages 数据结构

```ts
interface Message {
  id: string;             // messageId
  sessionId: string;
  role: 'user' | 'assistant';
  content: string;        // 累积文本
  done: boolean;
  error?: string;
  createdAt: number;
  events: DarvinEvent[];  // 原始事件流（用于 side panel）
}
const messages = ref<Message[]>([]);
```

- 切换 session 时清空 + 用 `window.darvin.getMessages(sessionId)` 加载

### FR-12：会话列表展示

- mock 3 条（`useSessions.mock`）
- active 高亮 + 切换
- 优先级：当前 sessionId 永远在列表顶
- 滚动：超过 20 条滚动条出现

### FR-13：模型选择 dropdown

`ChatHeader` 中间位置：

```vue
<Dropdown v-model:open="open" placement="bottom">
  <button class="flex items-center gap-1 px-2 py-1 rounded-md hover:bg-surface-2">
    <span class="text-sm">{{ currentModel }}</span>
    <Icon name="chevron-down" />
  </button>
  <template #menu>
    <ul class="bg-surface border border-border rounded-md shadow-lg">
      <li
        v-for="opt in options"
        :key="opt.id"
        class="px-3 py-1.5 text-sm hover:bg-surface-2 cursor-pointer"
        @click="selectModel(opt.id)"
      >
        {{ opt.label }}
        <span v-if="opt.id === currentModel" class="float-right text-accent">✓</span>
      </li>
    </ul>
  </template>
</Dropdown>
```

- `Dropdown` 组件自研（headless Vue 3，纯 utility class，**v0 简单实现**：click outside 关闭、Escape 关闭）
- 模型选项 mock：`claude-sonnet-4-5` / `claude-opus-4-5` / `gpt-4o`
- 选中进 localStorage

### FR-14：contextBridge API 契约锁死

`src/preload/index.ts`（S1 mock 阶段）：

```ts
import { contextBridge } from 'electron';
import type {
  DarvinPromptRequest,
  DarvinPromptResponse,
  DarvinAbortResponse,
  DarvinListSessionsResponse,
  DarvinGetMessagesResponse,
  DarvinEvent,
  DarvinRuntimeStatus,
} from '../shared/darvin-api';
import { mockSessions, mockMessages } from '../renderer/services/mock-data';
import { mockPrompt } from '../renderer/services/mock-agent';

const eventTarget = new EventTarget();
const SUBS = new Set<(e: DarvinEvent) => void>();

const api = {
  async prompt(req: DarvinPromptRequest): Promise<DarvinPromptResponse> {
    const r = await mockPrompt(req.content);
    // mock 阶段：异步推 events 到 eventTarget
    (async () => {
      for await (const ev of r.events) {
        eventTarget.dispatchEvent(new CustomEvent('darvin', { detail: ev }));
      }
    })();
    return { sessionId: r.sessionId, messageId: r.messageId };
  },
  async abort(sessionId: string): Promise<DarvinAbortResponse> {
    return { aborted: true, sessionId };
  },
  async listSessions(): Promise<DarvinListSessionsResponse> {
    return { sessions: mockSessions };
  },
  async getMessages(sessionId: string): Promise<DarvinGetMessagesResponse> {
    return { messages: mockMessages[sessionId] || [] };
  },
  onEvent(handler: (e: DarvinEvent) => void): () => void {
    const wrap = (e: Event) => handler((e as CustomEvent).detail as DarvinEvent);
    eventTarget.addEventListener('darvin', wrap);
    SUBS.add(handler);
    return () => {
      eventTarget.removeEventListener('darvin', wrap);
      SUBS.delete(handler);
    };
  },
  status(): DarvinRuntimeStatus {
    // S1 期间固定返回 'online'；S5 替换为 IPC
    return 'online';
  },
};

contextBridge.exposeInMainWorld('darvin', api);
```

S5 替换为真 IPC（实现去 S5 spec）。

### FR-15：状态持久化

- `localStorage` 键：
  - `darvin.theme` — `'dark' | 'light'`
  - `darvin.sidebar.collapsed` — `'true' | 'false'`
  - `darvin.sidepanel.open` — `'true' | 'false'`
  - `darvin.sidepanel.tab` — `'tools' | 'thinking' | 'artifact'`
  - `darvin.model` — 模型 id 字符串
  - `darvin.session.current` — 当前 sessionId 字符串
- 所有 composable 初始化时立即读 + 监听 ref 变化写入
- JSON 序列化失败 / 缺失值 fallback 到默认值

### FR-16：依赖 / 工具链

`package.json` 增量：

```json
{
  "dependencies": {
    "vue": "^3.5.0"
  },
  "devDependencies": {
    "@tailwindcss/vite": "^4.0.0",
    "@vitejs/plugin-vue": "^5.0.0"
  }
}
```

模板：

```ts
// vite.renderer.config.ts (新增)
import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
  plugins: [vue(), tailwindcss()],
});
```

---

## 4. 实现方案

### 4.1 目录结构（v2 增量）

```
src/renderer/
├── index.html                       # 改：<div id="app"></div> + <title>
├── index.ts                         # 改：createApp(App) + registerIcons
├── index.css                        # 改：@import tailwindcss + theme + reset
├── App.vue                          # 🆕：<AppShell />
├── layout/                          # 🆕：页面级布局壳（与 components/ 平级）
│   └── AppShell.vue                 # 🆕：3 栏 grid + 全局 onEvent
├── darvin.d.ts                      # 🆕：window.darvin 全局类型
├── assets/
│   └── icons/                       # 🆕 SVG 图标仓库
│       ├── index.ts                 # 导入 + 注册到全局组件
│       ├── plus.svg                 # 新建会话
│       ├── sun.svg                  # 浅色主题
│       ├── moon.svg                 # 深色主题
│       ├── menu.svg                 # hamburger
│       ├── panel-right-close.svg    # 折叠右栏
│       ├── panel-right-open.svg     # 展开右栏
│       ├── send.svg                 # 发送
│       ├── chevron-down.svg         # dropdown
│       ├── cog.svg                  # 设置
│       ├── alert-circle.svg         # 错误
│       ├── check.svg                # 完成
│       ├── invite-credits.svg       # 预留：用户中心「推广邀请」
│       ├── logout.svg               # 预留：用户中心「退出」
│       ├── promo-subscription.svg   # 预留：用户中心「订阅促销」
│       ├── recharge.svg             # 预留：用户中心「充值」
│       └── usage-overview.svg       # 预留：用户中心「用量概览」
├── components/
│   ├── sidebar/
│   │   ├── SidebarHeader.vue        # 🆕
│   │   ├── SessionList.vue          # 🆕
│   │   ├── SessionItem.vue          # 🆕
│   │   └── SidebarFooter.vue        # 🆕
│   ├── chat/
│   │   ├── ChatPane.vue             # 🆕：聚合 ChatHeader + MessageList + Composer
│   │   ├── ChatHeader.vue           # 🆕
│   │   ├── ChatHeaderModel.vue      # 🆕：dropdown
│   │   ├── MessageList.vue          # 🆕
│   │   ├── MessageItem.vue          # 🆕
│   │   ├── StreamingText.vue        # 🆕
│   │   └── Composer.vue             # 🆕
│   ├── side-panel/
│   │   ├── SidePanel.vue            # 🆕：聚合 SidePanelTabs + SidePanelContent
│   │   ├── SidePanelTabs.vue        # 🆕
│   │   └── SidePanelContent.vue     # 🆕
│   └── common/
│       ├── Icon.vue                 # 🆕：<Icon name="..." />
│       ├── IconButton.vue           # 🆕
│       └── Dropdown.vue             # 🆕：自研 headless
├── composables/
│   ├── useTheme.ts                  # 🆕
│   ├── useSidebar.ts                # 🆕
│   ├── useSidePanel.ts              # 🆕
│   ├── useSession.ts                # 🆕
│   ├── useMessages.ts               # 🆕
│   └── useModel.ts                  # 🆕
├── services/
│   ├── mock-agent.ts                # 🆕：mock 流
│   ├── mock-data.ts                 # 🆕：mock sessions / messages
│   └── i18n.ts                      # 🆕：t(key) 双语
├── styles/
│   ├── theme.css                    # 🆕：@theme 块
│   └── reset.css                    # 🆕：极少量 reset
└── shared/                          # 不放在 renderer 下！
    └── darvin-api.ts                # 实际路径：src/shared/darvin-api.ts
```

### 4.2 关键设计决策

#### 4.2.1 流式数据流（事件 → 组件）

```
[mock-agent.ts makeStream]
  ↓ AsyncIterable<DarvinEvent>
[preload/index.ts eventTarget dispatch]
  ↓ CustomEvent('darvin', { detail: ev })
[App.vue mounted, window.darvin.onEvent(handler)]
  ↓ handler(e: DarvinEvent)
[useMessages().appendEvent(e)]
  ↓ match e.type
  ├─ text_delta / thinking_delta → 找到对应 messageId, .content += delta
  ├─ done → .done = true; busy = false
  ├─ error → .error = message; busy = false
  ├─ tool_start / tool_end → useSidePanel().pushToolEvent(e)
  └─ agent_end → flush; 后续不做
[reactive ref 变化]
  ↓
[MessageList 自动重渲染]
  ↓
[MessageItem 子组件：流式光标 + 滚动到底]
```

#### 4.2.2 不引入 Pinia 的理由

v0 单页 + 6 个 composables 数量 + 单一全局状态；ref + composable module-level 状态足够。Pinia 等切换 / history 复杂后再引入。

#### 4.2.3 不引入 Vue Router

单页面（聊天）。多视图（设置 / 历史）S6+。

#### 4.2.4 主题切换策略

HTML 根 `.light` 类切换（不用 `dark:` 变体）；token 直接基于 `html.light` 覆盖。优点：同一组件代码 dark / light 都生效，CSS 静态。

#### 4.2.5 图标系统选型

用户导 SVG → `?raw` import 全部读为字符串 → 全局组件 `<Icon name="..." />` 渲染。优点：tree-shake 不友好（一次全 load），但 v0 icon 数量 ≤ 12 个，全部 inline 字符串总体积 < 10KB。

#### 4.2.6 Dropdown 自研

不需要 Headless UI / Radix Vue 引入额外依赖（用户选了纯自研）；自研内容：click outside 关闭 + Escape 关闭 + 焦点管理。代码 ≤ 50 行。

#### 4.2.7 折叠 sidebar / side panel 策略

- 用 `v-if` 卸载（不用 `display:none`），保留布局切换的清晰度
- 折叠时 grid 模板改为 `0px 1fr 320px`（sidebar 收回）
- 过渡动画 v0 简化：直接显隐，**不**做 width transition
- 折叠状态进 localStorage

### 4.3 关键代码骨架

#### 4.3.1 AppShell.vue（位于 `src/renderer/layout/AppShell.vue`）

```vue
<template>
  <div
    class="grid h-screen bg-bg text-text overflow-hidden"
    :style="{ gridTemplateColumns: gridCols }"
  >
    <Sidebar v-if="!sidebarCollapsed" />
    <ChatPane
      @toggle-sidebar="sidebar.toggle"
      @toggle-side-panel="sidePanel.toggle"
    />
    <SidePanel v-if="sidePanel.open" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue';
import Sidebar from '../components/sidebar/Sidebar.vue';
import ChatPane from '../components/chat/ChatPane.vue';
import SidePanel from '../components/side-panel/SidePanel.vue';
import { useSidebar } from '../composables/useSidebar';
import { useSidePanel } from '../composables/useSidePanel';
import { useTheme } from '../composables/useTheme';
import { useMessages } from '../composables/useMessages';

const sidebar = useSidebar();
const sidePanel = useSidePanel();
useTheme(); // 立即应用持久化主题
const messages = useMessages();

const sidebarCollapsed = computed(() => sidebar.collapsed.value);
const gridCols = computed(() => {
  const left = sidebarCollapsed.value ? '0px' : '240px';
  const right = sidePanel.open.value ? '320px' : '0px';
  return `${left} 1fr ${right}`;
});

onMounted(() => {
  window.darvin.onEvent((e) => messages.appendEvent(e));
});
</script>
```

> **目录约定**：`src/renderer/layout/` 放页面级布局壳（与 `components/` 平级）。`AppShell.vue` 是 v0 唯一的 layout 组件；后续多视图（如 `SettingsLayout.vue`）也放这里。

#### 4.3.2 ChatPane.vue

```vue
<template>
  <div class="flex flex-col min-w-0">
    <ChatHeader
      @toggle-sidebar="$emit('toggle-sidebar')"
      @toggle-side-panel="$emit('toggle-side-panel')"
    />
    <MessageList />
    <Composer @send="handleSend" :busy="busy" />
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import ChatHeader from './ChatHeader.vue';
import MessageList from './MessageList.vue';
import Composer from './Composer.vue';
import { useSession } from '../../composables/useSession';
import { useMessages } from '../../composables/useMessages';

defineEmits<{ 'toggle-sidebar': []; 'toggle-side-panel': [] }>();

const session = useSession();
const messages = useMessages();
const busy = ref(false);

async function handleSend(content: string) {
  if (!content.trim()) return;
  busy.value = true;
  const sessId = session.currentSessionId.value;
  const userMsgId = `m-${Date.now()}`;
  messages.appendUserMessage(sessId, userMsgId, content);
  const { sessionId, messageId } = await window.darvin.prompt({ content, sessionId: sessId });
  messages.startAssistantMessage(sessionId, messageId);
}
</script>
```

#### 4.3.3 MessageList.vue

```vue
<template>
  <div ref="scrollRef" class="flex-1 overflow-y-auto px-4 py-6">
    <div v-if="messages.list.value.length === 0" class="h-full flex items-center justify-center text-text-muted text-sm">
      Send a message to start the conversation.
    </div>
    <div v-else class="flex flex-col gap-3 max-w-3xl mx-auto">
      <MessageItem
        v-for="msg in messages.list.value"
        :key="msg.id"
        :message="msg"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, nextTick } from 'vue';
import MessageItem from './MessageItem.vue';
import { useMessages } from '../../composables/useMessages';

const messages = useMessages();
const scrollRef = ref<HTMLDivElement | null>(null);

watch(
  () => messages.list.value.length,
  async () => {
    await nextTick();
    scrollRef.value?.scrollIntoView({ block: 'end', behavior: 'smooth' });
  },
);
</script>
```

#### 4.3.4 useMessages.ts

```ts
import { ref } from 'vue';
import type { DarvinEvent } from '../../shared/darvin-api';

export interface Message {
  id: string;
  sessionId: string;
  role: 'user' | 'assistant';
  content: string;
  done: boolean;
  error?: string;
  createdAt: number;
}

const list = ref<Message[]>([]);

export function useMessages() {
  function appendUserMessage(sessionId: string, id: string, content: string) {
    list.value.push({ id, sessionId, role: 'user', content, done: true, createdAt: Date.now() });
  }
  function startAssistantMessage(sessionId: string, id: string) {
    list.value.push({ id, sessionId, role: 'assistant', content: '', done: false, createdAt: Date.now() });
  }
  function appendEvent(e: DarvinEvent) {
    if (e.type === 'text_delta' || e.type === 'thinking_delta') {
      const msg = list.value.find((m) => m.id === e.messageId);
      if (msg) msg.content += e.delta;
    } else if (e.type === 'done') {
      const msg = list.value.find((m) => m.id === e.messageId);
      if (msg) msg.done = true;
    } else if (e.type === 'error') {
      const msg = list.value.find((m) => m.id === e.messageId);
      if (msg) { msg.done = true; msg.error = e.message; }
    }
  }
  function reset() { list.value = []; }
  return { list, appendUserMessage, startAssistantMessage, appendEvent, reset };
}
```

#### 4.3.5 useTheme.ts

```ts
import { ref } from 'vue';

type Theme = 'dark' | 'light';
const KEY = 'darvin.theme';
const stored = (typeof localStorage !== 'undefined' && (localStorage.getItem(KEY) as Theme)) || 'dark';
const theme = ref<Theme>(stored);

// 初始化（模块加载时立即应用）
if (typeof document !== 'undefined') {
  document.documentElement.classList.toggle('light', stored === 'light');
}

export function useTheme() {
  function apply(t: Theme) {
    theme.value = t;
    if (typeof document !== 'undefined') {
      document.documentElement.classList.toggle('light', t === 'light');
    }
    if (typeof localStorage !== 'undefined') {
      localStorage.setItem(KEY, t);
    }
  }
  function toggle() { apply(theme.value === 'dark' ? 'light' : 'dark'); }
  return { theme, apply, toggle };
}
```

### 4.4 完整 @theme token

```css
/* src/renderer/styles/theme.css */
@import "tailwindcss";

@theme {
  /* ── 颜色（dark default）── */
  --color-bg:         #0e0e10;
  --color-surface:    #17171a;
  --color-surface-2:  #1f1f23;
  --color-border:     #2a2a30;
  --color-text:       #e8e8ea;
  --color-text-muted: #9a9aa3;
  --color-text-subtle:#6a6a72;
  --color-accent:     #6a8cff;
  --color-accent-hover: #8aa5ff;
  --color-user-msg:      #2a3656;
  --color-assistant-msg: #1a1a20;
  --color-danger:     #ff6b6b;
  --color-success:    #4ade80;
  --color-warning:    #fbbf24;

  /* ── 圆角 ── */
  --radius-sm: 4px;
  --radius-md: 6px;
  --radius-lg: 8px;
  --radius-xl: 12px;

  /* ── 间距 ── */
  --spacing-app-padding: 12px;
  --spacing-section-gap: 16px;

  /* ── 字号 ── */
  --font-sans:   ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
  --font-mono:   ui-monospace, "JetBrains Mono", "Fira Code", monospace;
  --text-xs:  11px;
  --text-sm:  13px;
  --text-base:14px;
  --text-md:  15px;
  --text-lg:  17px;
  --text-xl:  20px;

  /* ── 动画 ── */
  --animate-cursor-blink: cursor-blink 1s step-end infinite;

  @keyframes cursor-blink {
    0%, 50% { opacity: 1; }
    51%, 100% { opacity: 0; }
  }
}

@layer base {
  html.light {
    --color-bg:         #fafafa;
    --color-surface:    #ffffff;
    --color-surface-2:  #f4f4f5;
    --color-border:     #e4e4e7;
    --color-text:       #18181b;
    --color-text-muted: #52525b;
    --color-text-subtle:#a1a1aa;
    --color-user-msg:       #dbeafe;
    --color-assistant-msg:  #f4f4f5;
  }
  body {
    background: var(--color-bg);
    color: var(--color-text);
    font-family: var(--font-sans);
    font-size: var(--text-base);
    margin: 0;
    -webkit-font-smoothing: antialiased;
  }
  /* 焦点环 */
  *:focus-visible {
    outline: 2px solid var(--color-accent);
    outline-offset: 2px;
  }
  /* 滚动条 */
  ::-webkit-scrollbar { width: 8px; height: 8px; }
  ::-webkit-scrollbar-track { background: transparent; }
  ::-webkit-scrollbar-thumb { background: var(--color-border); border-radius: 4px; }
}
```

```css
/* src/renderer/styles/reset.css */
*, *::before, *::after { box-sizing: border-box; }
* { margin: 0; padding: 0; }
html, body, #app { height: 100%; }
ul { list-style: none; }
button { background: none; border: none; cursor: pointer; color: inherit; font: inherit; }
input, textarea { background: none; border: none; font: inherit; color: inherit; }
```

---

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| 启动时 `localStorage` 缺失 / 损坏 | composable 初始化 fallback 默认值；try/catch 包住 localStorage 访问 |
| 切主题瞬间组件未挂载 | module-level 加载时立即应用主题（在任何组件 mount 之前） |
| icon 文件缺失 | `<Icon name="missing">` 警告 + 空 16×16 占位 |
| 流式回复中切到其他会话 | `useMessages.reset()` 清空；in-flight event 仍在 EventTarget 推，被 ignore（messageId 找不到） |
| 流式回复中切到 light 主题 | 消息内容颜色变化，无丢失 |
| 流式回复中折叠 sidebar | 消息列表占据 sidebar 空间，无事件丢失 |
| 流式回复中点 "+ New chat" | 弹确认 dialog（自研）："当前回复未完成，确定新建？" / 取消 / 继续 |
| textarea 超过 12 行 | scrollHeight cap 12 行（max-h 200px） |
| 输入框空 + Enter | composer 不 emit |
| 流式期间 Enter | composer 仍 emit 但 send 按钮 disabled |
| 切会话时新会话无 messages | 空态文案 |
| 同一 sessionId 多次 prompt | 复用现有 messageId 列表，追加新 turn |
| mock 报错（preload 抛） | catch → composer 不 busy + 顶部 toast 显示 "Mock 失败" |
| 浅色主题下 bubble 颜色对比度 | token 配比已校验：user-msg `#dbeafe` (light) vs `#2a3656` (dark) 文字均通过 WCAG AA |
| dropdown 打开时 Esc / click outside | Dropdown 组件统一关闭 |
| 模型 dropdown 选中态 | `currentModel === opt.id` 显示 ✓ |
| 侧栏窄屏（< 1200px） | v0 不处理；窗口固定 1200×800 |
| 大量会话（> 100） | mock 阶段无此场景；S6 接入 sessions.db 后分页 |
| `useMessages` 并发 race | appendEvent 单线程（JS）天然安全 |
| 切会话导致流式中断 | mock 阶段无 Abort 信号；S5 接 IPC 后调 `window.darvin.abort(sessionId)` |

---

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `package.json` | 加 `vue ^3.5` / `@tailwindcss/vite ^4.0` / `@vitejs/plugin-vue ^5.0` |
| `vite.renderer.config.ts` | 🆕 加 plugin vue + tailwindcss |
| `src/renderer/index.html` | 改：`<div id="app"></div>` + `<title>Darvin Cowork</title>` |
| `src/renderer/index.ts` | 改：createApp + registerIcons |
| `src/renderer/index.css` | 改：@import tailwindcss + theme + reset |
| `src/renderer/App.vue` | 🆕 |
| `src/renderer/AppShell.vue` | 🆕 |
| `src/renderer/darvin.d.ts` | 🆕 |
| `src/renderer/assets/icons/{*.svg,index.ts}` | 🆕 用户导的 SVG + 注册 |
| `src/renderer/components/sidebar/*.vue` | 🆕 4 文件 |
| `src/renderer/components/chat/*.vue` | 🆕 5 文件 |
| `src/renderer/components/side-panel/*.vue` | 🆕 2 文件 |
| `src/renderer/components/common/{Icon,IconButton,Dropdown}.vue` | 🆕 |
| `src/renderer/composables/{useTheme,useSidebar,useSidePanel,useSession,useMessages,useModel}.ts` | 🆕 6 文件 |
| `src/renderer/services/{mock-agent,mock-data,i18n}.ts` | 🆕 |
| `src/renderer/styles/{theme,reset}.css` | 🆕 |
| `src/shared/darvin-api.ts` | 🆕 |
| `src/preload/index.ts` | 🆕（S1 mock 阶段） |
| `AGENTS.md` | 🆕 改：加 Vue 3 组件化规范 + 颜色 token 规则 |

**不修改**：
- `src/main/index.ts`、`src/main/runtime/manager.ts`、`src/main/runtime/client.ts`、`forge.config.ts`、`tsconfig.json`
- `scripts/build-go.js`（S5 才会通过这条路径验证 Go 二进制）

---

## 7. 验收标准

### 7.1 启动

- [ ] `npm install` 成功
- [ ] `npm run lint` 通过
- [ ] `npm start` 启动 Electron，主窗口显示 3 栏布局（240px + flex + 320px），**不**显示 "Hello World!"
- [ ] 默认深色主题（背景 `#0e0e10`）
- [ ] sidebar 顶部有 Logo + "Darvin" 文字 + "+ New chat" 按钮
- [ ] sidebar 中部 mock 3 条会话，第 1 条 active 高亮
- [ ] sidebar 底部 runtime status badge "ready" + 主题按钮 + 设置按钮
- [ ] 中间 ChatHeader：hamburger 按钮 + 标题 + 模型 badge "claude-sonnet-4-5" + 折叠右栏按钮
- [ ] 中间 MessageList 空态："Send a message to start the conversation."
- [ ] 中间 Composer：textarea + send 按钮
- [ ] 右侧 SidePanel：3 个 tab (Tools / Thinking / Artifact) + 空态文案

### 7.2 交互

- [ ] 输入 "ping" → Enter 或 send → user 消息右侧出现 + assistant 左侧逐字符 "Pong..."
- [ ] 流式带 `▍` 光标，done 后光标消失
- [ ] 流式期间 send 按钮 disabled + placeholder 改 "Agent is busy..."
- [ ] DevTools console `window.darvin` 看到 6 个方法：`prompt / abort / onEvent / listSessions / getMessages / status`
- [ ] DevTools console `await window.darvin.listSessions()` 返回 3 条 mock
- [ ] DevTools console `await window.darvin.status()` 返回 `"online"`
- [ ] 主题切换：toggle 按钮 → 整个 UI 浅色 → 按钮 icon sun 变 moon
- [ ] 主题切换持久化：刷新页面 / 重启 Electron，浅色保留
- [ ] 折叠 sidebar：hamburger → sidebar 消失，chat 占空间
- [ ] 折叠 sidebar 持久化：刷新页面 sidebar 保持收起
- [ ] 折叠 side panel：右栏按钮 → side panel 消失
- [ ] 折叠 side panel 持久化：刷新页面保持
- [ ] 模型 dropdown：点击 badge → 下拉 3 个选项 → 选中 → badge 更新 + dropdown 关闭
- [ ] 模型选择持久化：localStorage 写入
- [ ] 切换会话：点 SessionItem #2 → chat 标题变 + 消息列表清空 + 加载 mock messages
- [ ] 新建会话：+ New chat → 新会话 ID + composer 焦点 + 消息列表空
- [ ] error event：mock 注入 error → 气泡变红边框 + 错误文字 + busy=false

### 7.3 样式合规

- [ ] 组件**无** `<style>` 块（除 `styles/reset.css`）
- [ ] 组件**无**裸 CSS 属性（`style="..."`）
- [ ] grep `bg-\[` / `text-\[` / `color: #` 在 `src/renderer/components/` 下 = 0（不允许 magic value）
- [ ] 所有颜色通过 `bg-bg` / `bg-surface` / `text-text-muted` 等 utility class，使用 `var(--color-*)`
- [ ] `package.json` 不含 `lucide-vue-next` / `@heroicons/vue` 等图标库
- [ ] 所有 icon 通过 `<Icon name="..." />` 组件
- [ ] 字体：text 类走 `text-xs` / `text-sm` / `text-base` 等 utility（来自 token）

### 7.4 TypeScript / build

- [ ] `tsconfig.json` 不用改（vue 文件由 vite 处理；composables 走 strict）
- [ ] `npm run build`（electron-forge）成功
- [ ] `npm run package` 产出 `out/` 含正确结构

### 7.5 文档

- [ ] `AGENTS.md` 增加 §Vue 3 组件化规范（10 条）
- [ ] `AGENTS.md` 增加 §样式 / 颜色 token 规则（包括 dark / light 双覆盖）
- [ ] `README.md` 已有"Vite 启动"段落仍适用

---

## 8. 后续 spec 候选（不在本 spec 范围）

| Spec | 内容 |
|------|------|
| **S5** electron-runtime-client | preload 替换为真 IPC + main 侧 RuntimeMgr + AgentClient |
| **S6** agent-e2e-integration | 三层接通、session 持久化、优雅关闭 |
| Pinia 状态管理 | 多会话切换 + 跨组件大量状态时引入 |
| Vue Router | 多视图（设置 / 历史） |
| i18n 切换 UI | v0 中英混用；UI 完整 i18n 留 spec |
| Artifact 渲染器 | sandboxed iframe 接入 side panel Artifact tab |
| Markdown 渲染 | assistant content 走 markdown（v0 纯文本） |
| Code highlight | `<pre><code>` 片段 |
| Tool calls 渲染 | `<MessageItem>` 折叠 sub-component |
| Thinking 渲染 | `thinking_delta` 折叠 / 详展 |
| Dropdown 通用化 | 多个 dropdown 共用 a11y primitive |
| Mobile 适配 | < 1200px 折叠为单栏 |
| Accessibility 完整 | 焦点顺序、屏幕阅读器、ARIA 标注 |
