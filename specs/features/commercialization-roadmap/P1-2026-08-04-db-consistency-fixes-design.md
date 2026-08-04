# DB Consistency Fixes 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 问题

当前架构存在多 SQLite 库现状：

- `sessions.sqlite`：会话 / 事件 / metadata
- `memories.sqlite`（Memory Subsystem spec 提及）：FTS5 + user_memories
- 旧版 metadata JSON（`.bak/` 与历史）可能与 DB 字段漂移

潜在一致性问题：

1. 单写者边界不明确：万一 Electron 主进程与 Go runtime 同时写同一库，可能丢写。
2. drift 检测缺位：DB schema 与 Go struct 在重构后跑偏。
3. resync 操作可能中途崩溃导致半态。
4. LobsterAI 已经验证过双库 → 单库迁移路径（`merge-databases/2026-08-01`），本文档聚焦一致性本身的修复。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 双库单写者仲裁：每个 SQLite 文件仅一个 writer 协程 | `db/writer.go` mutex |
| G2 | 启动期 drift 检测：DB schema 与预期 schema 对齐 | `db/drift.go` + `pragma schema_version` |
| G3 | resync 原子性：失败可回滚到上一个 snapshot | 事务 + rename-swap |
| G4 | 失败恢复：write 协程 panic 后由 watcher 重启 | 复用 runtime-supervision |
| G5 | 验收：双库仲裁 100% 一致；24h soak 无 drift | smoke 用例 + soak |

### 1.3 非目标

- 不合并 SQLite 库（由 `specs/refactors/merge-databases/` 主理，P8 出商业化迭代）。
- 不引入新的数据库引擎（仅 SQLite）。
- 不修改既有 schema（除非为补 drift 校验字段）。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `src/darvin-agent/internal/store/` | 已规划存根；具体文件未实现 |
| `specs/refactors/merge-databases/2026-08-01-merge-databases-design.md` | 给出双库 → 单库迁移路径 |
| `./P3-2026-08-04-memory-core-design.md` | `MemoriesDBName = "memories.sqlite"` |
| `src/shared/darvin-api.ts` | 无 DB schema 描述字段 |

## 3. 用户/系统场景

### 场景 1：崩溃后回滚

**Given** resync 事务进行到一半，Go runtime 突然 panic
**When** 主进程拉起新子进程
**Then** 重启后 `db.migration.state` 表表明上次迁移失败，触发 `db.resync.resume`；并保留上一份 snapshot

### 场景 2：drift 检测触发告警

**Given** DB schema version 与 `schema_version` 表记录不一致
**When** 启动期读出 `pragma user_version`
**Then** 触发 `db.drift.detected` 事件，UI 提示「数据库结构已偏离，将进入只读模式」

### 场景 3：双写者竞态

**Given** 主进程与 Go runtime 尝试写同一 SQLite 文件
**When** SQLite 命中 `SQLITE_BUSY` 30s
**Then** 写者协程自动回退到「等待并重试 3 次」，3 次后抛出 `db.write.failed`，状态机进入 `db-degraded`

## 4. 功能需求

### FR-1 单写者仲裁

```go
// db/writer.go
type Writer struct {
    filename string
    mu       sync.Mutex
    queue    chan writeOp
}

func (w *Writer) Run(ctx context.Context) {
    for {
        select {
        case <-ctx.Done(): return
        case op := <-w.queue:
            w.mu.Lock()
            w.apply(op)
            w.mu.Unlock()
        }
    }
}
```

所有外部 caller 仅通过 `Submit(op)` 提交；不直接持有 `*sql.DB`。

### FR-2 drift 检测

启动期：

1. 读 `pragma user_version`，对照预期 `schema_version`（来自编译期常量 `CurrentSchemaVersion`）。
2. 不一致则发 `db.drift.detected` 事件并把 DB 切 `read-only`。
3. UI 显示「需要迁移」，引导用户走 `db.drift.repair`。

### FR-3 resync 原子性

```go
// db/resync.go
func (m *Manager) Resync(ctx context.Context) error {
    snap := m.snapshot()
    defer m.cleanupSnap(snap)

    tx, err := m.db.BeginTx(ctx, nil)
    if err != nil { return err }

    if err := m.applyResync(ctx, tx); err != nil {
        tx.Rollback()
        return err
    }

    if err := tx.Commit(); err != nil {
        m.restoreFromSnap(snap) // rename-swap 回滚
        return err
    }
    return nil
}
```

### FR-4 失败恢复

- `db.Writer.Run` 捕获 panic，发送 `db.writer.panic` 事件，重启协程并把队列里的 op 移到 retry 队列。

### FR-5 schema_version 表

```sql
CREATE TABLE schema_version (
    key       TEXT PRIMARY KEY,
    value     INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
```

所有 schema 变更必须更新此表。

## 5. 安全与隐私

- SQLite 文件权限 `0600`，仅当前用户可读写。
- 不在 DB 中存任何明文密钥或 refresh token。
- `pragma journal_mode = WAL` 共享给多 reader，但写仍由单协程托管。
- `db.drift.repair` 需要用户在设置页点击确认后才能触发，不自动执行。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| 写者协程 panic | Watcher 重启；queue op 移到 retry |
| `resync` 期间 DB 文件损坏 | 自动 restore snapshot 并发事件给 UI |
| `pragma user_version` 与 `schema_version` 表冲突 | 以 `schema_version` 表为准 |
| snapshot 文件本身损坏 | 报 `db.snapshot.corrupted`，由备份恢复接管 |
| 多 goroutine 同时 queue op | 全部走 `Submit`；先来先服务 |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/db/writer.go`（新） | 单写者协程 + queue |
| `src/darvin-agent/internal/db/drift.go`（新） | drift 检测 + 事件 |
| `src/darvin-agent/internal/db/resync.go`（新） | snapshot + 事务 + 回滚 |
| `src/darvin-agent/internal/db/migrate.go`（新） | 版本化迁移 |
| `src/shared/darvin-api.ts` | `db.drift.*` channel + `DbState` 类型 |
| `src/renderer/services/db-state.ts`（新） | composable：显示只读 banner |

## 8. 实施顺序与依赖

1. 先建 `db.Writer` 与单测，并发竞态用例 ≥ 5 条。
2. 加 `schema_version` 表 + 启动期 drift 检测。
3. 加 `Resync` 流程，加单元测试覆盖「半途 panic → snapshot 还原」。
4. 接入 runtime-supervision 的 watcher，捕获 panic 自动重启。
5. UI 端只读 banner + drift 提示。

> 前置：`specs/refactors/merge-databases/2026-08-01-...` 已存在。
> 并行：`specs/features/runtime-supervision/` 共享 watcher。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过（仅 TS 侧） |
| V2 | Go 单元测试覆盖 5 状态 + 5 场景（崩溃回滚 / drift / 队列重试） |
| V3 | 启动期 drift 检测生效：手动改 `pragma user_version` 触发 banner |
| V4 | `npm run smoke -- db-consistency` 通过 |
| V5 | 24h soak：每 30 分钟运行一次 drift 检测，0 false positive |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收完成同步 `CHECKLIST.md` |

## 10. 不在范围

- 真正的多库合并迁移在 `merge-databases/` 与 `single-sqlite-wal-commercialization/`（P8）中；本文只是「双库阶段的一致性修复」。
- 凭证加密 vault 不在本文。
