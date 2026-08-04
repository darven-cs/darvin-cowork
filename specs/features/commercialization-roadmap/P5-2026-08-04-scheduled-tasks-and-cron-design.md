# Scheduled Tasks & Cron 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

商业化用户希望设置定时任务：「每天 9 点整理当天 project」、「每周日汇总一周代码 diff」。darvin-cowork 当前没有定时任务能力。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | cron 表达式（含 `* / , -`） | parser |
| G2 | timezone 显式（IANA 名） + DST 正确 | tz |
| G3 | missed-task queue：错过的任务按幂等键排队补跑 | queue |
| G4 | 幂等重跑：同一 trigger 标识命中即 skip | key |
| G5 | 任务状态：`scheduled / running / ok / failed / skipped` | state |
| G6 | UI 设置页定时任务列表 | settings |
| G7 | ≥ 10 测试场景 | tests |

### 1.3 非目标

- 不做分布式 cron（单机本地）。
- 不做 webhook 类型 trigger（v2）。
- 不做事务级回放。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `src/darvin-agent/internal/runtime/` | 计划承载 |
| `specs/features/runtime-supervision/` | supervisor 提供状态 |

## 3. 用户/系统场景

### 场景 1：cron 设置

**Given** 用户添加 task `0 9 * * *` 描述「每天 9 点」
**When** 提交
**Then** 入队；下次触发 = 次日 09:00 本地时区

### 场景 2：DST

**Given** 用户时区 America/New_York，任务 `0 2 1 3 *`（3 月 1 日 02:00）
**When** 2027-03-14 spring-forward 当天
**Then** 跳过该日；下月 04-01 02:00 触发

### 场景 3：missed-task queue

**Given** app 关闭 24h
**When** 重启
**Then** 触发最近且未触发的任务；按 trigger 标识幂等

### 场景 4：跳过历史

**Given** task 设置 `0 9 * * *`
**When** 当前时间是 10:00 且今日未触发
**Then** 不补跑今天；下次明日 09:00

## 4. 功能需求

### FR-1 cron parser

支持字段：minute / hour / dom / month / dow。

```go
type Schedule struct {
    Minute  string
    Hour    string
    Dom     string
    Month   string
    Dow     string
    Tz      string // IANA
}

func Parse(expr string) (Schedule, error)
```

### FR-2 timezone

`tz = IANA`，如 `Asia/Shanghai`。运行时统一转换到 wall clock。

### FR-3 DST

```go
func nextFireTime(s Schedule, now time.Time, loc *time.Location) time.Time
```

利用 `time.LoadLocation` + 跳日 / 跳秒处理；spring-forward 当日 skip 该小时，fall-back 不重复。

### FR-4 任务 schema

```sql
CREATE TABLE scheduled_tasks (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    cron_expr       TEXT NOT NULL,
    tz              TEXT NOT NULL,
    action          TEXT NOT NULL,    -- JSON: {'type': 'agent', 'prompt': '...'}
    enabled         INTEGER NOT NULL DEFAULT 1,
    last_run_at     INTEGER,
    last_status     TEXT,
    next_run_at     INTEGER NOT NULL,
    created_at      INTEGER NOT NULL
);
```

### FR-5 trigger 标识

`trigger_id = sha1(task_id + scheduled_for + tz)` 用于幂等去重。

### FR-6 missed-task queue

启动期扫描 `next_run_at <= now` 的 tasks，按 trigger_id 入 `task_runs` 表（unique 触发）。

### FR-7 任务状态

`task_runs.status`：`scheduled / running / ok / failed / skipped`。

### FR-8 UI 设置页

`SettingsPanelScheduledTasks.vue`：列出 + 编辑 + 删除。

### FR-9 ≥ 10 测试场景

| # | 场景 |
|---|---|
| T1 | 解析 cron |
| T2 | 解析无效表达式报错 |
| T3 | 下次触发时间 |
| T4 | DST spring-forward skip |
| T5 | DST fall-back 不重复 |
| T6 | missed-task 入队 |
| T7 | trigger_id 幂等 |
| T8 | skip 已过去任务 |
| T9 | UI 设置页增删 |
| T10 | 时区切换 wall clock |
| T11 | 任务失败重试 |

## 5. 安全与隐私

- action prompt 不进任何日志明文。
- 任务列表本地存储，不上传。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| cron 表达式无效 | fail-fast + UI 提示 |
| tz 不存在 | 失败提示 |
| 任务执行超时 | 状态 failed + reason |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/scheduler/cron.go`（新） | parser |
| `src/darvin-agent/internal/scheduler/store.go`（新） | DB |
| `src/darvin-agent/internal/scheduler/runner.go`（新） | runner |
| `src/darvin-agent/internal/scheduler/dst.go`（新） | DST |
| `src/shared/darvin-api.ts` | 事件 |
| `src/renderer/components/settings/SettingsPanelScheduledTasks.vue` | UI |

## 8. 实施顺序与依赖

1. `cron.go` + `dst.go` 单测（含 8h 时区）
2. `store.go`
3. `runner.go`
4. UI

> 前置：`runtime-supervision`。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go 单测 ≥ 10 条 |
| V3 | `go vet ./...` |
| V4 | `npm run smoke -- scheduled-tasks` |
| V5 | dev 手工：cron `*/1 * * * *` 1min 节奏跑通 |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- Webhook / 外部 trigger（v2）。
