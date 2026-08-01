# Token / 上下文用量可视化

> 编号 **03**。在 chat header 与每条 assistant 消息底部展示 input/output/cache token 数字 + 圆环可视化。**依赖 00-darvin-api-extension**。

## 1. 背景

`DarvinUsage` 在 `darvin-api.ts` 已定义 `inputTokens/outputTokens/totalTokens`（spec 00 已补 `cacheReadTokens` / `cacheWriteTokens`），但 UI 完全不消费。LobsterAI 把单条消息的 `usage` 放 hover 浮层，把 session 级别的 `contextUsage` 放 chat header 圆环（`ContextUsageIndicator.tsx`）。

## 2. 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 单条 assistant 消息底部展示 token 三元组（in / out / total） | `TurnMeta` 组件内 inline |
| G2 | chat header 圆环展示 session 上下文占比 | `ContextUsageIndicator` 组件 |
| G3 | 圆环 tooltip 显示数字 + 模型上下文窗口 | hover 浮层 |
| G4 | 圆环状态颜色：`unknown/normal/warning/danger/compacting` | 5 态颜色 |
| G5 | 协议扩展 `DarvinContextUsage` + `cacheReadTokens/cacheWriteTokens` | 00 spec 落地 |
| G6 | `useMessages` 维护 `contextUsageBySessionId` | composable 派生 |

## 3. 非目标

- 不做按 turn 趋势图（图表是后续 spec）
- 不做 token 预算告警 toast（仅颜色提示）
- 不实现用户级 token 配额（这是账号体系后续 spec）

## 4. 设计要点

### 4.1 单条消息 token 展示

`TurnMeta.vue` 布局（hover 显示）：

```
┌──────────────────────────────────────────┐
│  claude-sonnet-4-5 · 2026-08-01 14:32   │
│  ⌥ 1.2k in · 0.3k out · 0.5k cache ↗   │
│                              [复制][Fork]│
└──────────────────────────────────────────┘
```

### 4.2 圆环组件

`ContextUsageIndicator.vue`：

- 尺寸：28×28px（与 LobsterAI 对齐）
- SVG：stroke-dasharray 实现圆环
- 颜色：继承 `text-text-muted` / `text-warning` / `text-danger` / `text-accent`
- 数据：`percent` 来自 `useMessages.contextUsageBySessionId[sessionId]?.percent`
- 状态 `compacting` 时附加 `animate-spin`

```vue
<template>
  <button
    class="context-usage-indicator"
    :class="statusClass"
    :title="tooltipText"
    @click="onCompact"
  >
    <svg viewBox="0 0 20 20">
      <circle cx="10" cy="10" r="7" stroke="currentColor" fill="none" stroke-width="2" opacity="0.2" />
      <circle
        cx="10" cy="10" r="7"
        stroke="currentColor" fill="none" stroke-width="2"
        :stroke-dasharray="CIRCUMFERENCE"
        :stroke-dashoffset="dashOffset"
        transform="rotate(-90 10 10)"
      />
    </svg>
    <span class="text-[10px]">{{ percentLabel }}</span>
  </button>
</template>
```

### 4.3 状态颜色映射

| status | token |
|---|---|
| `unknown` | `text-text-subtle` |
| `normal` (`< 60%`) | `text-text-muted` |
| `warning` (`60-85%`) | `text-warning` |
| `danger` (`> 85%`) | `text-danger` |
| `compacting` | `text-accent` + `animate-spin` |

## 5. 用户场景

### 场景 1：日常使用看上下文占用

**Given** session 跑 5 轮对话，context 占比 45%

**When** 用户看 chat header

**Then** 圆环显示 45%，灰色；hover tooltip 写「已用 45k / 上下文 100k」

### 场景 2：长会话接近上限

**Given** context 占比升到 78%

**When** 圆环重新渲染

**Then** 颜色变黄（warning）；tooltip 提示「接近上限，可手动压缩」

### 场景 3：超限压缩

**Given** 占比 100%

**When** Go agent 推 `context_usage { status: 'compacting' }` + `compaction` 事件

**Then** 圆环变 accent 色 + 持续旋转；圆环不可点（已在压缩）；压缩完成后 `status: 'normal'` + 新百分比

## 6. 验收

- [ ] 单条 assistant 消息 hover 显示 token 三元组
- [ ] chat header 圆环随 `context_usage` 事件实时更新
- [ ] 5 个状态颜色 + 动画正确
- [ ] tooltip 显示数字 + 上下文窗口大小
- [ ] 圆环可点击（手动压缩入口由 04 spec 实现，本 spec 只占位回调）
- [ ] `useMessages.contextUsageBySessionId` 单测覆盖

## 7. 依赖

- **前置**：00-darvin-api-extension
- **可并行**：01 / 02
- **后置**：04-context-compaction-ui（圆环点击事件由 04 落地）

## 8. 参考

### darvin-cowork
- `src/shared/darvin-api.ts` — `DarvinUsage`（spec 00 已补 cache 字段）
- `src/renderer/composables/useMessages.ts` — 加 `contextUsageBySessionId` 状态
- `src/renderer/components/chat/ChatHeader.vue` — 圆环挂载点
- `src/renderer/components/runtime/RuntimeStatusBadge.vue` — 同位置小徽章参考

### LobsterAI（借鉴）

> 参考项目根目录：`~/桌面/github-project/LobsterAI`（下述路径均相对该项目根）。组件实现遇阻时直接查该项目源码。

- `src/renderer/components/cowork/ContextUsageIndicator.tsx:40-121` — 圆环
- `src/renderer/types/cowork.ts:103-116` — `CoworkContextUsage`
- `src/renderer/store/slices/coworkSlice.ts` — `contextUsageBySessionId` 状态

## 9. 关联调研

`specs/features/agent-output-ux-research/2026-08-01-cowork-vs-lobsterai-comparison.md` § 2.3「Token / 上下文用量」
