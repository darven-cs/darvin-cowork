# SQLite Backup & Restore 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

商业化必须保证数据库完整性：用户升级时面临 schema migration 失败风险；硬件损坏需要从备份恢复。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 一致性备份：使用 SQLite `.backup` API | API |
| G2 | 24h 滚动保留 7 份 | retention |
| G3 | 跨平台路径（Darwin / Linux / Windows） | path |
| G4 | 手动备份按钮 | settings |
| G5 | 恢复演练入口（沙盒） | drill |
| G6 | 加密备份（gpg / age） | encryption |
| G7 | ≥ 10 测试场景 | tests |

### 1.3 非目标

- 不做云备份（v2）。
- 不做差异备份（仅 full）。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/refactors/merge-databases/` | DB 基础 |
| `specs/bugfixes/db-consistency-fixes/` | writer / migration |
| `src/darvin-agent/internal/store/` | 占位 |

## 3. 用户/系统场景

### 场景 1：自动备份

**Given** 系统时间已过 24h 上次备份
**When** scheduler 触发
**Then** 写一份到 `backups/2026-08-04T03-00.db.gz.age`

### 场景 2：手动备份

**Given** 用户在 settings 点「立即备份」
**When** 提交
**Then** 立即写一份；返回文件路径

### 场景 3：恢复演练

**Given** 用户点「演练」
**When** 提交
**Then** 把备份恢复到 `/tmp/darvin-drill-<ts>` 不覆盖主 DB；UI 列出可恢复快照

### 场景 4：加密

**Given** 用户启用了 backup encryption
**When** 备份写入
**Then** 走 `age` 加密；密钥 derived from keychain

## 4. 功能需求

### FR-1 备份 API

```go
func (m *Manager) Backup(ctx context.Context, dst string) error {
    return m.db.Backup(dst) // SQLite 内置
}
```

不锁表，依赖 SQLite WAL 一致性。

### FR-2 retention

```go
retention := struct {
    MaxAge     time.Duration // 7d
    MaxCount   int           // 7
}
```

启动期清理过期备份。

### FR-3 路径

```go
backupDir := filepath.Join(userDataDir, "backups")
```

`{userDataDir}/backups/2026-08-04T03-00:00.db.gz[.age]`。

### FR-4 手动触发

UI 按钮调用 `BackupNow` IPC。

### FR-5 恢复演练

`BackupDrill(dst)`：

- 选最近一份
- 写到 dst
- 不修改主 DB
- 返回 drill report

### FR-6 加密

`age` library：recipient `age1darvin...` (X25519)。密钥 derived from macOS Keychain / Linux secret-service / Win DPAPI。

### FR-7 校验

每次恢复前 `PRAGMA integrity_check`。失败抛 `ErrBackupCorrupted`。

### FR-8 ≥ 10 测试场景

| # | 场景 |
|---|---|
| T1 | 自动备份 |
| T2 | 手动备份 |
| T3 | retention 清理 |
| T4 | 跨平台路径 |
| T5 | 不锁表 |
| T6 | 加密备份 |
| T7 | 解密还原 |
| T8 | integrity_check |
| T9 | drill 演练 |
| T10 | 备份失败重试 |
| T11 | 并发备份互斥 |

## 5. 安全与隐私

- 加密密钥不存 SQLite。
- 备份文件 0600。
- 上传云（v2 时）必须用户显式确认。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| 磁盘满 | warning + skip |
| 备份 API 报错 | UI 提示 |
| 密钥丢失 | 备份不可读（设计预期） |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/backup/backup.go`（新） | 主备份 |
| `src/darvin-agent/internal/backup/encrypt.go`（新） | age |
| `src/darvin-agent/internal/backup/retention.go`（新） | retention |
| `src/darvin-agent/internal/backup/drill.go`（新） | 演练 |
| `src/shared/darvin-api.ts` | 通道 |
| `src/renderer/components/settings/SettingsPanelBackup.vue`（新） | UI |

## 8. 实施顺序与依赖

1. `backup.go` + `retention.go`
2. `encrypt.go`
3. `drill.go`
4. UI

> 前置：`db-consistency-fixes`。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go 单测 ≥ 10 条 |
| V3 | `go vet ./...` |
| V4 | `npm run smoke -- sqlite-backup-restore` |
| V5 | dev 手工：演练显示可恢复 |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- 云备份（v2）。
- 差异备份（v2）。
