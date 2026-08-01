# 上下文压缩 UI

> 编号 **04**。手动压缩入口 + 自动压缩可视化 + 压缩边界分隔。**依赖 00-darvin-api-extension 与 03-token-context-usage**。

## 1. 背景

spec 00 已把 `'compaction'` 从 `LIFECYCLE_EVENT_TYPES` 静默丢弃中移除（事件现已正常流入 renderer）；本 spec 剩余工作 = UI 无入口、无反馈、无历史。LobsterAI 把压缩做成端到端体验：圆环点击 → 手动压缩；自动压缩时圆环旋转 + `AssistantTurnBlock` 在 turn 之间插入 `ContextCompactionDivider`；i18n 4 态文案。

## 2. 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 圆环点击触发手动压缩请求 | `window.darvin.compactContext(sessionId)` IPC |
| G2 | 自动压缩触发时圆环持续旋转动画 | `status='compacting'` 驱动 |
| G3 | 压缩完成后显示「自动/手动压缩完成」浮层 | toast 3s 消失 |
| G4 | turn 之间插入 `ContextCompactionDivider` 视觉边界 | `<hr>` + 文案 + 时间戳 |
| G5 | 协议：`compaction` / `context_usage.compacting` 正式 union 成员 | 00 spec 落地 |
| G6 | 压缩历史：`compactionCount` + `latestCompactionAt` 在 about / settings 显示 | settings 04 spec 实现显示 |
| G7 | 压缩失败回退：`status='danger'` + toast「压缩失败」 | i18n `coworkContextCompactionFailed` |

## 3. 非目标

- 不改 Go agent 压缩策略（仅前端可视化）
- 不实现压缩历史详情页（只显示次数 + 最近时间）
- 不实现压缩前快照导出

## 4. 设计要点

### 4.1 手动压缩入口

```vue
<!-- ContextUsageIndicator.vue -->
<button @click="onCompactClick" :disabled="status === 'compacting'">
  <!-- 圆环 SVG -->
</button>

<script setup>
async function onCompactClick() {
  if (status.value === 'compacting') return;
  const sid = session.activeSessionId.value;
  if (!sid) return;
  await window.darvin.compactContext(sid);
  // 后续等 context_usage + compaction 事件回流，状态机自己切
}
</script>
```

### 4.2 IPC 契约扩展

```ts
// 00 spec + 本 spec 共同加：
compactContext(sessionId: string): Promise<{ accepted: boolean }>;
```

**Go 端落地决策**：Go 侧压缩逻辑已存在（`internal/agent/event/event.go:202` 的 `CompactionEvent` + `ContextEngine.Compact()`，配置 `compact_tail_keep` / `compact_max_retries`），但由 ACP loop 的预算检查**自动触发，没有手动 gateway RPC**（仅有 `store.CompactionCheckpoint` 落库）。手动压缩要真实生效必须补一个 Go 端点：

1. Go 加轻量 `agent.compact_context` RPC（参数 `{ sessionId }`），触发既有 `Compact()` 流程（**不改压缩策略本身**），返回 `{ accepted: true }`；主进程 `darvin:compact_context` IPC 转发，preload 暴露 `window.darvin.compactContext(sessionId)`。
2. Go 未就绪 / 离线时：IPC 返回 `{ accepted: false }`，UI **不进入 compacting 动画、不 toast**，圆环保持当前状态（避免假压缩）。

### 4.3 压缩边界分隔

`ContextCompactionDivider.vue`：

```
─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─
  ↻ 上下文已压缩 · 2026-08-01 14:32 · 保留最近 6 轮
─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─
```

### 4.4 浮层文案

| 状态 | i18n key | 文案 |
|---|---|---|
| 手动触发中 | `chat.context.compacting.manual` | 手动压缩中... |
| 自动触发 | `chat.context.compacting.auto` | 自动压缩中... |
| 完成 | `chat.context.compacted` | 上下文已压缩（XX → YY tokens） |
| 失败 | `chat.context.compactionFailed` | 压缩失败，可重试 |

## 5. 用户场景

### 场景 1：用户主动点圆环压缩

**Given** context 占比 70%

**When** 用户点 chat header 圆环

**Then** IPC 触发 `compactContext`；圆环进入 compacting 状态旋转；3s 后完成；toast「上下文已压缩」

### 场景 2：自动压缩（context 到 95%）

**Given** 占比 95% 触发 Go agent 自动压缩

**When** 推 `compaction { reason: 'auto' }` 事件

**Then** 圆环旋转；当前 turn 渲染完成后插入 `ContextCompactionDivider`；下一 turn 圆环恢复正常

### 场景 3：压缩失败

**Given** Go agent 推 `compaction` 事件后立刻推 `error` 事件

**When** renderer 收到 error

**Then** 圆环 `status='danger'` 红色；toast「压缩失败」；用户可重试点

## 6. 验收

- [x] 圆环点击触发 `compactContext` IPC
- [x] compacting 状态旋转动画流畅
- [x] 完成后显示 toast + 数字前后对比
- [x] `ContextCompactionDivider` 渲染边界
- [x] 失败时圆环变红 + toast 提示
- [x] i18n 4 态文案齐

## 7. 依赖

- **前置**：00 + 03
- **可并行**：01 / 02 / 05
- **后置**：07-settings-expansion（设置里要显示压缩次数）

## 8. 参考

### darvin-cowork
- `src/main/runtime/client.ts` — `LIFECYCLE_EVENT_TYPES`（spec 00 已移除 `'compaction'`，事件现已正常 push）
- `src/renderer/components/chat/ChatHeader.vue` — 圆环挂载点
- `src/renderer/composables/useMessages.ts` — 接收 `compaction` 事件

### LobsterAI（借鉴）

> 参考项目根目录：`~/桌面/github-project/LobsterAI`（下述路径均相对该项目根）。组件实现遇阻时直接查该项目源码。

- `src/renderer/components/cowork/AssistantTurnBlock.tsx` — `ContextCompactionDivider` 位置
- `src/renderer/components/cowork/ContextUsageIndicator.tsx` — `onCompact` 回调
- `src/shared/cowork/constants.ts` — `CoworkContextUsageRefreshMode`
- `specs/features/cowork-context-compaction/2026-05-08-cowork-context-compaction-design.md` — 完整设计参考
- `specs/features/cowork-context-compaction/2026-06-09-cowork-context-compaction-quality-optimization-design.md`

## 9. 关联调研

`specs/features/agent-output-ux-research/2026-08-01-cowork-vs-lobsterai-comparison.md` § 2.3「Token / 上下文用量」+ § 2.1「Proposed plan 确认 / 压缩边界」
