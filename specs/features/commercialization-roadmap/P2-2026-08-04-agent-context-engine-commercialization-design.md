# Agent Context Engine — 商业化迭代 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

[`2026-07-29-agent-context-engine-design.md`](../agent-context-engine/2026-07-29-agent-context-engine-design.md) 已确立 ContextEngine 框架（assembler / policy / afterTurn / preCompaction 等）。本文是商业化迭代版，聚焦以下差距：

- v1 单一 provider 假设。商业化场景需要按 provider budget 切分。
- v1 没考虑 Failover Chain 与 cost-aware compaction 的协作。
- v1 没显式区分 system / user / tool / assistant section 在跨 provider 切换时的 policy 边界。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | provider-aware context budget | per-provider limits |
| G2 | Failover 时 policy 不重写，仅追加 `provider_change_event` | audit |
| G3 | Cost-aware compaction：触发点考虑 token 成本 | trigger |
| G4 | 多 Section 优先级一致（同 v1 强约束） | ordering 测 |
| G5 | ≥ 10 测试场景 | tests |

### 1.3 非目标

- 不修改 v1 已存在的 assembler API 名称（仅扩展 fields）。
- 不新增 CLI flags。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/features/agent-context-engine/2026-07-29-...` | 既有 design（本文以迭代版扩展） |
| `src/darvin-agent/internal/context/engine.go` | 占位接口 |
| `src/darvin-agent/internal/provider/openai/...` | 引入 provider |
| `src/darvin-agent/internal/failover/` | 引入 circuit |

## 3. 用户/系统场景

### 场景 1：provider budget 切换

**Given** 当前 provider=openai + model=gpt-4o（context window=128k）
**When** 用户切换到 gemini-2.5-flash（context window=1M）
**Then** ContextEngine 重新计算 budget = 1M；不重发 history；标记 `provider_change_event`

### 场景 2：Failover 不重写

**Given** openai 出现 5xx circuit 进 OPEN
**When** Failover 切到 gemini
**Then** ContextEngine 不丢 sections，仅追加 `provider_change_event` system note

### 场景 3：cost-aware compaction

**Given** 单条 message 已 100k tokens，cost 估算 > 0.5 USD
**When** 下条 message 触发 pre-compaction
**Then** 触发 compaction 而非等 context window 满

## 4. 功能需求

### FR-1 provider budget

```go
type ProviderBudget struct {
    Provider       string
    Model          string
    ContextWindow  int
    ReservedSystem int
    ReservedTool   int
    CostCeiling    float64 // USD; 0 = unlimited
}
```

加载时与 Provider metadata 一并初始化。

### FR-2 provider_change_event

audit log 一行：

```go
type ProviderChangeEvent struct {
    From        string
    To          string
    Reason      string
    Sessions    []string
}
```

不进入 prompt content，仅 system section 末尾追加短句（前缀 provider_change_only）。

### FR-3 cost-aware pre-compaction

```go
func (e *Engine) ShouldCompact(history []Message) bool
```

trigger 条件：

- tokens > 0.7 * ContextWindow
- 估算 cost 增量 > CostCeiling * 0.1
- 工具返回大量 content

### FR-4 section priority

沿用 v1：system > tool > user > assistant。当切换 provider 时，新 provider 的 system prompt 套用同优先级，但旧 provider 的 system prompt 不丢（写为 `system.previous` section，限时保留）。

### FR-5 测试场景 ≥ 10

| # | 场景 |
|---|---|
| T1 | 切 provider budget 重建 |
| T2 | Failover 不重写 |
| T3 | provider_change_event |
| T4 | cost-aware trigger |
| T5 | section 优先级同 v1 |
| T6 | system.previous 保留 |
| T7 | 大 message 触发 compaction |
| T8 | 工具返回不污染 prompt |
| T9 | afterTurn hook 仍生效 |
| T10 | pre-compaction 一致性 |
| T11 | 多 provider 并发 budget |

## 5. 安全与隐私

- system.previous section 内容不进 cost 表。
- provider_change_event 不含 prompt content。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| Provider metadata 缺 | 沿用 default 128k |
| cost 估算溢出 | saturate at cap |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/context/budget.go`（新） | ProviderBudget |
| `src/darvin-agent/internal/context/engine.go` | 增加 cost-aware trigger |
| `src/darvin-agent/internal/context/provider_event.go`（新） | provider_change_event |
| tests | ≥ 10 场景 |

## 8. 实施顺序与依赖

1. `budget.go` + Provider 集成
2. `provider_event.go` + audit
3. cost-aware trigger
4. 测试

> 前置：`specs/features/agent-context-engine/v1` 已确认 + `provider-registry/`。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go 单测 ≥ 10 条 |
| V3 | `go vet ./...` |
| V4 | `npm run smoke -- agent-context-engine-commercialization` |
| V5 | dev 手工：切 provider 不中断 session |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- 完整重写 ContextEngine（沿用 v1）。
- 跨 session context 聚合（v2）。
