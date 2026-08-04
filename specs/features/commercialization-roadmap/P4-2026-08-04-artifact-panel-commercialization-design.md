# Artifact Panel — 商业化迭代 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

[`2026-08-01-artifact-panel-design.md`](../artifact-panel/2026-08-01-artifact-panel-design.md) 给出 10 渲染器形态 + 状态机。本文是商业化迭代版：增加 share、persist、cross-session reference。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | artifact 持久化（不仅内存） | persistence |
| G2 | share：复制 markdown / 链接 | share API |
| G3 | cross-session reference | ref |
| G4 | 与 sandbox iframe 安全策略一致 | boundary |
| G5 | 与新 subagent 协作：artifact 可来自 subagent | sender |
| G6 | ≥ 10 测试场景 | tests |

### 1.3 非目标

- 不做云分享链接。
- 不做 artifact 评论 / 协作。
- 不做版本控制系统。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/features/artifact-panel/2026-08-01-...` | v1 |
| `specs/features/artifact-sandbox-iframe/` | sandbox 安全策略 |
| `specs/features/subagent/` | subagent 事件聚合 |

## 3. 用户/系统场景

### 场景 1：artifact 持久化

**Given** 用户退出 app
**When** 重启
**Then** artifact 列表恢复；首次展开在 sandbox 中重新加载

### 场景 2：分享 markdown

**Given** artifact 类别为 markdown
**When** 用户点分享
**Then** 复制 markdown 源到剪贴板（不走云）

### 场景 3：跨 session 引用

**Given** session A 有 artifact X
**When** session B 通过引用 `@X` 提及
**Then** B 的 chat 中展示 X 缩略图；点击切换到 A 打开 X

### 场景 4：subagent artifact

**Given** subagent 在 spawn 期间生成 artifact
**When** main agent 接收
**Then** artifact 自动归属 main agent 的 session

## 4. 功能需求

### FR-1 持久化

```sql
CREATE TABLE artifacts (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL,
    kind        TEXT NOT NULL,  -- html/svg/mermaid/code/...
    title       TEXT NOT NULL,
    content     TEXT NOT NULL,
    metadata    TEXT,           -- json
    created_at  INTEGER NOT NULL
);
CREATE INDEX idx_artifacts_session ON artifacts(session_id);
```

### FR-2 share

```ts
async function shareArtifact(id: string, kind: 'clipboard' | 'markdown' | 'json'): Promise<void>
```

- `clipboard`：通过 Electron clipboard API；kinds = html / text
- `markdown`：仅对 markdown kind
- `json`：所有 kind

### FR-3 cross-session reference

`@X` 语法被 parser 识别：

```ts
interface ArtRef { artifactId: string; sessionId: string }
```

UI 在 chat 中显示缩略图与「跳转」按钮。

### FR-4 subagent 来源

artifact payload 含可选 `sender: { agentId, subagentId }`。归 session 时以 main session 为准。

### FR-5 sandbox 一致性

artifact 渲染仍走 `artifact-sandbox-iframe` spec；本文仅定数据层。

### FR-6 ≥ 10 测试场景

| # | 场景 |
|---|---|
| T1 | artifact 持久化 |
| T2 | 重启恢复 |
| T3 | share clipboard |
| T4 | share markdown 格式 |
| T5 | cross-session reference |
| T6 | @X 解析 |
| T7 | subagent 来源标注 |
| T8 | 删除 artifact |
| T9 | export JSON |
| T10 | undo delete |
| T11 | meta 字段脱敏 |

## 5. 安全与隐私

- artifact 持久化路径 0600。
- share 包含内容脱敏（参考 redact 策略）。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| 持久化失败 | 状态 `lost` + 提示用户 |
| 跨 session 引用 artifact 已删 | UI 显示 `removed` 占位 |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/artifact/store.go`（新） | 持久化 |
| `src/darvin-agent/internal/artifact/reference.go`（新） | 解析 |
| `src/darvin-agent/internal/artifact/share.go`（新） | 分享 |
| `src/shared/darvin-api.ts` | `artifact.*` 扩展 |
| `src/renderer/composables/useArtifactsCommercialization.ts`（新） | UI |
| `src/renderer/components/artifact/ArtifactRefChip.vue`（新） | 引用 chip |

## 8. 实施顺序与依赖

1. `store.go`
2. `reference.go`
3. `share.go`
4. UI

> 前置：`artifact-panel/v1` + `artifact-sandbox-iframe`。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go/TS 单测 ≥ 10 条 |
| V3 | `go vet ./...` |
| V4 | `npm run smoke -- artifact-panel-commercialization` |
| V5 | dev 手工：跨 session reference |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- 完整 v1 重写。
- 云同步（v2）。
