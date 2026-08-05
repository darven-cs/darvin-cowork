# 切换会话滚动到底部设计文档

## 1. 概述

### 1.1 问题

点击侧栏会话进入对话时，视图停留在对话顶部（从第一条 chat 开始），没有直接拉到最新消息（底部）。

### 1.2 根因

`MessageList.vue` 只在两类变化时滚动到底部：

- `turns.value.length` 变化（`behavior: 'smooth'`）
- 内容签名变化（`behavior: 'auto'`）

切换会话时，`switchSession` 先 `await` IPC 再更新 `activeSessionId`，随后 `loadMessages` 异步填充新 bucket。当新旧会话 turn 数相同（`turns.length` watch 不触发）或时序让 length / 签名 watch 未正确触发时，滚动不发生或发生时内容尚未就位，视图停在顶部。

### 1.3 目标

- 切换会话后，视图始终落到底部（最新消息）。
- 切换瞬间用瞬时滚动（`behavior: 'auto'`），不播放平滑动画（符合「直接拉到底部」）。

## 2. 用户场景

### 场景 1: 切换到长会话
**Given** 当前在会话 A（5 条消息），侧栏点击会话 B（50 条消息，未加载过）
**When** 切换完成
**Then** 视图直接落到 B 的最后一条消息

### 场景 2: 切到等长会话
**Given** 会话 A 与 B 都是 5 条消息（B 已缓存）
**When** 从 A 切到 B
**Then** 视图落到 B 底部（此时 `turns.length` 不变，靠新增的 `activeSessionId` watch 兜底）

### 场景 3: 切到空会话
**Given** 点击一个无消息的新会话
**When** 切换完成
**Then** 显示空态；对空容器 scrollTo 为 no-op，无异常

## 3. 功能需求

### FR-1: activeSessionId 变化时滚动到底部
- `MessageList.vue` 引入 `useSession`，watch `activeSessionId`，`nextTick` 后 `scrollTo({ top: el.scrollHeight, behavior: 'auto' })`。

### FR-2: 保留既有流式 / 内容变化滚动
- 现有两个 watch 不动（流式追加时仍自动跟随底部）。

## 4. 实现方案

`src/renderer/components/chat/MessageList.vue`：

```ts
import { useSession } from '../../composables/useSession';

const session = useSession();

watch(
  () => session.activeSessionId.value,
  async () => {
    await nextTick();
    const el = scrollRef.value;
    if (el) el.scrollTo({ top: el.scrollHeight, behavior: 'auto' });
  },
);
```

覆盖逻辑：

- **全新加载**（bucket 空 → 有内容）：此 watch 对空容器滚动为 no-op；随后 `loadMessages` 填充、turn length 0→N 触发既有 smooth watch，最终到达底部。
- **缓存等长会话**（length 不变）：此 watch 兜底，直接落到底部。
- **缓存不等长会话**：length watch 与新增 watch 都会触发，均落底。

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| 切换到空会话 | scrollTo 空容器为 no-op，正常显示空态 |
| 流式中的旧会话切走 | 事件仍写旧 bucket；新会话 view 正常落底 |
| 快速连点多个会话 | 依赖 Vue watch flush 顺序；最终 `activeSessionId` 对应会话落底 |
| 用户手动滚到顶部读历史 | 仅新消息 / 内容变化或切会话才触发滚动（与现状一致，不在本次范围内收窄） |

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/renderer/components/chat/MessageList.vue` | 新增 `activeSessionId` watch → 滚动到底部 |

## 7. 验收标准

- [ ] 场景 1 / 2 / 3 经 CDP 实测通过：切换后始终落底
- [ ] `npm run lint` 通过
- [ ] 手动 `npm start`：侧栏点多个不同长度会话，均直接落到底部
