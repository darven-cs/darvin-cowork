# Agent UI Shell 设计文档（S1）

> **Phase 1 / 6 — UI 阶段**。**不接后端**，仅把 Vue3 + Tailwind v4 接入、UI shell 跑起来、contextBridge API 契约锁死。后端接入由 S5 完成。
> 前置：仓库 `src/renderer/` 当前是 vanilla TS + HTML "Hello World!"（`AGENTS.md` §项目概览），需整体迁到 Vue3 + Tailwind v4 目标栈。

---

## 1. 概述

### 1.1 问题 / 背景

当前 renderer / preload / RuntimeMgr 三处的状态：

| 位置 | 现状 | 期望 |
|------|------|------|
| `src/renderer/index.html` | `<h1>💖 Hello World!</h1>` | 极简聊天界面 |
| `src/renderer/index.ts` | `console.log("👋 This message...")` | `createApp(App).mount('#app')` |
| `src/renderer/index.css` | 一段 `body { ... }` | `@import "tailwindcss";` |
| `src/preload/index.ts` | 仅一段注释 | contextBridge 暴露 `window.darvin` |
| `src/main/runtime/client.ts` | `throw new Error('createAgentClient 未实现...')` | S5 才动 |
| `src/main/runtime/manager.ts` | `resolveAgentBinaryPath()` 已实装，`startAgentRuntime()` 是占位 | S5 才动 |

**S5 接后端之前必须先把 contextBridge API 契约锁死**，否则后端协议与前端期待对不上。本 spec 只做 UI shell + 契约，**mock 流式事件验证渲染**。

### 1.2 目标

- Vue3 + Tailwind v4 接入（按 `AGENTS.md` §前端栈）
- `App.vue`：textarea + send 按钮 + 消息列表（流式追加）
- `src/shared/darvin-api.ts`：API 类型锁死（`DarvinEvent` 7 类 union、`DarvinPromptRequest/Response`、`DarvinAbortRequest`）
- `src/preload/index.ts`：contextBridge 暴露 `window.darvin.{prompt, abort, onEvent}` 三方法
- S1 期间 mock 流式事件，验证渲染逻辑（不依赖 Go 子进程）

### 1.3 非目标

- **不**接 Go agent 子进程（运行时是 mock；S5 才接）
- **不**接 sessions.db / GORM（那是 S2/S3）
- **不**做 Pinia / Vue Router / i18n（按需后续 spec）
- **不**做 Tailwind 主题定制（默认配色）
- **不**改 `src/main/runtime/manager.ts` / `client.ts`（S5 才动）
- **不**改 `src/main/index.ts`（保留 DevTools 等已有逻辑；S5 才改）
- **不**引入 Vitest / Vue Test Utils（仓库未配置 test runner，`AGENTS.md` §测试）

---

## 2. 用户场景

### 场景 1：启动 Electron 看到 UI

**Given** 仓库当前 renderer 是 Hello World 占位
**When** `npm start` 启动 Electron
**Then** DevTools 主窗口显示 Vue3 + Tailwind v4 渲染的聊天界面（顶部 header、消息列表区域、底部 textarea + send 按钮），**不再**显示 "Hello World!"

### 场景 2：输入并发送（mock 流式回复）

**Given** UI 已渲染，消息列表为空，输入框为空
**When** 用户输入 "ping" 点击 send
**Then** 消息列表追加一条 user 消息（右侧）；mock 流式 assistant 消息（左侧）逐字符出现 "Pong. Agent runtime is ready."，每 50ms 一个字符；结束符 done 后定型

### 场景 3：contextBridge API 契约可见

**Given** Electron DevTools 打开
**When** 在 console 输入 `window.darvin`
**Then** 看到 `{ prompt: [Function], abort: [Function], onEvent: [Function] }`
**And** `await window.darvin.prompt({ content: 'test' })` 返回 `{ sessionId: 'mock-session', messageId: 'mock-msg' }`（mock 实现）

### 场景 4：流式回复期间禁用 send

**Given** mock 流式回复正在进行中（assistant 消息未 done）
**When** 用户在 textarea 输入字符 / 点击 send
**Then** send 按钮 disabled，textarea 显示 "Agent is busy..." 提示

---

## 3. 功能需求

