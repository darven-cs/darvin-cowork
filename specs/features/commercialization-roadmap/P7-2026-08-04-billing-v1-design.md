# Billing v1 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

商业化必须支持：

- 订阅（按月 / 按年）
- 按量（token / image / media calls）
- 折扣码
- 退订
- 发票

darvin-cowork 当前无账单。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 账单账本（本地 ledger） | schema |
| G2 | 幂等计量（含 provider + usage_events 重放） | idempotent |
| G3 | 订阅（按月 / 按年）链路 | subscription |
| G4 | 按量链路 | pay-as-you-go |
| G5 | 折扣码 / 优惠券 | coupon |
| G6 | 退订 / 退款 | refund |
| G7 | 发票生成（PDF） | invoice |
| G8 | ≥ 10 测试场景 | tests |

### 1.3 非目标

- 不实接支付 SDK（仅 spec 约束，platform 集成 v2）。
- 不做 B2B 团购（v2）。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/features/oauth-login/` | PKCE + token 刷新 |
| `specs/features/cost-and-usage-tracking/` | usage 表 |
| `src/darvin-agent/internal/billing/` | 占位 |

## 3. 用户/系统场景

### 场景 1：订阅

**Given** user 选择「Pro 月度」
**When** checkout 完成
**Then** 写 `subscriptions` 表；UI 显示「已订阅」

### 场景 2：用量累计

**Given** user 跑对话
**When** cost-and-usage-tracking 写 usage
**Then** 周期累计；超量触发 top-up 链路

### 场景 3：折扣码

**Given** 用户输入 `LAUNCH10`
**When** checkout
**Then** 应用 10% off

### 场景 4：发票

**Given** 用户需要发票
**When** 点「生成发票」
**Then** 走本地 PDF 生成

## 4. 功能需求

### FR-1 ledger

```sql
CREATE TABLE billing_ledger (
    id              TEXT PRIMARY KEY,
    idempotent_key  TEXT UNIQUE NOT NULL,
    ts              INTEGER NOT NULL,
    kind            TEXT NOT NULL,    -- 'subscription' / 'usage' / 'topup' / 'refund'
    amount_cents    INTEGER NOT NULL,
    currency        TEXT NOT NULL,    -- 'USD' / 'CNY'
    provider        TEXT,             -- 仅 usage 必需
    subscription_id TEXT,
    description     TEXT,
    external_ref    TEXT              -- 第三方支付 reference
);
```

### FR-2 幂等性

`idempotent_key` = `sha1(kind + external_ref + amount)`，写时 `INSERT ON CONFLICT DO NOTHING`。

### FR-3 订阅

```sql
CREATE TABLE subscriptions (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL,
    plan_id         TEXT NOT NULL,    -- 'pro_monthly' / 'team_annual'
    status          TEXT NOT NULL,    -- 'active' / 'cancelled' / 'expired'
    started_at      INTEGER NOT NULL,
    expires_at      INTEGER NOT NULL,
    auto_renew      INTEGER NOT NULL DEFAULT 1
);
```

### FR-4 按量

```sql
CREATE TABLE usage_cycles (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL,
    period_start INTEGER NOT NULL,
    period_end   INTEGER NOT NULL,
    included_amount_cents INTEGER NOT NULL,
    overage_cents INTEGER NOT NULL DEFAULT 0,
    closed      INTEGER NOT NULL DEFAULT 0
);
```

周期内累计；超量计入 `overage_cents`。

### FR-5 折扣

```sql
CREATE TABLE coupons (
    code        TEXT PRIMARY KEY,
    off_percent INTEGER,              -- 50 = 50%
    off_amount_cents INTEGER,
    starts_at   INTEGER,
    ends_at     INTEGER,
    max_uses    INTEGER,
    used        INTEGER DEFAULT 0
);
```

### FR-6 退订

Subscriptions 表 `status='cancelled'` + 触发 `refund` 记录（仅未消耗部分可退）。

### FR-7 发票 PDF

`internal/invoice/pdf.go` 模板：

```go
type Invoice struct {
    No       string
    UserInfo
    Items    []Item
    Total    int
    IssuedAt time.Time
}

func RenderPDF(inv Invoice, w io.Writer) error
```

### FR-8 ≥ 10 测试场景

| # | 场景 |
|---|---|
| T1 | ledger insert |
| T2 | 幂等命中 |
| T3 | 订阅激活 |
| T4 | 续费 |
| T5 | 退订 |
| T6 | 退款 |
| T7 | 折扣应用 |
| T8 | 折扣码过期 |
| T9 | 周期累计 |
| T10 | 超量累计 |
| T11 | 发票 PDF |

## 5. 安全与隐私

- 金额不写明文金额 if 加密可选。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| 幂等冲突 | NOOP |
| 退款时间窗口 | 14 天 |
| 折扣码过期 | UI 提示 |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/billing/ledger.go`（新） | ledger |
| `subscription.go`（新） | 订阅 |
| `cycle.go`（新） | usage cycle |
| `coupon.go`（新） | coupon |
| `refund.go`（新） | 退款 |
| `invoice.go`（新） | 发票生成 |
| `src/shared/darvin-api.ts` | 通道 |
| `src/renderer/components/settings/SettingsPanelBilling.vue`（新） | UI |

## 8. 实施顺序与依赖

1. ledger.go
2. subscription.go
3. cycle.go
4. coupon.go
5. refund.go
6. invoice.go

> 前置：`oauth-login` + `cost-and-usage-tracking`。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go 单测 ≥ 10 条 |
| V3 | `go vet ./...` |
| V4 | `npm run smoke -- billing-v1` |
| V5 | dev 手工：mock ledger |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- 真实支付 SDK（v2）。
- B2B 团购（v2）。
