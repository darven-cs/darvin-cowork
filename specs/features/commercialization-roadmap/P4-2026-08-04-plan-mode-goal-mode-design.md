# Plan Mode / Goal Mode 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

商业化场景中很多长任务要求「先告诉我打算怎么做」——agent 不能直接动手。LobsterAI 通过只读 Plan Mode 与 Goal Mode 实现：

- Plan Mode：agent 只读世界 / 给计划
- Goal Mode：plan 通过后逐条执行并报告进度

darvin-cowork 当前未规范。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | Plan Mode 只读：禁止文件写 / network 写 / 持久化副作用 | capability |
| G2 | Plan 输出结构化（JSON / 表格 / 时间线） | format |
| G3 | 用户在 UI 显式「确认执行」 | gate |
| G4 | Goal Mode 进度事件 + 取消 | progress |
| G5 | Plan / Goal 状态机：`drafted / approved / running / done / aborted` | state |
| G6 | 单实例执行（per session） | unique |
| G7 | ≥ 10 测试场景 | tests |

### 1.3 非目标

- 不做多 agent 协同的 Goal Mode。
- 不做 plan 可视化拖拽编辑器（v2）。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/features/agent-context-engine/` | assembler 提供只读 mode 基础 |
| `specs/features/subagent/` | Goal Mode 通过 subagent 实施 |
| `src/darvin-agent/internal/context/` | 占位 |

## 3. 用户/系统场景

### 场景 1：进入 Plan Mode

**Given** 用户在 composer 写「重构整个 auth 模块」
**When** toggle Plan Mode
**Then** agent 进入只读，所有写工具返回 `ErrReadOnlyMode`；plan 草稿呈现在 chat

### 场景 2：plan 起草

**Given** Plan Mode 中
**When** agent 完成计划
**Then** 输出 `plan` artifact，含 steps / ETA / risk

### 场景 3：用户确认

**Given** plan 已完成
**When** 用户点击「执行」
**Then** Plan Mode → Goal Mode；subagent `mode = task` 派生

### 场景 4：进度回报

**Given** Goal Mode 运行中
**When** subagent 推进
**Then** 发送 `goal.progress` 事件，含 step / done / total

### 场景 5：取消

**Given** Goal Mode 运行
**When** 用户点取消
**Then** subagent cancel；plan 状态 `aborted`

## 4. 功能需求

### FR-1 Plan Mode 能力边界

只允许工具：

- `read_file`
- `search_*`
- `git_log`
- `git_diff`
- `bash_run { readonly: true }`（仅 read-only 命令）

禁止：

- `write_file`
- `edit_file`
- `bash_run` 含写操作
- 网络 POST

违反时返回 `ErrCapabilityDenied`。

### FR-2 plan artifact

```json
{
  "type": "plan",
  "id": "plan_abc",
  "summary": "...",
  "steps": [
    { "id": "s1", "title": "...", "tool": "...", "args": {...}, "expectedArtifactId": "..." },
    ...
  ],
  "estimatedSeconds": 600,
  "riskLevel": "low"
}
```

### FR-3 确认执行

UI 在 composer 上方渲染「执行 / 取消 / 修改」三按钮；只有点「执行」才进 Goal Mode。

### FR-4 Goal Mode 进度

```ts
interface DarvinGoalProgressEvent {
  type: 'goal.progress';
  planId: string;
  stepId: string;
  doneCount: number;
  totalCount: number;
}
```

### FR-5 状态机

```go
type PlanStatus string
const (
    PlanDrafted   PlanStatus = "drafted"
    PlanApproved  PlanStatus = "approved"
    PlanRunning   PlanStatus = "running"
    PlanDone      PlanStatus = "done"
    PlanAborted   PlanStatus = "aborted"
)
```

### FR-6 单实例

per session 一次仅一个 plan；并发 plan 进入排队。

### FR-7 ≥ 10 测试场景

| # | 场景 |
|---|---|
| T1 | 进入 Plan Mode |
| T2 | 写工具被禁 |
| T3 | 读工具通过 |
| T4 | plan artifact 格式 |
| T5 | 用户确认 → Goal |
| T6 | 进度事件 |
| T7 | 取消 |
| T8 | 单实例队列 |
| T9 | 失败回退 |
| T10 | plan 持久化 |
| T11 | 重启 plan 状态恢复 |

## 5. 安全与隐私

- Plan 不写明文 secret。
- Plan artifact 持久化在 user workspace。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| Plan 起草失败 | UI 提示「请换 prompt」 |
| Goal Mode 执行超时 | 强制 abort + 提示 |
| 用户离开 session | plan 状态保留 |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/plan/mode.go`（新） | capability gate |
| `src/darvin-agent/internal/plan/plan.go`（新） | plan schema |
| `src/darvin-agent/internal/plan/runner.go`（新） | Goal Mode runner |
| `src/shared/darvin-api.ts` | `goal.*` 事件 |
| `src/renderer/components/PlanCard.vue`（新） | UI 卡片 |
| `src/renderer/composables/usePlanMode.ts`（新） | toggle 状态 |

## 8. 实施顺序与依赖

1. `mode.go` + capability gate 单测
2. `plan.go` + artifact 序列化
3. `runner.go` + subagent 集成
4. UI 卡片

> 前置：`agent-context-engine` 已确认 + `subagent`。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go/TS 单测 ≥ 10 条 |
| V3 | `go vet ./...` |
| V4 | `npm run smoke -- plan-mode-goal-mode` |
| V5 | dev 手工：plan 起草 → 确认 → 进度 |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- 可视化计划编辑器（v2）。
- 多 agent 协同 goal（v2）。