### FR-1：Vue3 + Tailwind v4 接入

- `package.json` `dependencies` 加 `vue ^3.5`、`@tailwindcss/vite ^4.0`
- 仓库当前 `vite.renderer.config.ts` 不存在（推断；如存在则加 `@tailwindcss/vite` 插件）
- `src/renderer/index.css` 第一行加 `@import "tailwindcss";`
- `src/renderer/index.ts` 改成：
  ```ts
  import { createApp } from 'vue';
  import App from './App.vue';
  import './index.css';
  createApp(App).mount('#app');
  ```
- `src/renderer/index.html` 不改

### FR-2：App.vue 极简聊天界面

布局（Tailwind utility class）：

```
┌─────────────────────────────────┐
│ Darvin Cowork · dev             │  ChatHeader
├─────────────────────────────────┤
│                                 │
│ [user]    ping                  │
│ [assistant] Pong. Agent...      │  MessageList
│                                 │   (滚动到底)
│                                 │
├─────────────────────────────────┤
│ [textarea...]      [Send ▶]     │  InputBar
└─────────────────────────────────┘
```

组件结构：
```
App.vue
├── ChatHeader.vue          (env 标识：dev/prod)
├── MessageList.vue         (持有 messages: Message[] ref)
│   └── MessageItem.vue     (props: role, deltas, done)
│       └── StreamingText.vue (props: deltas, done)
└── InputBar.vue            (emits 'send')
```

- assistant 消息支持流式追加：deltas 数组逐个 push，最终 done=true 时定型
- 滚动到底部自动跟随（`nextTick` + `scrollIntoView`）
- 流式期间 send 按钮 disabled + 输入框 placeholder 改为 "Agent is busy..."

### FR-3：StreamingText 子组件

接收 `{ deltas: string[], done: boolean }`，渲染累积文本。简单实现：

```vue
<template>
  <span>{{ content }}<span v-if="!done" class="animate-pulse">▍</span></span>
</template>
<script setup lang="ts">
import { computed } from 'vue';
const props = defineProps<{ deltas: string[]; done: boolean }>();
const content = computed(() => props.deltas.join(''));
</script>
```

### FR-4：Mock 流式事件

`src/renderer/services/mock-agent.ts`：

```ts
import type { DarvinEvent } from '../../shared/darvin-api';

export async function mockPrompt(content: string): Promise<{
  sessionId: string;
  messageId: string;
  events: AsyncIterable<DarvinEvent>;
}> {
  const sessionId = `mock-${Date.now()}`;
  const messageId = `mock-msg-${Date.now()}`;
  const reply = simulateReply(content);
  return {
    sessionId,
    messageId,
    events: makeStream(reply, sessionId, messageId),
  };
}

async function* makeStream(text: string, sessionId: string, messageId: string): AsyncIterable<DarvinEvent> {
  for (const char of text) {
    await delay(50);
    yield { type: 'text_delta', delta: char, sessionId, messageId };
  }
  yield {
    type: 'done', sessionId, messageId,
    usage: { promptTokens: text.length, completionTokens: text.length, totalTokens: text.length * 2 },
  };
  yield { type: 'agent_end', sessionId };
}

function simulateReply(input: string): string {
  if (input.toLowerCase() === 'ping') return 'Pong. Agent runtime is ready.';
  return `Echo: ${input.slice(0, 80)}${input.length > 80 ? '...' : ''}`;
}

const delay = (ms: number) => new Promise((r) => setTimeout(r, ms));
```

### FR-5：contextBridge API 契约锁死

`src/preload/index.ts`（**S1 期间 mock 走法**，S5 替换为真 IPC）：

