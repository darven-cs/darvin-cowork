# Usage Analytics 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

`usage_events` 已存在（cost-and-usage-tracking）。本 spec 在此之上聚合 / 去敏 / 报表。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 时序聚合：日 / 周 / 月 | buckets |
| G2 | top-N 排行：provider / model / session | rank |
| G3 | 去敏：永远不发送原始消息 / 文件 / 凭证 | redacted |
| G4 | UI 报表：dashboard | chart |
| G5 | ≥ 10 测试场景 | tests |

### 1.3 非目标

- 不上传任何数据到云端。
- 不做 ML 预测（v2）。
- 不做实时 streaming analytics（v2）。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/features/cost-and-usage-tracking/` | usage_events schema |
| `src/darvin-agent/internal/usage/` | 占位 |

## 3. 用户/系统场景

### 场景 1：daily 报表

**Given** 用户打开 dashboard
**When** 渲染
**Then** 显示今日 token / cost 累计；与昨日对比

### 场景 2：top 模型

**Given** 用户在 dashboard 切到 model tab
**When** 渲染
**Then** 列出按 cost 排序的 top 10 models

### 场景 3：去敏

**Given** usage_events 含 session_id
**When** 报表
**Then** 显示 hash 前 6 位；不显示原始 session_id

## 4. 功能需求

### FR-1 聚合 query

```sql
SELECT
  strftime('%Y-%m-%d', ts/1000, 'unixepoch') AS day,
  provider,
  model,
  SUM(prompt_tokens) AS in_tok,
  SUM(completion_tokens) AS out_tok,
  SUM(cost_usd) AS cost
FROM usage_events
WHERE ts BETWEEN ? AND ?
GROUP BY day, provider, model;
```

### FR-2 top-N

```sql
SELECT provider, model, SUM(cost_usd) cost
FROM usage_events
GROUP BY provider, model
ORDER BY cost DESC LIMIT 10;
```

### FR-3 去敏

- `session_id` → 6 字符前缀 hash
- timestamp → 保留日 / 周 / 月
- model 名 → 原样
- prompt 内容 → 不存

### FR-4 dashboard

`useAnalytics()`：

```ts
const daily = ref<DailyBucket[]>([])
const topModels = ref<ModelSummary[]>([])
const ratio = ref<{ todayVsYesterday: number }>({})
```

### FR-5 时间窗口

默认范围 last 30 days；可改 7d / 90d / 1y。

### FR-6 ≥ 10 测试场景

| # | 场景 |
|---|---|
| T1 | daily 聚合 |
| T2 | weekly 聚合 |
| T3 | monthly 聚合 |
| T4 | top-N 模型 |
| T5 | top-N provider |
| T6 | session hash 截断 |
| T7 | 时间窗口 |
| T8 | 空数据占位 |
| T9 | tz 跨日 |
| T10 | 报表导出 CSV |
| T11 | UI dashboard |

## 5. 安全与隐私

- 报表永远不上传。
- 导出 CSV 仅本机保存。
- 去敏不可逆。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| DB 不存在 | 空报表 |
| 大窗口查询慢 | 走索引 |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/usage/analytics.go`（新） | 聚合 |
| `src/darvin-agent/internal/usage/redact.go`（新） | 去敏 |
| `src/shared/darvin-api.ts` | 通道 |
| `src/renderer/composables/useAnalytics.ts` | UI |

## 8. 实施顺序与依赖

1. `analytics.go` + 单测
2. `redact.go`
3. UI

> 前置：`cost-and-usage-tracking`。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go/TS 单测 ≥ 10 条 |
| V3 | `go vet ./...` |
| V4 | `npm run smoke -- usage-analytics` |
| V5 | dev 手工：填测试数据看 dashboard |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- ML 预测（v2）。
- 上传云（v2）。
