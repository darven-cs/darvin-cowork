# Observability & Monitoring 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

商业化必须具备生产级 observability：

- 指标（metrics）
- 日志（logs）
- 告警（alerts）

LobsterAI 已有 `featureFlags.ts` 与 `runtime/metrics`；darvin-cowork 统一基础。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 指标 schema：counter / gauge / histogram / summary | metric types |
| G2 | 日志字段：time / level / msg / session / provider / model / cost | fields |
| G3 | 敏感信息脱敏（凭证 / 个人数据） | redact |
| G4 | 告警阈值 / 通知渠道 | alert |
| G5 | crash-free 口径 | sla metric |
| G6 | ≥ 10 测试场景 | tests |

### 1.3 非目标

- 不接入商业 SaaS（Datadog / NewRelic）；仅本地 + 可选导出。
- 不做分布式 tracing。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/features/runtime-supervision/` | state 事件 |
| `specs/features/heartbeat-and-cost-control/` | metrics 基础 |
| `src/darvin-agent/internal/observability/` | 占位 |

## 3. 用户/系统场景

### 场景 1：counter

**Given** runtime 跑
**When** chat 一次
**Then** `agent.chat.count{provider="openai"}` +1

### 场景 2：histogram

**Given** chat 调用
**When** 完成
**Then** `agent.chat.duration_ms{...}` 记录到 histogram

### 场景 3：日志脱敏

**Given** 日志含 API key
**When** 输出
**Then** 替换为 `***redacted***`

### 场景 4：告警

**Given** 连续 5 分钟 crash-free 低于 99%
**When** 触发
**Then** 推告警 banner + 用户邮箱（可选）

## 4. 功能需求

### FR-1 metrics

```go
type Counter struct { ... }
type Gauge struct { ... }
type Histogram struct { ... }

func Register(namespace string, name string, help string) Metric
```

namespace：

- `agent.*`
- `runtime.*`
- `provider.*`
- `im.*`
- `billing.*`
- `usage.*`

### FR-2 日志

```go
type LogLine struct {
    Timestamp int64
    Level     string  // 'debug' / 'info' / 'warn' / 'error'
    Msg       string
    Fields    map[string]any
}
```

结构化 JSON 输出。

### FR-3 脱敏

```go
func redact(line LogLine) LogLine {
    for k, v := range line.Fields {
        if isSensitive(k) {
            line.Fields[k] = "***redacted***"
        }
    }
    return line
}
```

sensitive keys: `api_key`, `secret`, `refresh_token`, `password`, `cookie`, `*_id_token`。

### FR-4 告警

```go
type AlertRule struct {
    Name     string
    Expr     string  // PromQL-like
    Severity string  // 'info' / 'warn' / 'critical'
    Channels []string
}
```

内置：

| rule | 表达式 |
|---|---|
| crash-free low | crash_total / run_total < 0.99 over 5m |
| circuit_open | circuit_state{state="open"} > 0 over 1m |
| usage_budget | usage_cost_usd{period="today"} / budget > 0.8 |

### FR-5 channels

- UI banner
- 系统通知（`Notification` API）
- 邮件（v2 接入）

### FR-6 crash-free 口径

```ts
crash_free = (sessions - crashed_sessions) / sessions
```

`crashed_sessions`：runtime supervisor 检测的 crash 计数。

### FR-7 ≥ 10 测试场景

| # | 场景 |
|---|---|
| T1 | counter 注册 |
| T2 | gauge |
| T3 | histogram |
| T4 | 日志 JSON |
| T5 | 脱敏 |
| T6 | 告警命中 |
| T7 | 告警通知 |
| T8 | crash-free 计算 |
| T9 | 阈值动态 |
| T10 | 导出 CSV |
| T11 | 历史 24h 报表 |

## 5. 安全与隐私

- 日志文件权限 0600。
- 告警内容不含 prompt。
- 用户可关闭 info 级日志。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| 指标存储爆炸 | rotate |
| 告警风暴 | dedup 60s |
| 邮件发送失败 | UI banner 兜底 |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/observability/metrics.go`（新） | 指标 |
| `logs.go`（新） | 日志 |
| `redact.go`（新） | 脱敏 |
| `alert.go`（新） | 告警 |
| `crashfree.go`（新） | sla |
| `src/renderer/components/observability/ObservabilityPanel.vue`（新） | UI |

## 8. 实施顺序与依赖

1. metrics + logs
2. redact
3. alert + crash-free
4. UI

> 前置：`runtime-supervision` + `heartbeat-and-cost-control`。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go/TS 单测 ≥ 10 条 |
| V3 | `go vet ./...` |
| V4 | `npm run smoke -- observability-and-monitoring` |
| V5 | dev 手工：触发 1 告警 |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- 商业 SaaS 接入（v2）。
- 分布式 tracing（v2）。