```ts
import { contextBridge } from 'electron';
import type {
  DarvinPromptRequest,
  DarvinPromptResponse,
  DarvinAbortResponse,
  DarvinEvent,
} from '../shared/darvin-api';
import { mockPrompt } from '../renderer/services/mock-agent';

const api = {
  async prompt(req: DarvinPromptRequest): Promise<DarvinPromptResponse> {
    // S1 mock：同步返回 sessionId/messageId，events 通过 onEvent 推
    const r = await mockPrompt(req.content);
    // 在 mock 阶段，事件通过 IPC 推回 renderer（虽然 S1 还没真 IPC；这里改为直接派发）
    // S5 替换为 ipcRenderer.invoke('darvin:prompt', req)
    return { sessionId: r.sessionId, messageId: r.messageId };
  },
  async abort(sessionId: string): Promise<DarvinAbortResponse> {
    // S1 mock：no-op
    return { aborted: true, sessionId };
  },
  onEvent(handler: (e: DarvinEvent) => void): () => void {
    // S1 mock：直接订阅 mock 流（绕开 IPC）
    // S5 替换为 ipcRenderer.on('darvin:event', wrapped)
    const subs: Array<() => void> = [];
    const origMockPrompt = mockPrompt;
    // ... 实际写法见 §4.2.1
    return () => subs.forEach((u) => u());
  },
};

contextBridge.exposeInMainWorld('darvin', api);
```

S5 替换后的 `preload/index.ts`（写在这里作为契约目标）：

```ts
const api = {
  prompt(req: DarvinPromptRequest): Promise<DarvinPromptResponse> {
    return ipcRenderer.invoke('darvin:prompt', req);
  },
  abort(sessionId: string): Promise<DarvinAbortResponse> {
    return ipcRenderer.invoke('darvin:abort', { sessionId });
  },
  onEvent(handler: (e: DarvinEvent) => void): () => void {
    const wrapped = (_: unknown, e: DarvinEvent) => handler(e);
    ipcRenderer.on('darvin:event', wrapped);
    return () => { ipcRenderer.off('darvin:event', wrapped); };
  },
};
contextBridge.exposeInMainWorld('darvin', api);
```

### FR-6：类型定义 `src/shared/darvin-api.ts`

```ts
// Req / Res
export interface DarvinPromptRequest {
  content: string;
  sessionId?: string; // 可选：未指定则开新会话（S2/S3 实现 SessionManager 后此字段生效）
}

export interface DarvinPromptResponse {
  sessionId: string;
  messageId: string;
}

export interface DarvinAbortResponse {
  aborted: boolean;
  sessionId: string;
}

// 事件 union（与 Go 侧 event.Event 7 类对应，详见 S3 §4.2 映射表）
export type DarvinEvent =
  | DarvinTextDeltaEvent
  | DarvinToolStartEvent
  | DarvinToolEndEvent
  | DarvinThinkingDeltaEvent
  | DarvinDoneEvent
  | DarvinErrorEvent
  | DarvinAgentEndEvent;

export interface DarvinTextDeltaEvent {
  type: 'text_delta';
  delta: string;
  sessionId: string;
  messageId: string;
}

export interface DarvinToolStartEvent {
  type: 'tool_start';
  callId: string;
  name: string;
  arguments: Record<string, unknown>;
  sessionId: string;
  messageId: string;
}

export interface DarvinToolEndEvent {
  type: 'tool_end';
  callId: string;
  result: { content: string; isError: boolean };
  durationMs: number;
  sessionId: string;
  messageId: string;
}

export interface DarvinThinkingDeltaEvent {
  type: 'thinking_delta';
  delta: string;
  sessionId: string;
  messageId: string;
}

export interface DarvinDoneEvent {
  type: 'done';
  sessionId: string;
  messageId: string;
  usage: { promptTokens: number; completionTokens: number; totalTokens: number };
}

export interface DarvinErrorEvent {
  type: 'error';
  sessionId: string;
  messageId: string;
  message: string;
}

export interface DarvinAgentEndEvent {
  type: 'agent_end';
  sessionId: string;
}
```

S5 + Go 侧按此 schema 实现；S2-S4 阶段 Go 产出的 `event.Event` 子类型映射规则见 S3 §4.2。

### FR-7：window.darvin 全局声明

`src/renderer/darvin.d.ts`：

```ts
import type {
  DarvinPromptRequest,
  DarvinPromptResponse,
  DarvinAbortResponse,
  DarvinEvent,
} from '../shared/darvin-api';

declare global {
  interface Window {
    darvin: {
      prompt(req: DarvinPromptRequest): Promise<DarvinPromptResponse>;
      abort(sessionId: string): Promise<DarvinAbortResponse>;
      onEvent(handler: (e: DarvinEvent) => void): () => void;
    };
  }
}
export {};
```

---

## 4. 实现方案

### 4.1 目录结构（v1 增量）

