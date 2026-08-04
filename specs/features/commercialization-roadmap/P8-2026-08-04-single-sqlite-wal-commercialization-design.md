# Single SQLite + WAL — 商业化迭代 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

[`2026-08-01-merge-databases-design.md`](../../refactors/merge-databases/2026-08-01-merge-databases-design.md) 已确立双库 → 单库迁移路径。商业化迭代要：

- 真正落地单 SQLite + WAL
- 提供迁移、回滚、校验
- 引入商用读写并发

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 单一 SQLite DB 文件 + WAL 模式 | file |
| G2 | 表清单：sessions / events / memories / usage / artifacts / dreaming 等 | tables |
| G3 | 启动期迁移 + 自动回滚 | migration |
| G4 | reader / writer 分离 | roles |
| G5 | 不停机校验（PRAGMA integrity_check） | health |
| G6 | ≥ 10 测试场景 | tests |

### 1.3 非目标

- 不引入 distributed DB。
- 不做跨设备 sync（v2）。
- 不动 v1 的 manifest 字段（向后兼容）。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/refactors/merge-databases/2026-08-01-...` | v1 |
| `specs/bugfixes/db-consistency-fixes/` | 双库阶段 |
| `src/darvin-agent/internal/store/migrate.go` | 占位 |

## 3. 用户/系统场景

### 场景 1：迁移

**Given** 用户已运行 v1（双库）
**When** 启动 v2
**Then** 自动检测 schema version；触发迁移；备份回滚点

### 场景 2：回滚

**Given** 迁移中途失败
**When** 回滚脚本启动
**Then** 把双库恢复为唯一来源；显示 v1 配置

### 场景 3：reader / writer 分离

**Given** 10 个 renderer query sessions
**When** 后台大量 writes
**Then** writer 独占；readers 走 WAL snapshot

### 场景 4：health check

**Given** 启动完成
**When** 每 30 分钟检查
**Then** `PRAGMA integrity_check`；失败报警

## 4. 功能需求

### FR-1 单 SQLite 文件

`{userdata}/darvin.db` + `darvin.db-wal` + `darvin.db-shm`。

```go
pragma := []string{
    "journal_mode = WAL",
    "synchronous = NORMAL",
    "foreign_keys = ON",
    "temp_store = MEMORY",
    "cache_size = -32000",
}
```

### FR-2 表清单

```sql
-- 来自各 spec
sessions                 -- session-management
session_events           -- session-management
memories                 -- memory-core
user_memories            -- memory-core
user_memory_sources      -- memory-core
memory_fts               -- memory-core
memory_index_meta_v1     -- memory-bootstrap-agents
bootstrap_files          -- memory-bootstrap-agents
artifacts                -- artifact-panel-commercialization
artifact_shares          -- artifact-panel-commercialization
usage_events             -- cost-and-usage-tracking
dream_jobs               -- memory-dreaming
im_subscriptions         -- im-channel-abstraction
im_webhooks              -- im-channel-abstraction
browser_sessions         -- web-browser-tool
media_jobs               -- media-generation
billing_ledger           -- billing-v1
subscriptions            -- billing-v1
usage_cycles             -- billing-v1
coupons                  -- billing-v1
oauth_tokens             -- oauth-login
enterprise_policies      -- enterprise-config
scheduled_tasks          -- scheduled-tasks-and-cron
budgets                  -- heartbeat-and-cost-control
backup_snapshots         -- sqlite-backup-restore
circuit_states           -- failover-and-circuit-breaker
audit_log                -- observability-and-monitoring
schema_version           -- db-consistency-fixes
```

### FR-3 启动期迁移

```go
func MigrateToSingleSQLite(ctx context.Context, src map[string]string, dst string) error
```

步骤：

1. 备份 v1
2. 创建 v2 schema
3. COPY 数据（分批 1000 条）
4. 校验 row count
5. 切换 manifest
6. 失败回滚

### FR-4 reader / writer

```go
type Store interface {
    Reader() *sql.DB
    Writer() *sql.DB
}
```

writer 单连接；reader pool。

### FR-5 integrity check

```go
func HealthCheck() error {
    rows, _ := db.Query("PRAGMA integrity_check")
    defer rows.Close()
    var status string
    rows.Next()
    rows.Scan(&status)
    if status != "ok" { return ErrDBInconsistent }
    return nil
}
```

### FR-6 WAL 调优

| pragma | value |
|---|---|
| `wal_autocheckpoint` | 1000 |
| `busy_timeout` | 30000 |

### FR-7 ≥ 10 测试场景

| # | 场景 |
|---|---|
| T1 | 单 SQLite 创建 |
| T2 | 表清单 |
| T3 | v1 → v2 迁移 |
| T4 | 迁移中断回滚 |
| T5 | writer 单连接 |
| T6 | reader 并发 |
| T7 | integrity_check |
| T8 | WAL switch |
| T9 | busy_timeout |
| T10 | health check 失败 |
| T11 | manifest 切换 |

## 5. 安全与隐私

- 单 DB 文件权限 0600。
- WAL 文件同步加密（v2）。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| 迁移 IO 错误 | 回滚 + 报警 |
| WAL 文件损坏 | restore backup |
| busy_timeout 超 | retry 3 次 |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/store/sqlite.go`（新） | 单 SQLite 管理 |
| `migrate.go`（新） | v1 → v2 |
| `health.go`（新） | integrity_check |
| `reader.go` | reader 抽象 |
| `writer.go` | writer 抽象 |

## 8. 实施顺序与依赖

1. `sqlite.go` + `health.go`
2. `migrate.go`
3. `reader.go` + `writer.go`

> 前置：`merge-databases/v1` + `db-consistency-fixes`。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go 单测 ≥ 10 条 |
| V3 | `go vet ./...` |
| V4 | `npm run smoke -- single-sqlite-wal-commercialization` |
| V5 | dev 手工：mock 迁移 |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- distributed DB（v2）。
- 跨设备 sync（v2）。
