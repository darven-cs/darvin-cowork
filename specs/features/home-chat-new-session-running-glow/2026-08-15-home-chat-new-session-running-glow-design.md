# 首页聊天新建会话 + 输入框运行态环形光晕 设计文档

## 1. 概述

### 1.1 问题 / 背景

1. **首页聊天会续接旧会话**：首页的聊天框由两部分组成——底部输入框（`PromptDock`）和 4 张快捷卡（`QuickActions`，做 PPT / 看数据 / 写文档 / 搜网页）。二者共用 `HomeView.onSend` → `useChatActions.send`。当前逻辑（`useChatActions.ts:52-57`）：

   ```ts
   let sessId = session.activeSessionId.value;
   if (session.draftMode.value || sessId === null) {
     const created = await session.createSession(...); // 只有 draft 态 / 无会话才新建
     sessId = created.id;
   }
   ```

   也就是说，只要已经存在一个 active session，首页发的消息会**混进旧会话**继续对话，而不是开启一段新对话。用户期望：首页发起 = 全新会话，绝不续接。

2. **Agent 运行时聊天框没有运行指示**：`busy` 只在 `window.darvin.prompt` 的 RPC 往返瞬间为 `true`（Go 端 `handlePrompt` 是 `Loop.Submit` 拿 `RunTicket` 立刻返回，run 在 goroutine 里异步流式），覆盖不了整个回答过程。真正代表"正在运行"的响应式信号是 `useMessages` 的 `streamingSessionIds`（`text_delta` / `thinking_delta` / `tool_start` / `tool_end` → 加入；`done` / `error` / `agent_end` → 移除），侧栏 `SessionList.vue` 已经用它驱动 running 状态点，但聊天输入框本身没有任何视觉反馈。

### 1.2 目标

- 从首页（输入框或快捷卡）发起的任何聊天，一律**新建一个 session** 进行对话，之后照常切到 Chat 视图；原 active session 不被追加消息。
- Agent 运行 / 回答期间，聊天输入框（Composer / PromptDock 容器）以**主题色环形光晕**高亮并脉冲，让用户一眼知道"正在回答"；run 结束光晕消失。

### 1.3 非目标

- 不改 ChatView 内发消息行为：在 Chat 视图里发送仍续接当前 active session。
- 不改 Go / main 端 session 归属与数据所有权（纯 renderer 行为变更）。
- 不把 Composer 的 `busy`（禁用输入）扩展成覆盖整个 run——只新增运行指示视觉，`busy` 语义保持现状。
- **不改变首页发送后跳转 Chat 视图的行为**（已确认：保持现状，光晕的真实可见位置在 Chat 视图的 Composer；首页快捷卡在运行期间不可见、不做光晕包装）。
- 不对首页快捷卡做视觉分组 / 合并布局（跳转是瞬时的，无可见收益）。

## 2. 用户场景

### 场景 1: 首页输入框开启新对话
**Given** 已经有一个 active session（历史对话，侧栏高亮）
**When** 用户在首页底部输入框输入消息并回车发送
**Then** 系统新建一个 session（首条消息截断 30 字符作为标题）、设为 active、切到 Chat 视图展示新会话；原 active session 没有任何新增消息

### 场景 2: 首页快捷卡开启新对话
**Given** 同场景 1
**When** 用户点击任意快捷卡（如「做 PPT」）
**Then** 行为与场景 1 一致：新建 session + 切到 Chat 视图

### 场景 3: Agent 运行时输入框环形光晕
**Given** 用户在 Chat 视图发送消息，切到新会话
**When** agent 开始运行（收到 `text_delta` / `tool_start` 等流事件）
**Then** 输入框容器（Composer 外围）出现主题色（`--color-primary`）环形光晕并柔和脉冲；`done` / `error` / `agent_end` 到达后光晕消失

### 场景 4: 首页输入框同样具备运行光晕
**Given** 用户在首页发送后仍短暂停留在首页，或后台 session 运行期间回到首页
**When** active session 处于 streaming 状态
**Then** 首页输入框（PromptDock）外围同样显示主题色光晕

## 3. 功能需求

### FR-1: Home 发消息强制新建 session
- `useChatActions.send` 增加可选参数 `opts?: { newSession?: boolean }`；`HomeView.onSend` 调用时传 `newSession: true`。
- `send` 内条件扩展为：`opts?.newSession || session.draftMode.value || sessId === null` 时 `createSession(首条消息前 30 字符)`，并清 draftMode、设 active。

### FR-2: 聊天框运行态环形光晕
- `Composer`（Chat 视图）与 `PromptDock`（首页）新增可选 prop `running?: boolean`；`running === true` 时给容器 div 追加 `composer-running` 类——**环形** box-shadow 光晕 + 脉冲动画，包住输入框外围。
- ChatView / HomeView 各自用 `messages.streamingSessionIds.value.has(activeSessionId)` 计算 `running` 并传给输入框组件。
- 首页快捷卡不做光晕包装（跳转瞬时，无可见收益，见非目标）。

### FR-3: 主题色 token 与动画（遵循既有样式纪律）
- 在 `theme.css`（非组件级 CSS 的唯一合法位置，与 `.qa-tile` / `.markdown-content` 同列）新增 `.composer-running` 规则 + 光晕 keyframes。
- 颜色一律引用 `var(--color-primary)`，不写死 magic value；光晕透明度用 `color-mix(in srgb, ...)`，使深色模式与 `data-accent` 蓝 / 绿主题自动跟随。