```
src/
├── renderer/
│   ├── index.html            # 不改
│   ├── index.ts              # 改：createApp(App).mount('#app')
│   ├── index.css             # 改：@import "tailwindcss";
│   ├── App.vue               # 🆕
│   ├── darvin.d.ts           # 🆕
│   ├── components/
│   │   ├── ChatHeader.vue    # 🆕
│   │   ├── MessageList.vue   # 🆕
│   │   ├── MessageItem.vue   # 🆕
│   │   ├── InputBar.vue      # 🆕
│   │   └── StreamingText.vue # 🆕
│   └── services/
│       └── mock-agent.ts     # 🆕
├── preload/
│   └── index.ts              # 🆕（S1 mock 阶段；S5 替换为真 IPC）
├── shared/
│   └── darvin-api.ts         # 🆕
└── main/                     # 不动（S5 才动）
    ├── index.ts
    └── runtime/{manager,client}.ts
```

### 4.2 关键设计决策

#### 4.2.1 S1 mock 阶段：events 怎么回到 renderer

S1 没有 IPC，`onEvent(handler)` 需要直接订阅 mock 流。两种走法：

**走法 A（推荐）**：`onEvent` 在 preload 内部维护 mock 流的订阅句柄，但 mock 流由 `prompt()` 触发 — 这种情况下 preload 必须持有 prompt 调用的上下文。最简单的办法：

```ts
// preload/index.ts (S1 期间)
const api = {
  async prompt(req: DarvinPromptRequest): Promise<DarvinPromptResponse> {
    const r = await mockPrompt(req.content);
    // 把 events 推到一个全局 EventTarget
    eventTarget.dispatchEvent({ detail: ... });
    return { sessionId: r.sessionId, messageId: r.messageId };
  },
  onEvent(handler) {
    const sub = (ev: any) => handler(ev.detail);
    eventTarget.addEventListener('mock-event', sub);
    return () => eventTarget.removeEventListener('mock-event', sub);
  },
  ...
};
```

**走法 B**：`prompt()` 返回 events 句柄，App.vue 在 prompt 之后手动订阅：

```ts
// App.vue
const handle = await window.darvin.prompt(content);
const events = mockEvents.get(handle.messageId); // 不优雅
for await (const e of events) { ... }
```

走法 A 更接近 S5 真实形态（事件推到 renderer），保留 S1 mock 与 S5 真接的接口一致。**S1 用走法 A**。

#### 4.2.2 流式渲染数据流

```
mock-agent.ts makeStream()
  ↓ AsyncIterable<DarvinEvent>
preload/index.ts eventTarget.dispatchEvent({ detail: event })
  ↓
App.vue mounted() → window.darvin.onEvent(handler)
  ↓ handler 入参 = DarvinEvent
MessageList.vue Map<messageId, { deltas, done, role }>
  ↓ reactive update
MessageItem.vue :deltas="..." :done="..."
  ↓
StreamingText.vue 累积 deltas.join('') + ▍
```

#### 4.2.3 不引入 Pinia 的理由

S1 阶段只有一个 MessageList state，ref + provide/inject 足够。Pinia 在多会话切换时再引入（S6+）。

#### 4.2.4 不引入 Vue Router 的理由

S1 只有单页面（聊天界面）。多视图（设置页 / 历史会话列表页）留到 S6+。

### 4.3 关键代码骨架

```vue
<!-- App.vue -->
<template>
  <div class="flex flex-col h-screen">
    <ChatHeader :env="env" />
    <MessageList ref="listRef" />
    <InputBar @send="handleSend" :busy="busy" />
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue';
import ChatHeader from './components/ChatHeader.vue';
import MessageList from './components/MessageList.vue';
import InputBar from './components/InputBar.vue';

const env = import.meta.env.DEV ? 'dev' : 'prod';
const listRef = ref<InstanceType<typeof MessageList> | null>(null);
const busy = ref(false);

onMounted(() => {
  window.darvin.onEvent((e) => {
    listRef.value?.handleEvent(e);
    if (e.type === 'done' || e.type === 'error' || e.type === 'agent_end') {
      busy.value = false;
    }
  });
});

async function handleSend(content: string) {
  busy.value = true;
  listRef.value?.appendUserMessage(content);
  const { sessionId, messageId } = await window.darvin.prompt({ content });
  listRef.value?.startAssistantMessage(sessionId, messageId);
}
</script>
```

