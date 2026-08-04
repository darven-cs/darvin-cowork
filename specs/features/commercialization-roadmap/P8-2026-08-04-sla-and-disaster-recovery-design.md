# SLA & Disaster Recovery 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

商业化承诺 SLA 与灾备：

- 99% crash-free（< 1%/天）
- RPO ≤ 5 分钟（最多丢失 5 分钟数据）
- RTO ≤ 30 分钟（恢复时间 ≤ 30 分钟）

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 24h 滚动 RPO 报告 | report |
| G2 | 自动备份保留 7 天 | retention |
| G3 | 用户手动触发备份 | trigger |
| G4 | 演练入口：恢复沙盒版本 | drill |
| G5 | 灾难手册文档 | runbook |
| G6 | ≥ 10 测试场景 | tests |

### 1.3 非目标

- 不做跨节点灾备（单机本地）。
- 不做合规审计（v2）。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/features/sqlite-backup-restore/` | 备份基础 |
| `specs/features/observability-and-monitoring/` | crash-free |
| `src/darvin-agent/internal/disaster/` | 占位 |

## 3. 用户/系统场景

### 场景 1：RPO 报告

**Given** 用户 24h 使用过 app
**When** 启动
**Then** 设置页显示「最近 24h RPO 估算：3 分钟」（根据最近一份 backup 时间）

### 场景 2：手动备份

**Given** 用户点「立即备份」
**When** 触发
**Then** 写一份完整快照，路径返回

### 场景 3：演练

**Given** 用户点「演练」
**When** 演练启动
**Then** 写到 `/tmp/darvin-drill-<ts>`，UI 显示 diff

### 场景 4：灾难恢复

**Given** 用户硬盘损坏
**When** 重装 app
**Then** 提示「选择最近备份」；按时间戳排序；恢复后回到损坏前一致状态

## 4. 功能需求

### FR-1 RPO 计算

```go
func computeRPO(now time.Time, lastBackup time.Time) time.Duration {
    return now.Sub(lastBackup)
}
```

写入 settings 报告。

### FR-2 备份保留

- 自动周期 24h
- 保留 7 份
- 超出启动期清理

### FR-3 手动触发

`ui.billing.backup.now` + IPC `backup.now`。

### FR-4 演练入口

`drill.run()`：
1. 选最近 backup
2. 复制到 `/tmp/darvin-drill-<ts>`
3. 启动 Go runtime 连该 DB
4. 输出「健康报告」

### FR-5 灾难手册

`docs/runbooks/disaster-recovery.md`：

1. 硬盘损坏
2. 系统重装
3. 找到最近备份（`{userdata}/backups/`)
4. `darvin restore <file>`
5. 验证 sessions / memories 数量

### FR-6 恢复 SLA

```ts
type SlaConfig = {
  rpoTargetMinutes: 5
  rtoTargetMinutes: 30
}
```

实际值与目标值并排显示。

### FR-7 ≥ 10 测试场景

| # | 场景 |
|---|---|
| T1 | RPO 计算 |
| T2 | 备份保留 |
| T3 | 手动触发 |
| T4 | drill |
| T5 | drill 失败回退 |
| T6 | restore from backup |
| T7 | restore 校验 |
| T8 | runbook 文档存在 |
| T9 | RTO 模拟 |
| T10 | 恢复后 sessions 数量 |
| T11 | 备份损坏处理 |

## 5. 安全与隐私

- 演练写到 `/tmp` 不带敏感数据。
- 恢复时校验 schema_version。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| 演练超时 | 提示并保留 drill 文件 |
| 恢复失败 | 双 DB 暂存 |
| 备份文件损坏 | drill 时 fail-fast |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/disaster/rpo.go`（新） | RPO |
| `drill.go`（新） | 演练 |
| `restore.go`（新） | 恢复 |
| `runbook.md`（新） | 文档 |
| `src/renderer/components/settings/SettingsPanelDisaster.vue`（新） | UI |

## 8. 实施顺序与依赖

1. `rpo.go`
2. `drill.go`
3. `restore.go`
4. runbook

> 前置：`sqlite-backup-restore`。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go/TS 单测 ≥ 10 条 |
| V3 | `go vet ./...` |
| V4 | `npm run smoke -- sla-and-disaster-recovery` |
| V5 | dev mock RPO / drill |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- 跨节点灾备（v2）。
- 合规审计（v2）。