## 4. 实现方案

### 4.1 `useChatActions.send` 签名扩展

`src/renderer/composables/useChatActions.ts`：

```ts
async function send(
  content: string,
  busyRef: { value: boolean },
  opts?: { newSession?: boolean },
): Promise<void> {
  ...
  let sessId = session.activeSessionId.value;
  if (opts?.newSession || session.draftMode.value || sessId === null) {
    const created = await session.createSession(routeContent.trim().slice(0, 30));
    sessId = created.id;
    session.draftMode.value = false;
  }
  ...
}
```

`HomeView.onSend`：

```ts
async function onSend(content: string) {
  await chatActions.send(content, busy, { newSession: true });
  viewMode.goChat();
}
```

（快捷卡走 `onSend(t(TEMPLATE_KEYS[id]))`，同一路径，天然覆盖。）

### 4.2 running 派生 + prop 传递

ChatView 与 HomeView 同款派生：

```ts
const messages = useMessages();
const session = useSession();
const running = computed(() =>
  messages.streamingSessionIds.value.has(session.activeSessionId.value ?? ''),
);
```

模板：`<Composer :busy="busy" :running="running" @send="handleSend" />` / `<PromptDock :busy="busy" :running="running" @send="onSend" />`

两输入框组件 props：

```ts
defineProps<{ busy: boolean; running?: boolean }>();
```

容器 class 绑定（Composer.vue 第 32-34 行 / PromptDock.vue 第 3-5 行的 `div`）：

```vue
<div
  class="rounded-xl border border-border bg-surface-2 transition-colors focus-within:border-border-strong"
  :class="{ 'composer-running': running }"
>
```

### 4.3 光晕样式（theme.css 末尾）

```css
.composer-running {
  border-color: color-mix(in srgb, var(--color-primary) 45%, transparent);
  animation: composer-glow 1.6s ease-in-out infinite;
}
@keyframes composer-glow {
  0%, 100% {
    box-shadow: 0 0 0 1px color-mix(in srgb, var(--color-primary) 30%, transparent),
                0 0 14px  color-mix(in srgb, var(--color-primary) 40%, transparent);
  }
  50% {
    box-shadow: 0 0 0 1px color-mix(in srgb, var(--color-primary) 55%, transparent),
                0 0 28px  color-mix(in srgb, var(--color-primary) 60%, transparent);
  }
}
```

- 环形光环：`box-shadow` 沿圆角矩形边框外圈扩散，即"环形高亮"。
- 脉冲：0%→50% 光晕半径 / 亮度增强再回落到 100%，1.6s 循环。
- 跟随主题：只用 `--color-primary`，orange（默认）/ blue / green 与 dark 全覆盖自动生效。

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| 首页发送时没有 active session | 本就会新建，行为不变 |
| 运行中继续发消息（同 session 排队 turn） | `streamingSessionIds` 仍在集合中，光晕保持到该 session 最后一轮 `agent_end` |
| 多 session 并发运行 | 按 `activeSessionId` 过滤，只有当前活动会话的输入框发光 |
| 首页发送后立即跳 Chat | Home 的 PromptDock 光晕几乎不可见（瞬时）；真实可见效果落在 Chat 视图 Composer，符合已确认的"保持跳转"决定 |
| 后台 session 运行期间回到首页 | `activeSessionId` 仍指向运行中的会话 → 首页输入框同样显示光晕（场景 4） |
| agent 离线 / 无事件 | `streamingSessionIds` 为空，无光晕 |
| 深色模式 / accent 蓝 / accent 绿 | 光晕用 `var(--color-primary)`，自动适配 |
| 错误 run（RPC error / error 事件） | `error` 事件从 `streamingSessionIds` 移除 → 光晕消失，与侧栏状态一致 |

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/renderer/composables/useChatActions.ts` | `send` 增加 `opts?: { newSession?: boolean }`；新建条件纳入该 flag |
| `src/renderer/views/HomeView.vue` | `onSend` 传 `newSession: true`；计算 `running` 传给 PromptDock |
| `src/renderer/views/ChatView.vue` | 计算 `running` 传给 Composer |
| `src/renderer/components/home/PromptDock.vue` | 增加 `running` prop + 容器 `composer-running` class |
| `src/renderer/components/chat/Composer.vue` | 增加 `running` prop + 容器 `composer-running` class |
| `src/renderer/styles/theme.css` | 末尾新增 `.composer-running` + `composer-glow` keyframes |

## 7. 验收标准

- [ ] 场景 1 / 2：存在 active session 时，从首页输入框或快捷卡发消息 → 新建 session、切 Chat、原会话零新增
- [ ] 新建 session 标题为首条消息前 30 字符
- [ ] 场景 3：Chat 视图发送后 Composer 出现主题色环形光晕并脉冲；`done` / `error` 后消失
- [ ] 场景 4：后台 session 运行期间回到首页，PromptDock 同样显示光晕
- [ ] 深色模式 + accent 蓝 / 绿下光晕颜色正确跟随主题
- [ ] `npm run lint` 通过
- [ ] `npm run test` 通过
- [ ] 手动 `npm start`：首页发消息走新会话；回答期间输入框光晕可见、结束时消失；DevTools 无 console error