---

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| 流式回复中再点 send | send 按钮 disabled；textarea placeholder 改 "Agent is busy..." |
| 流式回复中关闭 Electron | mock 进程结束即丢弃；S5 接 IPC 后由 S6 接住优雅关闭 |
| mock 流式事件报 ErrorEvent | `MessageItem.vue` 显示错误样式（红色 border + "Agent 错误"） |
| 输入框空时点 send | send 按钮 disabled |
| 极长输入（>10k 字符） | S1 不限制；S5 在 main 侧 AgentClient 层加 maxLength 校验 |
| 消息列表超过 1000 条 | S1 不分页；S6+ 引入虚拟滚动 |
| window.darvin 未注入 | 不可能（S1 始终跑在 Electron 中）；runtime 防御 null 检查 |
| mock 流式 stream 内部 throw | EventTarget dispatch error → handler 抛 → App.vue catch → 显示系统消息 |

---

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `package.json` | 加 `vue ^3.5`、`@tailwindcss/vite ^4.0` |
| `vite.renderer.config.ts` | 加 `@tailwindcss/vite` 插件（若不存在则创建） |
| `src/renderer/index.ts` | 改为 `createApp(App).mount('#app')` |
| `src/renderer/index.css` | 第一行加 `@import "tailwindcss";` |
| `src/renderer/App.vue` | 🆕 |
| `src/renderer/darvin.d.ts` | 🆕 |
| `src/renderer/components/ChatHeader.vue` | 🆕 |
| `src/renderer/components/MessageList.vue` | 🆕 |
| `src/renderer/components/MessageItem.vue` | 🆕 |
| `src/renderer/components/InputBar.vue` | 🆕 |
| `src/renderer/components/StreamingText.vue` | 🆕 |
| `src/renderer/services/mock-agent.ts` | 🆕 |
| `src/preload/index.ts` | 🆕（S1 mock 阶段） |
| `src/shared/darvin-api.ts` | 🆕 |

**不修改**：`src/main/index.ts`、`src/main/runtime/manager.ts`、`src/main/runtime/client.ts`、`forge.config.ts`、`tsconfig.json`（不需调整；vue 文件由 vite 处理）。

---

## 7. 验收标准

- [ ] `cd src/darvin-agent/..  && npm install` 成功，`package.json` 含 `vue ^3.5`、`@tailwindcss/vite ^4.0`
- [ ] `npm run lint` 干净
- [ ] `npm start` 启动 Electron，DevTools 主窗口显示 Vue3 + Tailwind v4 聊天界面，**不再**显示 "Hello World!"
- [ ] 顶部 header 含 "Darvin Cowork" + `dev` 标识
- [ ] 输入 "ping" 点击 send → user 消息追加右侧 → assistant 消息左侧逐字符出现 "Pong. Agent runtime is ready."
- [ ] DevTools console 输入 `window.darvin` → 看到 `{ prompt, abort, onEvent }` 3 个方法
- [ ] `await window.darvin.prompt({ content: 'test' })` 返回 `{ sessionId, messageId }`
- [ ] 输入框空时 send 按钮 disabled
- [ ] 流式回复期间 send 按钮 disabled + placeholder "Agent is busy..."
- [ ] 关闭 Electron 不报错（DevTools 关闭即进程退出）
- [ ] `src/shared/darvin-api.ts` 类型能被 `App.vue`、`MessageItem.vue`、`preload/index.ts` 引用（TS 编译过）

---

## 8. 后续 spec 候选（不在本 spec 范围）

| Spec | 内容 |
|------|------|
| **S5** electron-runtime-client | preload 替换为真 IPC + main 侧 RuntimeMgr + AgentClient |
| **S6** agent-e2e-integration | 三层接通、session 持久化、优雅关闭 |
| Pinia 状态管理 | 多会话切换时引入，替换 S1 的 ref 散落模式 |
| Vue Router | 多视图（设置 / 历史） |
| i18n | 中英双语 |
| Artifact 渲染器 | AGENTS.md §项目概览 提到的 sandboxed iframe |
| Vitest | 当前无 test runner，本 spec 不引入 |