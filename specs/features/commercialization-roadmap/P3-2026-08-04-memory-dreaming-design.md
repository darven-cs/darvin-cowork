# Memory Dreaming 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

LobsterAI 的 dreaming 是后台异步任务，在用户不在线 / 后台运行时把记忆重新组织、压缩、归类、生成 DREAMS.md。darvin-cowork 已经在 Settings 占位 dreaming tab，本次正式 spec 化：

- 三阶段触发点（Light / Deep / REM）
- 离线 / 断网 / 关机补跑
- Subagent 隔离
- DREAMS.md 输出格式

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 三阶段触发点：Light（每日）/ Deep（每周）/ REM（每月） | scheduler |
| G2 | 任务状态机：`pending / running / ok / failed / skipped` | state machine |
| G3 | 原子离线队列（SQLite 表） | queue |
| G4 | 断网 / 关机后补跑 | resume |
| G5 | 幂等键：`sessionId + phase + n` | idempotent |
| G6 | 智能调度：用户使用高峰期降级 / 夜间优先 | priority |
| G7 | Subagent 隔离：dreaming 内部生成内容不进 user memory | boundary |
| G8 | DREAMS.md 输出格式 | format |
| G9 | 资源预算（CPU / memory budget） | limit |
| G10 | 隐私过滤（不写明文 secret 到 DREAMS.md） | filter |

### 1.3 非目标

- 不做分布式 dreaming（单机本地）。
- 不做 dreaming 加密货币化。
- 不替代 ContextEngine compaction（沿用其触发）。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `./P3-2026-08-04-memory-core-design.md` | Memory core / FTS5 / status 字段 |
| `./P3-2026-08-04-memory-extract-cleanup-design.md` | AutoExtract 已有 filter |
| `./P3-2026-08-04-memory-renderer-design.md` | Settings dreaming tab 占位 |
| `specs/features/context-compaction-ui/v1` | manual / auto / preview 三模式 |
| `specs/features/runtime-supervision/` | spawn / state 机制可复用 |

## 3. 用户/系统场景

### 场景 1：Light 每日触发

**Given** 用户历史 24h 有 ≥ 1 个新 memory entry
**When** Light 任务在 idle 时跑
**Then** 对新 entry 做归类（去噪 / 近重复合并），状态 = ok

### 场景 2：Deep 每周触发

**Given** 7 天累计
**When** Deep 任务跑
**Then** 对全部 memory 做向量化（仅 schema） + 重新聚类（k-medoids，k 自适应）

> 仅 schema：实际向量化实接由 usage-analytics / embedding 提供方决定；本 spec 不动 Embedding Provider 接口（与 LobsterAI 一致，字段保留不实接）。

### 场景 3：REM 每月触发

**Given** 30 天
**When** REM 跑
**Then** 生成 `DREAMS.md`：本月新增 / 修改 / 合并 / stale 4 类总结；不写原文

### 场景 4：断网补跑

**Given** REM 任务开始但网络中断
**When** 重连
**Then** 幂等键命中，从断点续跑；不重做前阶段

### 场景 5：关机补跑

**Given** 设置 `dreaming.runOnStartup=true`
**When** 用户启动 app
**Then** 检查最后一次 REM，若 > 30 天则补跑

### 场景 6：用户高峰期降级

**Given** 系统检测用户在 idle 超过 30 分钟
**When** dreaming scheduler 决定
**Then** 在用户 idle 期跑；高峰期只跑 Light，且 5 分钟限时

## 4. 功能需求

### FR-1 三阶段触发

| 阶段 | 频率 | 默认时间 |
|---|---|---|
| Light | 每日 1 次 | 系统 idle / 用户主动 |
| Deep | 每周 1 次 | 周日 03:00（用户可改） |
| REM | 每月 1 次 | 月初 03:00 |

### FR-2 任务状态机

```go
type DreamPhase string
const (
    PhaseLight DreamPhase = "light"
    PhaseDeep  DreamPhase = "deep"
    PhaseREM   DreamPhase = "rem"
)

type DreamStatus string
const (
    StatusPending   DreamStatus = "pending"
    StatusRunning   DreamStatus = "running"
    StatusOk        DreamStatus = "ok"
    StatusFailed    DreamStatus = "failed"
    StatusSkipped   DreamStatus = "skipped"
)
```

### FR-3 原子离线队列

```sql
CREATE TABLE dream_jobs (
    id          INTEGER PRIMARY KEY,
    phase       TEXT NOT NULL,
    idempotent_key TEXT NOT NULL UNIQUE, -- sha1(phase + windowStart)
    window_start INTEGER NOT NULL,
    window_end   INTEGER NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending',
    attempts     INTEGER NOT NULL DEFAULT 0,
    last_error   TEXT,
    started_at   INTEGER,
    finished_at  INTEGER
);
```

入队 / 出队走 `db.Writer` 单写者。

### FR-4 幂等键

