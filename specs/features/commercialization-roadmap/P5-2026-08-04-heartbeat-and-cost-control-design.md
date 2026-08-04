# Heartbeat & Cost Control 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

darvin-cowork 长时间运行会产生 token / 资源泄漏，必须有周期性检查 + 预算封顶。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 5 分钟 heartbeat 自检 | interval |
| G2 | 泄漏自愈：未关闭的 ws / goroutine / 文件句柄清掉 | cleanup |
| G3 | token / 费用预算封顶 | budget |
| G4 | 超预算行为：暂停 / 提示 / 强制退出 | policy |
| G5 | 用户可调整预算上限 | settings |
| G6 | ≥ 10 测试场景 | tests |

### 1.3 非目标

- 不做跨设备 budget 同步。
- 不做 budget 报表导出。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/features/cost-and-usage-tracking/` | 用量记录 |
| `specs/features/runtime-supervision/` | supervisor |
| `src/darvin-agent/internal/runtime/` | 计划承载 |

## 3. 用户/系统场景

### 场景 1：heartbeat 自检

**Given** runtime 在线
**When** 每 5 分钟
**Then** 收集指标：goroutine count / open ws / open files；写 log

### 场景 2：泄漏自愈

**Given** ws 数 > 50
**When** heartbeat 检测
**Then** 关闭空闲 ws（idle > 30min），释放资源

### 场景 3：budget

**Given** 用户设置 daily cost cap = $1
**When** 累计 cost >= 80%
**Then** 提示预警；100% 强制 pause 新请求

### 场景 4：超限 fallback

**Given** daily cap 已满
**When** 用户发新消息
**Then** 拒绝 + 弹 UI banner「今日 budget 已用完」

## 4. 功能需求

### FR-1 heartbeat

```go
type Heartbeat struct {
    Interval time.Duration // 默认 5min
    Fn       func(ctx context.Context) error
}
```

`runtime/heartbeat.go` 启动一个 ticker。

### FR-2 自检指标

- `runtime.goroutine_count`
- `runtime.open_connections`
- `runtime.open_files`
- `runtime.memory_alloc_mb`
- `runtime.queue_depth`
- `runtime.last_event_age_ms`

### FR-3 泄漏自愈

```go
func cleanupIdleWS(idleFor time.Duration)
func cleanupFilesNotInWorkspace()
```

每个 ws / 文件持有 `lastUsed` 时间戳。

### FR-4 budget

```go
type Budget struct {
    DailyCostUSD  float64 // 0 = unlimited
    MonthlyCostUSD float64
    PerSessionUSD float64
    TokensDaily   int
}
```

累计由 `cost-and-usage-tracking` 写入。

### FR-5 行为

| level | 行为 |
|---|---|
| 80% | push `budget.warning` 事件 |
| 100% | `budget.exceeded` + 自动 pause 新请求 |
| 用户手动 | 解除 pause |

### FR-6 UI

`useBudget()` composable：显示 daily / monthly bars。

### FR-7 ≥ 10 测试场景

| # | 场景 |
|---|---|
| T1 | 5min 触发 |
| T2 | goroutine count 报告 |
| T3 | 泄漏自愈 |
| T4 | budget warning |
| T5 | budget exceeded pause |
| T6 | 用户手动解除 |
| T7 | 心跳 panic 隔离 |
| T8 | 时区跨日重置 |
| T9 | per-session cap |
| T10 | budget 持久化 |
| T11 | 日历天切换 |

## 5. 安全与隐私

- 心跳指标不写消息内容。
- budget 配置 local-only。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| 心跳协程 panic | watcher 隔离重启 |
| 持久化失败 | in-memory fallback |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/heartbeat/heartbeat.go`（新） | main ticker |
| `src/darvin-agent/internal/heartbeat/cleanup.go`（新） | 泄漏自愈 |
| `src/darvin-agent/internal/heartbeat/budget.go`（新） | budget |
| `src/shared/darvin-api.ts` | 事件 |
| `src/renderer/composables/useBudget.ts` | UI |

## 8. 实施顺序与依赖

1. heartbeat.go
2. cleanup.go
3. budget.go
4. UI

> 前置：`cost-and-usage-tracking`。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go 单测 ≥ 10 条 |
| V3 | `go vet ./...` |
| V4 | `npm run smoke -- heartbeat-cost-control` |
| V5 | dev 手工：改 budget cap → 验证 warn/exceed |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- 全平台 app nap 防护（spec: anti-sleep-and-shortcuts）。
