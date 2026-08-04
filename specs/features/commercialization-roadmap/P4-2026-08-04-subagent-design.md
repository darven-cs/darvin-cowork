# Subagent 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 问题

商业化路线图要求支持 subagent：模型可以主动派生一个轻量子 agent 跑子任务（搜索 / 工具调用 / 后台作业）。darvin-cowork 当前没有 subagent 概念。

LobsterAI 有 `agents-default!spawn` / `task` / `scope` 三种模式。本 spec 沿用：

- `spawn`：长生命周期子 agent，独占 workspace
- `task`：短任务，复用当前 agent 的工具
- `scope`：最小职责，仅一次工具调用

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 三模式 spawn / task / scope | enum |
| G2 | subagent_id 全局唯一 + 事件聚合 | uuid |
| G3 | 取消 / 资源上限 / 错误隔离 | lifecycle |
| G4 | Subagent 输出回流主 agent（最终答案） | return |
| G5 | Subagent 自带 quota / context window 独立 | isolation |
| G6 | 与 dreaming / heartbeat 解耦 | boundaries |
| G7 | ≥ 10 测试场景 | tests |

### 1.3 非目标

- 不做跨进程的 subagent（仅主进程内派生）。
- 不实现 subagent 可视化编排（独立 spec / v2）。
- 不支持递归 subagent（subagent 内不可再 spawn）。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/refactors/per-session-acp-agent/` | per-session ACP 提供 agent loop 容器 |
| `specs/features/agent-acp-loop/` | 早期 spec |
| `src/darvin-agent/internal/agent/` | 计划承载 |

## 3. 用户/系统场景

### 场景 1：spawn

**Given** agent loop 决定派生 long-lived subagent
**When** `agents.defaults.spawn(name, prompt)` 触发
**Then** 派生独立 agent 实例，独占新 workspace；事件 `subagent.spawned` 推送；UI 在侧面板显示 subagent 列表

### 场景 2：task

**Given** 主 agent 调用临时工具
**When** `agents.defaults.task(prompt)` 触发
**Then** 新增短任务，复用 workspace；完成后销毁

### 场景 3：scope

**Given** 仅做一次工具调用
**When** `agents.defaults.scope(toolName, args)` 触发
**Then** 同步执行一次工具调用；不建任务，不入事件总线

### 场景 4：取消

**Given** subagent 仍在跑
**When** `subagent.cancel(subagent_id)`
**Then** 通知 worker 优雅退出；状态 `cancelled`；事件 `subagent.cancelled` 推送

### 场景 5：资源上限

**Given** subagent 已达 5 分钟或 10 tool calls
**When** runtime 检测
**Then** 自动 cancel 并发 `subagent.timeout` 事件

## 4. 功能需求

### FR-1 模式枚举

```go
type SpawnMode string
const (
    ModeSpawn SpawnMode = "spawn"
    ModeTask  SpawnMode = "task"
    ModeScope SpawnMode = "scope"
)
```

### FR-2 spawn 实现

```go
type SpawnRequest struct {
    Mode     SpawnMode
    Name     string
    Prompt   string
    Tools    []string   // 受限工具白名单
    Timeout  time.Duration
    Quota    Quota      // 上下文 + token 预算
}

type Subagent struct {
    ID         string
    Mode       SpawnMode
    ParentID   string
    State      string
    Events     chan Event
    Result     chan Result
}
```

派生 worker：

```go
func Spawn(ctx context.Context, req SpawnRequest) (*Subagent, error)
```

### FR-3 agent_id 聚合

所有 subagent 事件带 `agent_id` 字段；UI 通过 `agent_id` 聚合到「侧边栏 / session」视图。

### FR-4 取消 / 资源

```go
func (s *Subagent) Cancel(reason string)
```

timeout 走 `context.WithTimeout`；tool count 走 runtime quota checker。

### FR-5 错误隔离

subagent panic → state `crashed`；主 agent 不 panic，继续；UI 显示 `crashed` 徽章。

### FR-6 结果回流

scope / task 完成后返回 `Result.Value` 给主 agent；spawn 长生命周期不直接回流。

### FR-7 边界

与 dreaming / heartbeat：

- dreaming 不可 spawn
- heartbeat 不可 spawn
- subagent 不可再 spawn

### FR-8 ≥ 10 测试场景

| # | 场景 |
|---|---|
| T1 | spawn 长生命周期 |
| T2 | task 短任务 |
| T3 | scope 一次性 |
| T4 | 取消 |
| T5 | timeout 自动取消 |
| T6 | tool count 超限 |
| T7 | panic 不影响主 agent |
| T8 | result 回流 |
| T9 | event 聚合到 agent_id |
| T10 | 不允许递归 spawn |
| T11 | quota 超限 |

## 5. 安全与隐私

- Subagent 默认 tools 白名单（不动 filesystem，禁用 `bash_run` 除外）。
- Subagent 不继承 parent 凭证；只持有 scoped key。
- Subagent 写文件必须走 darvin 的工作区权限策略。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| Subagent worker 不响应 | 用 `Cancel` + 状态 `unresponsive` |
| Parent agent 终止 | Subagent 自动 cancel |
| 工具名重复 | 返回 `ErrSpawnInvalid` |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/subagent/spawn.go`（新） | Spawn |
| `src/darvin-agent/internal/subagent/manager.go`（新） | Subagent 全局 Manager |
| `src/darvin-agent/internal/subagent/worker.go`（新） | worker pool |
| `src/darvin-agent/internal/subagent/quota.go`（新） | quota |
| `src/shared/darvin-api.ts` | `subagent.*` 事件 |
| `src/renderer/components/subagent/SubagentPanel.vue`（新） | UI 列表 |

## 8. 实施顺序与依赖

1. `quota.go` + 单元测试
2. `spawn.go` + 单测（≥ 10 场景）
3. `worker.go` + 并发
4. UI 面板 + events

> 前置：`per-session-acp-agent` 已确认 + `runtime-supervision`。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go 单测 ≥ 10 条 |
| V3 | `go vet ./...` |
| V4 | `npm run smoke -- subagent` |
| V5 | dev 手工：spawn → 取消 → UI 显示状态 |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- Subagent UI 编排器（v2）。
- 跨进程 subagent（v2）。