```go
key := sha1(phase + "|" + windowStart.Format("2006-01-02"))
```

windowStart 跨阶段：

- Light: 每日 00:00 本地时区
- Deep: 每周日 00:00
- REM: 每月 1 日 00:00

### FR-5 智能调度

```go
type Scheduler struct {
    mu        sync.Mutex
    lastUserActivity time.Time
}

func (s *Scheduler) Priority(now time.Time) Priority
```

按用户活动 idle 时长 + cron 期望时间：

- idle > 30min → High
- 03:00–05:00 → High
- 其他时间白天 → Low

### FR-6 Subagent 隔离

Dreaming 输出通过独立 `subagent` ID = `dreaming-{phase}`：

- 写 user memory? **否**（写入 dream memory）
- 读 user memory? **是**（只读）
- 读 settings? **是**（仅 dreaming.* 配置）

dream memory 与 user memory 物理隔离（不同表 / 不同 SQLite 文件均可）。

### FR-7 DREAMS.md 输出

```markdown
# Dream Diary — 2026-08

## 添加
- 2026-08-02 14:32: 用户偏好 shell 用 fish
- 2026-08-03 09:11: 用 Mongo 做主库

## 修改
- 2026-08-01: 「喜欢 coffee」改为「早上喝美式」

## 合并
- 「习惯用 vim」与「用 neovim」合并为「主编辑器 neovim」

## Stale
- 180 天前的 23 条 entry 因超过 capacity 进 stale

## 错误
- (无)
```

### FR-8 资源预算

```go
type Budget struct {
    MaxDuration   time.Duration
    MaxCPUPercent int
    MaxMemoryMB   int
    MaxTokens     int
}
```

每个 phase 默认：

| Phase | duration | CPU | Mem | tokens |
|---|---|---|---|---|
| Light | 5min | 30% | 256MB | 10k |
| Deep | 30min | 50% | 512MB | 50k |
| REM | 60min | 60% | 1GB | 100k |

超出 → 状态 `failed` + reason `budget`。

### FR-9 隐私过滤

写入 DREAMS.md 前正则过滤：

- `(?i)(api[-_]?key|secret|password|token)` → `***`
- email → `***@***`
- 长数字串（>=12 位） → 截断

### FR-10 测试场景 ≥ 10

| # | 场景 |
|---|---|
| T1 | Light 入队 / 出队 |
| T2 | 跨天幂等命中 |
| T3 | Deep 跨周幂等命中 |
| T4 | REM 跨月幂等命中 |
| T5 | 状态机切换 |
| T6 | 断网后 resume |
| T7 | 关机后 startup 补跑 |
| T8 | user idle priority |
| T9 | 资源预算超限 → failed |
| T10 | Subagent 边界（不写 user memory） |
| T11 | DREAMS.md 输出格式 |
| T12 | 隐私过滤命中 |
| T13 | 并发 dream job |

## 5. 安全与隐私

- dreaming 输出不上传任何 user content。
- DREAMS.md 文件权限 0600，归 user workspace 目录。
- 任何 schema 校验失败的 memory，dreaming 不写 DREAMS.md，记录日志。
- dreaming 不影响 user memory 的真实写入（顺序：先 user write → 后 dreaming read）。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| Worker panic | watcher 隔离；job 回退 pending |
| 磁盘满 | 状态 failed + reason `disk_full` |
| settings 误关 dreaming | 用户显式重启；不静默重跑 |
| 并发 schema 升级期间 dreaming 入队 | 等 schema_version 同步 |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/dream/queue.go`（新） | dream_jobs 表 + Writer |
| `src/darvin-agent/internal/dream/scheduler.go`（新） | 触发点 + Priority |
| `src/darvin-agent/internal/dream/phases.go`（新） | Light/Deep/REM |
| `src/darvin-agent/internal/dream/subagent.go`（新） | Subagent 隔离 |
| `src/darvin-agent/internal/dream/diary.go`（新） | DREAMS.md 渲染 |
| `src/darvin-agent/internal/dream/redact.go`（新） | 隐私过滤 |
| `src/shared/darvin-api.ts` | `dream.*` 事件 |
| `src/renderer/services/i18n.ts` | 新增 dreaming i18n key |

## 8. 实施顺序与依赖

1. `queue.go` + `phases.go` + 单测
2. `scheduler.go` + priority
3. `subagent.go` 边界
4. `diary.go` + `redact.go`
5. Settings UI + i18n

> 前置：`memory-core` / `memory-extract-cleanup` / `runtime-supervision` 已确认。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go 单测 ≥ 13 条 |
| V3 | `go vet ./...` |
| V4 | `npm run smoke -- memory-dreaming` |
| V5 | dev 手工：mock 触发 3 phase 各 1 次，状态切换正确 |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- 向量化聚类实接（v2，由 Embedding provider 主理）。
- DREAMS.md 自动发往用户邮箱（v2）。
- LLM-judge 自动归类（v2）。
