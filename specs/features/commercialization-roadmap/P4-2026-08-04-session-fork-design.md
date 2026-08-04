# Session Fork 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

商业化用户经常需要：从某次历史步骤分叉出并行探索 / 退一步试不同方案。LobsterAI 通过 session DAG 实现：每个 session 是一个节点，分叉创建新节点 + 共享前缀边。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | session DAG 数据模型 | schema |
| G2 | 分叉时复制 user / assistant / tool 事件至分叉点 | copy |
| G3 | 附件 / 记忆引用语义：深拷贝 attachments，引用 memory（不变性） | semantics |
| G4 | 可选 Git worktree 生命周期 | optional |
| G5 | session 关系图 UI | graph view |
| G6 | ≥ 10 测试场景 | tests |

### 1.3 非目标

- 不做 session 合并（v2）。
- 不做冲突解决（v2）。
- 不做跨设备 fork sync（v2）。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/features/session-management/2026-08-01-...` | session 基础 |
| `src/darvin-agent/internal/store/` | 计划承载 session 表 |

## 3. 用户/系统场景

### 场景 1：分叉

**Given** session A 已经 5 轮对话
**When** 用户在第 3 轮点「分叉」
**Then** 新 session B 拥有前 3 轮内容；第 4 / 5 轮后续不复制

### 场景 2：附件

**Given** session A 第 2 轮上传文件 X
**When** fork 到 B
**Then** B 也含 X 的独立副本（深拷贝）；不影响 A

### 场景 3：memory 引用

**Given** session A 引用 memory id=M123
**When** fork 到 B
**Then** B 也引用 M123（不变性，不复制 memory row）

### 场景 4：worktree

**Given** session A 在 git worktree `wt-A`
**When** fork 时勾选「在 worktree 中」
**Then** B 派生 wt-B；wt-A 与 wt-B 互不影响

### 场景 5：关系图

**Given** 用户打开关系图
**When** 显示
**Then** 节点为 session，边为 fork 关系；点击节点切换 session

## 4. 功能需求

### FR-1 DAG 数据模型

```sql
CREATE TABLE sessions (
    id              TEXT PRIMARY KEY,
    parent_id       TEXT,           -- 上一节点
    forked_from_id  TEXT,           -- 分叉源节点
    split_at_event  TEXT,           -- 分叉点事件 ID
    title           TEXT,
    created_at      INTEGER,
    updated_at      INTEGER,
    workspace_path  TEXT,
    worktree_path   TEXT
);

CREATE TABLE session_events (
    id              TEXT PRIMARY KEY,
    session_id      TEXT NOT NULL,
    parent_event_id TEXT,           -- 同一 session 内的事件前驱
    seq             INTEGER NOT NULL,
    -- ...
);
```

fork 时复用 `seq` ≤ split_at_event.seq。

### FR-2 复制策略

- `events`：≤ split_at_event.seq 全复制；新 session_id 起算
- `attachments`：深拷贝（独立 row / 独立文件）
- `memory_refs`：仅引用，原 memory 不动
- `subagent_events`：分叉点之后不复制

### FR-3 worktree

```bash
git worktree add ./workspaces/{sessionId} -b session/{sessionId}
```

fork 弹出 toggle 让用户选 `auto / no / yes`。

### FR-4 关系图

UI 组件 `<SessionGraph>` 通过 IPC `session.list` + `session.parent_id` 构建；DAG render 使用 force-directed。

### FR-5 ≥ 10 测试场景

| # | 场景 |
|---|---|
| T1 | fork 创建新 session |
| T2 | 复制 events 边界 |
| T3 | 附件深拷贝 |
| T4 | memory 引用不变 |
| T5 | 无附件 fork |
| T6 | worktree 创建 |
| T7 | 双 worktree 互不影响 |
| T8 | 关系图渲染 |
| T9 | 删除 branch 不影响 parent |
| T10 | 并发 fork 安全 |
| T11 | split_at_event 边界正确 |

## 5. 安全与隐私

- fork 不复制任何 secret 类型附件。
- 新 session_id 与 parent 不可枚举。
- 附件文件独立权限 0600。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| git worktree 失败 | 不创建 worktree |
| 附件过大 | 提示并跳过 |
| 同名 session | uuid v7 防冲突 |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/session/fork.go`（新） | Fork |
| `src/darvin-agent/internal/session/worktree.go`（新） | Git worktree |
| `src/darvin-agent/internal/store/migrate.go` | migrations 增列 |
| `src/shared/darvin-api.ts` | `session:fork` 通道 |
| `src/renderer/components/SessionGraph.vue`（新） | 关系图 |
| `src/renderer/composables/useSessionFork.ts`（新） | fork state |

## 8. 实施顺序与依赖

1. schema migration
2. fork.go
3. worktree.go
4. UI

> 前置：`session-management` 已确认。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go/TS 单测 ≥ 10 条 |
| V3 | `go vet ./...` |
| V4 | `npm run smoke -- session-fork` |
| V5 | dev 手工：分叉 + 关系图 |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- session 合并（v2）。
- 冲突解决（v2）。
