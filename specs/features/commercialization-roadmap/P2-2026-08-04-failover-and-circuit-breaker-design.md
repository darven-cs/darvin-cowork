# Failover & Circuit Breaker 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更更新的仓库规则为准。

## 1. 概述

### 1.1 问题

商用 Provider 经常出现以下问题：

- 偶发 5xx / 429
- 长时间 outage
- streaming 截断
- rate limit
- 凭证失效

darvin-cowork 用户期望：

- **session 不丢失**：当前 provider 失败不丢 events。
- **自动切换**：在用户配置 fallback 链上自动 failover。
- **可观测**：失败次数 / 状态切换有事件推送。
- **不滥用**：避免对 outage 的 provider 反复打。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 状态机 `CLOSED / OPEN / HALF_OPEN` | state machine |
| G2 | 连续失败阈值（默认 5）后进 OPEN | count |
| G3 | OPEN 持续 cooldown（默认 30s） | timer |
| G4 | HALF_OPEN 探测 1 次；成功回 CLOSED | probe |
| G5 | Session 不丢失 | replay buffer |
| G6 | Failover 链：用户在 settings 显式 order | config |
| G7 | ≥ 10 状态机测试场景 | tests |

### 1.3 非目标

- 不在 Provider 层重试（避免雪崩）；仅由 Failover 决定。
- 不跨 session 共享 circuit（per-provider / per-key）。
- 不实现 global circuit；每个 provider 一个。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/features/provider-registry/` | Provider 接口 + SwitchProvider |
| `src/darvin-agent/internal/provider/errors.go` | 错误归一（含 RetryAfter / Retryable 字段） |

## 3. 用户/系统场景

### 场景 1：一次失败可重试

**Given** 当前 provider=openai，状态 CLOSED
**When** provider 返回 503（Retryable=true）
**Then** Failover 重试同 provider 1 次；依然 503 才计入失败计数

### 场景 2：连续失败进 OPEN

**Given** 在 30s 内累计失败 5 次
**When** 第 5 次失败入队
**Then** 状态变 OPEN；后续请求直接 fail-fast 不打 provider

### 场景 3：half-open 探测

**Given** OPEN 已持续 30s
**When** 下一次请求进入
**Then** 状态 HALF_OPEN，发 1 次 probe；成功 → CLOSED，失败 → OPEN 重置 timer

### 场景 4：failover 切 provider

**Given** `primary=openai`，`fallback=gemini`
**When** openai 进入 OPEN
**Then** 下次 Chat 切到 gemini；session 内 events 续写

### 场景 5：observability

**Given** 状态变化
**When** 任意状态切换
**Then** 发送 `provider.circuit.state` 事件给 renderer，UI 显示 banner

## 4. 功能需求

### FR-1 状态机

```go
type State int

const (
    Closed State = iota
    Open
    HalfOpen
)
```

```go
type Circuit struct {
    name            string
    state           State
    failures        int
    cooldown        time.Duration
    nextProbeAfter  time.Time
    mu              sync.Mutex
}
```

### FR-2 转换规则

| 当前 | 事件 | 下一 |
|---|---|---|
| Closed | 失败 N 次 | Open |
| Open | cooldown 到期 | HalfOpen |
| HalfOpen | probe 成功 | Closed |
| HalfOpen | probe 失败 | Open（重置 timer） |
| Closed | 失败（网络 / 5xx） | Closed + 计数+1 |

### FR-3 失败计数

可配置的失败类型：

- `HTTP 5xx`
- `HTTP 429` 超过 Retry-After
- `network timeout`
- `stream truncation`
- `ErrProviderUnauthorized`（计入，但不重试）

不可计为失败的：

- 用户取消（context.Canceled）
- 业务校验错误（4xx BAD_REQUEST）

### FR-4 Cooldown

默认 30s；用户可在 settings 中覆盖（不超过 5min）。

### FR-5 Retryable 一次重试

状态机在 Closed 时对 Retryable 错误做 1 次重试（同 provider），仍失败再计数 +1。

### FR-6 Failover Chain

```go
type FailoverChain struct {
    Items []string // provider IDs in priority order
}
```

- 用户在 settings 中配置：`primary` + `fallbacks[]`
- Failover 在 circuit 进 OPEN 时切下一个
- 切完所有 provider 进 `fatal`：返回 `ErrProviderNoAvailable`，UI 提示

### FR-7 Session 不丢失

切换 provider 时：

- 不修改 session_id
- 不重新发已确认的 messages
- 把切换信息写进 audit log

### FR-8 Observability

事件：

```ts
interface DarvinCircuitStateEvent {
    type: 'provider.circuit.state';
    providerId: string;
    from: State;
    to: State;
    failures: number;
}
```

UI 在每个 provider 卡片显示状态徽章。

### FR-9 测试场景 ≥ 10

| # | 场景 |
|---|---|
| T1 | Closed → Open（5 失败） |
| T2 | Open cooldown 到期 → HalfOpen |
| T3 | HalfOpen probe 成功 → Closed |
| T4 | HalfOpen probe 失败 → Open |
| T5 | 失败计数重置（成功 1 次） |
| T6 | Retryable 一次重试 |
| T7 | non-retryable 不计入 |
| T8 | fail-over 切换 provider |
| T9 | 全链失败 → ErrProviderNoAvailable |
| T10 | 并发 fail-fast 不重入 |
| T11 | 状态切换事件发送 |

## 5. 安全与隐私

- error body 包含潜在 PII，按 Provider spec 已有脱敏；Failover 仅看 code / status。
- audit log 写本地 SQLite，含 provider_id / timestamps / state_diff。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| circuit observer panic | 隔离，circuit 不失效 |
| half-open probe 超时 | 视作失败 → OPEN |
| 用户禁用 failover chain | Failover 不可用即抛 `ErrProviderNoAvailable` |
| 状态持久化失败 | fallback 到内存，重启后重建 |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/failover/circuit.go` | Circuit 实现 |
| `src/darvin-agent/internal/failover/chain.go` | Failover Chain |
| `src/darvin-agent/internal/failover/policy.go` | 失败计数 / retry 策略 |
| `src/shared/darvin-api.ts` | `provider.circuit.state` 事件类型 |
| `src/renderer/services/providers.ts` | 状态徽章 UI |

## 8. 实施顺序与依赖

1. `circuit.go` + 单测（10+ 场景）
2. `chain.go` + 配置校验
3. `policy.go` + 与 Provider 集成
4. UI 状态徽章

> 前置：`specs/features/provider-registry/` 已确认。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go 单测 ≥ 10 条 |
| V3 | `go vet ./...` |
| V4 | `npm run smoke -- failover-circuit-breaker` |
| V5 | dev 手工验证：mock provider 触发 5 失败 → 状态切换事件 |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- Provider 层重试（这里是 Failover + Circuit，仅在 circuit 内部做 1 次重试）。
- 跨 session circuit 共享。
