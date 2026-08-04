# Context Compaction UI — 商业化迭代 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

[`2026-08-01-context-compaction-ui-design.md`](../context-compaction-ui/2026-08-01-context-compaction-ui-design.md) 已经实现「auto / manual / preview 三模式 + Settings 侧栏」。商业化迭代补齐：

- provider 切换前的 compact 引导。
- 费用预估显示（USD / 单条 / 累计）。
- Compaction 后的 diff 高亮（v1 仅弹 modal）。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 在 provider 切换对话框中显示 current vs target context window | settings UI |
| G2 | 实时费用估算（基于 cost-and-usage-tracking） | composable |
| G3 | Compaction diff 高亮（删 / 改 / 保留） | renderer |
| G4 | 兼容 v1 三模式，不破坏 API | backward |
| G5 | ≥ 10 场景 | tests |

### 1.3 非目标

- 不重写 v1 三模式 API。
- 不接入第三方 diff 库（手写实现已够用）。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/features/context-compaction-ui/2026-08-01-...` | v1 设计 |
| `src/renderer/components/settings/` | 计划承载 |
| `src/renderer/composables/useCompaction.ts` | 占位（推断） |

## 3. 用户/系统场景

### 场景 1：切 provider 强提示

**Given** 当前 openai context window=128k
**When** 用户点选 gemini-2.5-flash
**Then** 弹 modal 显示「目标 1M tokens；当前 session 已用 110k」；提供「立即 compact」+「不 compact」

### 场景 2：实时费用

**Given** session 已运行
**When** token 累计 100k
**Then** settings 页 usage 显示 ≈ 0.23 USD（按 pricing 表）

### 场景 3：diff 高亮

**Given** Compaction 执行
**When** v1 modal 弹内容差异
**Then** 渲染器在原文基础上加 `bg-warning/30` 高亮删除 / `bg-success/20` 高亮新加 / 普通保留

## 4. 功能需求

### FR-1 provider 切换前 compact

```ts
useProviderSwitchModal() {
  const onSwitch = async (target) => {
    if (target.contextWindow < current.contextWindow && usedTokens > target.contextWindow * 0.7) {
      openModal({ kind: 'compact-before-switch', target, current })
    }
  }
}
```

### FR-2 费用估算

```ts
function estimateCost(usage: UsageBreakdown, pricing: PricingEntry[]): number
```

通过 `usage:pricing` IPC 取实时定价。

### FR-3 diff 高亮

```ts
function compactDiff(prev: string, next: string): DiffOp[]
```

返回 `{ kind: 'add' | 'remove' | 'keep', text }[]`。渲染时按 kind 加 utility class。

### FR-4 v1 兼容

保留原 `auto / manual / preview` enum 值；新增 `compact-before-switch` 第四种 mode。

### FR-5 i18n

新增 key：

- `compaction.beforeSwitch.title`
- `compaction.beforeSwitch.body`
- `compaction.diff.added`
- `compaction.diff.removed`
- `compaction.estimate.cost`

### FR-6 测试场景 ≥ 10

| # | 场景 |
|---|---|
| T1 | 切 provider 弹 modal |
| T2 | 不弹（context 已 < 70%） |
| T3 | estimate cost |
| T4 | diff 加亮渲染 |
| T5 | v1 mode 不变 |
| T6 | manual 模式仍生效 |
| T7 | auto 模式切 provider 仍触发 |
| T8 | preview mode 显示 diff |
| T9 | pricing override 影响 estimate |
| T10 | 语言切换 i18n 命中 |
| T11 | dark / light mode token 一致 |

## 5. 安全与隐私

- estimated cost 不上传任何 user content。
- modal 内容不上任何外部域。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| pricing 缺 | estimate = 0 + UI 提示 |
| diff 极长（> 10k 行） | 摘要 + 滚动展开 |
| modal 阻塞主线程 | virtualize |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/renderer/composables/useProviderSwitchModal.ts` | 弹窗逻辑 |
| `src/renderer/composables/useCostEstimate.ts` | 费用估算 |
| `src/renderer/components/settings/CompactionDiff.vue` | diff 高亮渲染 |
| `src/renderer/services/i18n.ts` | 新增 i18n key |
| tests | ≥ 10 场景 |

## 8. 实施顺序与依赖

1. `useCostEstimate.ts`
2. `CompactionDiff.vue`
3. `useProviderSwitchModal.ts`
4. i18n

> 前置：`specs/features/context-compaction-ui/v1` + `cost-and-usage-tracking`。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | TS 单测 ≥ 10 条 |
| V3 | `npm run smoke -- context-compaction-ui-commercialization` |
| V4 | dev 手工：provider 切换弹窗 |
| V5 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V6 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- v1 三模式重写。
- Compact 异步 worker（沿用 v1 同步）。
