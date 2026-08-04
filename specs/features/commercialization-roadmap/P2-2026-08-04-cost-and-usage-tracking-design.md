# Cost & Usage Tracking 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

商业化必须能精确测算每个 session / 每个 provider / 每个 model 的费用；同时也要给用户呈现 usage 统计。目前没有任何统一的 token / cost 视图。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 四维数据：token / model / provider / session | schema |
| G2 | Pricing 表：内嵌常用模型 + 用户可覆盖 | config |
| G3 | Real-time 累计 + 历史汇总 | aggregation |
| G4 | 不存储 prompt 内容；只记数量 / 维度 | privacy |
| G5 | 报表：日 / 周 / 月聚合 | report |
| G6 | ≥ 10 单元测试场景 | tests |

### 1.3 非目标

- 不做发票 / 计费（账单由 billing-v1 主理）。
- 不做用量预测 / 容量规划。
- 不导出计费标准 CSV / PDF（v2）。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/features/provider-*/` | 每家 UsageBreakdown 已规范 |
| `src/darvin-agent/internal/store/` | 计划承载 usage 表 |

## 3. 用户/系统场景

### 场景 1：记录一次 Chat usage

**Given** 用户发 1 条消息，模型 `gpt-4o` 返回
**When** Chat 结束
**Then** `usage_events` 表插入一行：provider=openai, model=gpt-4o, session=X, promptTokens=120, completionTokens=45, costUsd=0.0014

### 场景 2：覆盖 pricing

**Given** 用户在 settings.json 自定义 `gpt-4o.input = 0.000005`
**When** 计算 cost
**Then** 用用户价格替换默认表

### 场景 3：用户 session 报告

**Given** session id 已知
**When** 调用 `usage.sessionReport(sessionId)`
**Then** 返回该 session 总 tokens / cost / 时间线

### 场景 4：隐私

**Given** usage 事件
**When** SQLite 行写入
**Then** 不含任何 prompt / response 内容；仅维度 + 数量

## 4. 功能需求

### FR-1 数据模型

```sql
CREATE TABLE usage_events (
    id              INTEGER PRIMARY KEY,
    ts              INTEGER NOT NULL,   -- epoch ms
    session_id      TEXT NOT NULL,
    provider        TEXT NOT NULL,
    model           TEXT NOT NULL,
    prompt_tokens   INTEGER NOT NULL,
    completion_tokens INTEGER NOT NULL,
    cached_tokens   INTEGER NOT NULL,
    cost_usd        REAL NOT NULL,
    finish_reason   TEXT NOT NULL,
    circuit_state   TEXT NOT NULL,     -- 'closed' / 'open' / 'half-open' / 'failover'
    failover_provider TEXT,
    source          TEXT NOT NULL      -- 'chat' / 'embedding' / 'asr' / 'media'
);
```

索引：

- `(session_id, ts)`
- `(provider, model, ts)`
- `(ts)` 用于时序查询

### FR-2 Pricing 表

```go
type PricingEntry struct {
    Provider string
    Model    string
    Input    float64 // per token USD
    Output   float64
    Cached   float64 // optional (如适用)
}
```

内置：

```go
var defaultPricing = []PricingEntry{
    {Provider: "openai", Model: "gpt-4o", Input: 0.0000025, Output: 0.00001},
    // ...
}
```

用户 settings.json 可 override：

```json
{
  "providers.openai.pricing": {
    "models.gpt-4o": { "input": 2.5e-6, "output": 1.0e-5 }
  }
}
```

### FR-3 写入路径

`UsageBreakdown` 由 Provider 实现返回 → `usageRecorder.Record(event)` → SQLite INSERT。

`Record` 是 sync 操作；批量缓冲可选。

### FR-4 聚合查询

```go
func (r *Repo) SessionReport(sessionId string) (*UsageReport, error)
func (r *Repo) DailyReport(day time.Time) ([]DailyBucket, error)
func (r *Repo) TopModels(limit int) ([]ModelSummary, error)
```

### FR-5 报表展示

Renderer 通过 IPC `usage:report:session` / `usage:report:daily` 获取，进入设置页 `usage` 区块。

### FR-6 隐私边界

- 记录不写：prompt content / response content / tool_args / user email
- 写：`session_id hash`（不写 raw session_id），`provider` / `model` / `ts` / 数量
- 备份策略：与主 DB 同 WAL

### FR-7 ≥ 10 测试场景

| # | 场景 |
|---|---|
| T1 | Insert 事件 |
| T2 | Session 聚合 |
| T3 | Daily 聚合 |
| T4 | Provider 排名 |
| T5 | pricing 覆盖 |
| T6 | 默认 pricing fallback |
| T7 | cached tokens 计算 |
| T8 | 失败（finish_reason="error"）也记录 |
| T9 | failover 标记 |
| T10 | 不写 prompt 内容（脱敏） |
| T11 | 时区聚合（DST 不重计） |

## 5. 安全与隐私

- session_id hash（SHA-256）。
- pricing 文件权限 0600。
- settings.json 的 cost 数据导出需要二次确认按钮。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| 写 SQLite BUSY | 走 `db.Writer` 队列，由 db-consistency 主理 |
| pricing 缺值 | 退 0 cost + warning log |
| Pricing 字段类型错误 | 启动 fail-fast |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/usage/schema.sql` | 表结构 |
| `src/darvin-agent/internal/usage/recorder.go` | Record / Insert |
| `src/darvin-agent/internal/usage/pricing.go` | Pricing |
| `src/darvin-agent/internal/usage/repo.go` | 聚合查询 |
| `src/shared/darvin-api.ts` | `usage:report:*` channel |
| `src/renderer/composables/useUsage.ts` | 报表 UI |

## 8. 实施顺序与依赖

1. `schema.sql` + `recorder.go`
2. `pricing.go` + 默认表
3. `repo.go` 聚合
4. UI composable

> 前置：`specs/features/provider-*/` 已确认（每个有 UsageBreakdown）。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go 单测 ≥ 10 条 |
| V3 | `go vet ./...` |
| V4 | `npm run smoke -- cost-usage-tracking` |
| V5 | dev 手工：session 报告与单条 message 报告一致 |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- 发票 / 计费（billing-v1）。
- 用量异常告警（usage-analytics）。
