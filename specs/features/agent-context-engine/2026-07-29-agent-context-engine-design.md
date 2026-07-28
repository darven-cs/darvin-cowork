# Agent 上下文引擎（Context Engine）设计文档

## 1. 概述

### 1.1 问题 / 背景

darvin-agent 的 Agent 运行时（M2，`agent-loop` spec 已落地）在 `executor.RunConversation` 第 70 行留了一个明确的接缝：

```go
// TODO(spec: future-context-engine): replace
// direct session snapshot with ContextEngine.Assemble()
messages := d.Session().Messages()
```

当前实现**直接把整段 session 消息送进 LLM**。这对短会话没问题，但有以下限制：

1. **无 token 预算管理**：长会话（多轮 + 多次 tool 调用）下 messages 累积会撞模型 context window，模型报 `length` 或 `internal_error` 后 Agent 直接挂掉。
2. **无压缩**：保留所有历史（包括过期 tool result、长文本片段）会持续膨胀 prompt。
3. **与 session.Session 绑死**：`session.Messages()` 返回最近一次写时的拷贝，`agent-loop §1.3` 明确把 ContextEngine 列在 non-goal 里。
4. **组装层缺失 OpenClaw 设计的"语义角色"**：未来接入 Skills / Memory / MCP 时，LLM 需要的 system prompt 注入（`<available_skills>` / 记忆相关事实块 / MCP 服务器描述）需要一个统一的 `assemble()` 出口，而不是在 `executor` 里塞 if-cascade。

`docs/agent/02_CONTEXT_MANAGEMENT.md` 给出了 OpenClaw 框架的 ContextEngine 完整设计（10 个生命周期接口、5 类产物），本 spec 收敛出 darvin-agent **当前阶段**需要落地的形态：

- 一个进程内的 `ContextEngine` 子包 `internal/agent/ctxengine/`
- 暴露 OpenClaw 同形 10 个接口（接口可达 = spec 落地；具体实现深度按里程碑分层）
- 一个默认实现 `DefaultAssembler`（**进程内 memory，**零 SQLite / 零第三方依赖）
- 一条 token 估算管线（**字符数/4**，零依赖）
- 一条 LLM 调用 Compaction（共用现有 `llm.ModelProvider`）
- executor 接缝替换（`messages := assembler.Assemble(...)`）
- 一套配置项（token budget / 压缩阈值 / 摘要模型）

### 1.2 目标

1. **接管组装权**：`executor.RunConversation` 的 step-1（assemble messages）改为调用 `ctxengine.Assemble()`，session 直发的接缝下线。
2. **token 预算强约束**：每个 turn 调 LLM 之前，assembler 算出 `estimatedTokens`；超过 `MaxBudget` 时自动 `Compact()` 调 LLM 摘要后重试。
3. **10 个 ContextEngine 接口在 darvin-agent 端可达**：method 集与 OpenClaw 完全一致（签名可读、可测、可被 fake 注入）；不在 v0 范围里的（DAG / SubAgent / Projection 持久化）以 `TODO` / `ErrNotImplementedInV0` 显式标记，不静默漏接。
4. **零新外部依赖**：用标准库 `unicode/utf8`（字符计数）+ 现有 `llm.ModelProvider`（摘要 LLM 调用）；不引入 tiktoken-go / lark / 任何 LLM-SDK。
5. **可单测**：用 fake provider + fake session 验证 assemble / compact / budget 的边界路径，不依赖真 LLM。

### 1.3 非目标

- **不**实现 DAG 分支管理（OpenClaw 的 `prepareSubagentSpawn` / `onSubagentEnded` / `ContextProjection` 持久化）。这些接口签名保留、`ErrNotImplementedInV0` 返回。
- **不**把 ContextEngine 状态落 SQLite。本 spec 全部数据驻留在 `internal/agent/ctxengine/` 内的 `*DefaultAssembler` struct（sync.RWMutex 保护），与 `session.Session` 同层。后续 store spec 扩展 SQLite 时再迁。
- **不**实现 `bootstrap` / `maintain` 的实际副作用。方法签名可达，默认实现返回空 + `nil` error；留接缝给未来 spec 接入。
- **不**实现真正的 token 计数（tiktoken、BPE）。**字符数/4** 近似足够触发压缩触发器；精确计数等接第二个 LLM provider 时单独 spec。
- **不**重新设计 `session.Session`。Session 仍是消息 append-only 的真相源；ContextEngine 是"消费侧装配器"，**不写入** session（压缩摘要作为新消息 append，由 executor 在 compaction 后负责）。
- **不**改 EventBus 14 类事件契约。本 spec 通过 `event.CompactionEvent`（agent-loop §FR-7 留接口位）emit 一次压缩事件；其他事件不变。
- **不**实现跨模型消息转换；Compaction 复用当前 ModelProvider（如换 provider 跟随 `agent.cfg.Model`）。
- **不**引入跨 turn 的 `afterTurn` 副作用（记忆写回等）；M3 milestone 接口可达，副作用 default no-op，留 M4 spec。

### 1.4 术语

| 术语 | 含义 |
|------|------|
| **ContextEngine** | 一组装配 / 处理 session messages 的接口集合（OpenClaw 定义） |
| **Assembler** | ContextEngine 的默认实现，负责 `Assemble()`、`Compact()` 等 |
| **Token budget** | 单次 LLM 调用允许的最大 token 数 |
| **Compaction** | 超 budget 时用 LLM 把历史 messages 摘要成更短表示 + 替换 |
| **CheckPoint** | 压缩前的 session 状态快照，回滚 / 重试用 |
| **Projection** | 持久化后端线程的生命周期句柄（OpenClaw 概念） |
| **Ingest** | 把消息消化入 ContextEngine 的过程（提取事实 / 更新笔记） |
| **DAG** | 分支历史结构（OpenClaw 支持，本 spec 不实现） |

---

## 2. 用户场景

### 场景 1：长会话自动压缩

**Given** 用户已经和 Agent 聊了 30 轮，每轮有 2-3 次工具调用，`session.Messages()` 长度 = 120 条
**When** 用户继续 Prompt 第 31 轮
**Then**
1. `executor` 调 `assembler.Assemble(ctx, ...)`
2. Assembler 估算 `prompt tokens ≈ 80 KiB / 4 ≈ 20K tokens` > budget 16K
3. 自动触发 `Compact(ctx, {reason: "budget_exceeded"})`
4. Compact 把 120 条消息里前 90 条作为一个 span，调 LLM 生成 ≈ 800 token 摘要 append 为一条新消息
5. 摘要消息 + 后 30 条原始消息作为新 messages（≈ 9K tokens）送 LLM
6. emit `CompactionEvent {BeforeTokens:20000, AfterTokens:9000, Reason:"budget_exceeded"}`

### 场景 2：短会话直发无压缩

**Given** 全新 session，只 4 条 messages，总 ≈ 1K tokens
**When** 用户 Prompt
**Then**
1. `Assemble()` 返回原 messages，`estimatedTokens=1000 < budget`
2. **不触发** Compact，直接送 LLM
3. 不 emit `CompactionEvent`

### 场景 3：工具结果超长被截断

**Given** 某 turn 的 tool result 是 200 KiB 的 grep 输出
**When** Executor 在写完 tool message 后进入下一 turn
**Then**
1. Assemble 阶段对每个 tool message 做内容截断（`tool-result-truncate-bytes`，默认 50 KiB）+ `[truncated N bytes]` marker
2. 总 prompt token 估回 ≤ budget 后送 LLM
3. emit `CompactionEvent {Reason:"tool_truncate", TruncatedBytes:150000}`

### 场景 4：Abort 时清理未提交的压缩

**Given** Compact 刚生成摘要消息，还没把旧 messages 替换 / 删除
**When** 用户 Abort 当前 Run
**Then**
1. Compact goroutine 收到 ctx.Done() → 取消 LLM 摘要调用
2. 摘要消息**不**写入 session
3. 原始 messages 不变
4. Assembler 状态回滚到 CheckPoint（在 Compact 入口处 snapshot）

### 场景 5：Steer 强制重压缩

**Given** Assembler 在 budget 边界上 + Agent 刚 emit `TurnEndEvent{tool_calls}` 准备进入下一 turn
**When** 用户 Steer 一条消息
**Then**
1. Compact **不**被动触发（因为新消息还没写）
2. 新消息 append 后下一次 Assemble 时重算预算；如果再次超 budget，触发新一轮 Compact
3. emit `CompactionEvent` 时 Mode 字段记 `steer_triggered` 用于审计

### 场景 6：单消息 ingest 触发事实提取（v0 stub）

**Given** 用户发来一条 "我司的数据库叫 Postgres 16"
**When** Dispatcher append user message 后调 `ContextEngine.Ingest(ctx, message)`
**Then** （v0 = no-op） Ingest 接口被调用、内部 fact buffer 不变；不报错。v1 spec 把它接入 Memory 系统。

---

## 3. 功能需求

### FR-1: ContextEngine 接口（10 个方法，与 OpenClaw 对齐）

```go
// internal/agent/ctxengine/ctxengine.go
package ctxengine

type Info struct {
    Name    string  // "default" / 后续可 "memory-aware" 等
    Version string
}

type ContextEngine interface {
    Info() Info

    // 生命周期
    Bootstrap(ctx context.Context, p BootstrapParams) error
    Maintain(ctx context.Context, p MaintainParams) error
    Dispose(ctx context.Context) error

    // 消息处理
    Ingest(ctx context.Context, p IngestParams) IngestResult
    IngestBatch(ctx context.Context, p IngestBatchParams) IngestResult
    AfterTurn(ctx context.Context, p AfterTurnParams) error

    // 组装 + 压缩
    Assemble(ctx context.Context, p AssembleParams) AssembleResult
    Compact(ctx context.Context, p CompactParams) CompactResult

    // 子 Agent（v0 TODO 接缝;真实实现后续 spec)
    PrepareSubagentSpawn(ctx context.Context, p SubagentSpawnParams) (*SubagentSpawnPreparation, error)
    OnSubagentEnded(ctx context.Context, p SubagentEndedParams) error
}
```

### FR-2: AssembleParams / AssembleResult

```go
type AssembleParams struct {
    SessionID       string
    Messages        []llm.Message     // session 当前快照
    SystemSections  []SystemSection   // 用户/Agent/外部注入
    ToolBudget      int               // prompt 允许 token 上限 (0 = 不限)
    AvailableTools  []string          // 当前可用工具名集合
    AvailableSkills []SkillSummary    // v0 empty;future spec 接入 skills 时填充
    AvailableFacts  []Fact            // v0 empty;future spec 接入 memory 时填充
    MCPServers      []MCPServerInfo   // v0 empty;future spec 接入 mcp 时填充
}

type AssembleResult struct {
    Messages         []llm.Message  // 送进 LLM 的最终 messages
    EstimatedTokens  int            // 字符数/4 粗估
    SystemAddition   string         // 拼到 system prompt 末尾的额外段落
    Budget           int            // 实际生效的 budget (透传 ToolBudget)
    Stats            AssembleStats
}

type AssembleStats struct {
    TruncatedTools    int   // 截断了几条 tool result
    TruncatedBytes    int64
    CompactionTriggered bool
}
```

### FR-3: CompactParams / CompactResult

```go
type CompactParams struct {
    SessionID  string
    Messages   []llm.Message
    Budget     int
    Force      bool          // 强制压缩（忽略 budget check）
    Reason     string        // "budget_exceeded" | "tool_truncate" | "manual" | "steer_triggered"
    Checkpoint *CheckPoint   // 调用方传入用于回滚;Compact 内复制
}

type CompactResult struct {
    Success         bool
    TokensBefore    int
    TokensAfter     int
    RetainedMessages []llm.Message  // 替换后的完整 messages
    Summary         string         // LLM 生成的摘要原文（保留以便审计)
    Checkpoint      *CheckPoint    // 出口对应的回滚点（同一指针)
}

type CheckPoint struct {
    ID        string
    CapturedAt time.Time
    Snapshot  []llm.Message
}
```

### FR-4: 10 接口的 v0 实现深度（里程碑矩阵）

| 方法 | v0 状态 | 行为 |
|------|---------|------|
| `Info` | ✅ | 返回 `Info{Name:"default", Version:"v0"}` |
| `Bootstrap` | 🟡 stub | no-op + nil; 留接缝接 store/SQLiteStore 后续 |
| `Maintain` | 🟡 stub | no-op + nil; 留接缝 dream/cron 后续 |
| `Dispose` | ✅ | 释放内部 RWMutex 保护的资源;目前 no-op |
| `Ingest` | 🟡 stub | 记录 `lastIngestAt` 时间戳 + nil;真正事实提取留 Memory spec |
| `IngestBatch` | 🟡 stub | 同上 |
| `AfterTurn` | 🟡 stub | no-op + nil |
| `Assemble` | ✅ 完整 | 见 §4.4 7-step pipeline |
| `Compact` | ✅ 完整 | 见 §4.5 LLM-based compaction |
| `PrepareSubagentSpawn` | ❌ TODO | `return nil, ErrNotImplementedInV0` |
| `OnSubagentEnded` | ❌ TODO | `return ErrNotImplementedInV0` |

**显式 placeholder**：
```go
var ErrNotImplementedInV0 = errors.New("ctxengine: not implemented in v0 (TODO seam)")
var ErrSubAgentUnsupported = fmt.Errorf("%w (sub-agent)", ErrNotImplementedInV0)
```

### FR-5: 默认 Assembler（`DefaultAssembler`）

`DefaultAssembler` 是 `ContextEngine` 接口的进程内实现：

```go
type DefaultAssembler struct {
    mu           sync.RWMutex
    cfg          Config
    estimator    TokenEstimator       // 字符数/4 函数,可注入 fake
    summarizer   Summarizer           // 调 LLM 的函数,可注入 fake
    sections     []SystemSection      // 追加到 system prompt
    lastIngestAt map[string]time.Time // session_id → 上次 ingest

    // projection registry（v0 仅 in-memory 接口）
    projectionsMu sync.RWMutex
    projections   map[string]ContextProjection
}

func NewDefaultAssembler(cfg Config, deps Deps) *DefaultAssembler
func (a *DefaultAssembler) ContextEngine // 接口全部 method
```

`Deps` 接口（**避开 agent ↔ ctxengine 循环**，参考 `agent-loop §9.1 Deps 模式**）：

```go
type Deps interface {
    Provider()  llm.ModelProvider
    ModelName() string
    Logger()    *zap.Logger
}
```

`agent.Agent` 在新增 `Assembler` 字段时隐式满足上述 3 个方法（`Provider()` / `ModelName()` 已有；新增 `Logger()`）。

### FR-6: Token 估算

```go
type TokenEstimator func(text string) int

// 默认 estimator:
func EstimateCharsOver4(text string) int {
    n := utf8.RuneCountInString(text)
    return (n + 3) / 4   // 向上取整
}

// llm.Message 级别:
func EstimateMessageTokens(m llm.Message) int {
    n := utf8.RuneCountInString(m.Content)
    for _, tc := range m.ToolCalls {
        n += utf8.RuneCountInString(tc.Name)
        for k, v := range tc.Arguments {
            n += utf8.RuneCountInString(k)
            n += estimateAny(v)
        }
    }
    return (n + 3) / 4
}
```

注意：**不精确**，仅用于触发阈值判断。模型实际 token 计费用 provider 回的 `Usage`。

### FR-7: 单条 tool message 内容截断

`Assemble` pipeline 中第 6 步：当某条 `RoleTool` message 的 `Content` 字节数 > `ToolResultMaxBytes`（默认 50 KiB，可配）：

- 截断到上限
- 末尾追加 `\n[truncated N bytes, total M bytes]`
- 计入 `Stats.TruncatedBytes` / `Stats.TruncatedTools`

`ToolResultMaxBytes = 0` 关闭截断（**不推荐**，会让 200 KiB grep 结果直接进 prompt）。

### FR-8: LLM-based Compaction 策略

```go
type Summarizer interface {
    Summarize(ctx context.Context, req SummarizeRequest) (string, error)
}

type SummarizeRequest struct {
    Model     string
    Messages  []llm.Message  // 待摘要的 span
    Hint      string         // e.g. "conversational summary; preserve tool input/output facts"
    MaxTokens int            // 默认 800
}
```

**默认实现 `DefaultSummarizer`**：复用 `llm.ModelProvider.Complete()`（非流式），用与 Agent 同一 provider / model。`Compact` 流程：

1. **CheckPoint 快照**：`snapshot = clone(messages)`
2. **预算判定**：若 `!Force && estimatedTokens(messages) <= Budget` → 不压缩，返回原 messages（`Success=true, TokensAfter=tokensBefore`）
3. **切分 span**：保留 `TailKeepCount`（默认 6）条尾部消息不动；前面的视为待摘要 span
4. **调 LLM 摘要**：构造 `SummarizeRequest{Model: a.cfg.Model, Messages: span, Hint: "...", MaxTokens: 800}`；**注意：用与当前 Agent 完全独立的 `_summarize` 内部 provider call，避免污染 Agent 当前的 Session / EventBus**
5. **构造新 messages**：`[summary_msg] + tail`；`summary_msg = llm.Message{Role: RoleAssistant, Content: "[Conversation Summary] " + summaryText}` 加上元注释
6. **预算重算**：估算 `TokensAfter`；若仍 > Budget（极小概率，比如 tail 本身超 budget），按 token/2 比例再次切分 span 重试至多 2 次
7. **返回** `CompactResult{Success, TokensBefore, TokensAfter, RetainedMessages: newMessages, Summary, CheckPoint: originalSnapshot}`

**checkPoint 是入口传入**（executor 调用方传入），Compact 内部复制，避免 Compact goroutine 与 abort ctx 竞态。

### FR-9: Compact 触发点

Assembler 阶段触发：

```
estimatedTokens(messages) > Budget
  → Compact(ctx, {reason:"budget_exceeded"})
  → 若 Compact 失败 / 不收敛 → 仍返回原 messages，但 emit CompactionEvent{Success:false}
```

executor 阶段触发：

```
executor.RunConversation 第 70 行改为:
  assembled := a.assembler.Assemble(ctx, AssembleParams{
      SessionID: session.ID,
      Messages:  session.Messages(),
      SystemSections: a.sections(),
      ToolBudget: d.Config().TokenBudget,
      AvailableTools: toolNames,
      ...
  })
  messages := assembled.Messages
```

`d.Config()`（executor.Config）新增 `TokenBudget` 字段；`agent.Config` 同步。

### FR-10: CompactionEvent

agent-loop §FR-7 留了接口位，没实现。本 spec 实装：

```go
type CompactionEvent struct {
    SessionID          string
    Before             int
    After              int
    Reason             string
    Success            bool
    TruncatedBytes     int64
}
```

emitter 入口：每完成一次 Assemble / Compact 都 emit 一次（成功失败都有）。

### FR-11: 错误类型

```go
// internal/agent/ctxengine/errors.go
var (
    ErrNotImplementedInV0 = errors.New("ctxengine: not implemented in v0 (TODO seam)")
    ErrSubAgentUnsupported = fmt.Errorf("%w (sub-agent)", ErrNotImplementedInV0)

    ErrAssemblerNotConfigured = errors.New("ctxengine: assembler not configured on Agent")

    ErrCompactUnrecoverable = errors.New("ctxengine: compact could not converge under retry budget")
)
```

### FR-12: 配置

`internal/config/config.go.AgentConfig` 新增：

```go
type AgentConfig struct {
    // ... 已有字段
    TokenBudget          int    `mapstructure:"token_budget"`           // 默认 16000
    CompactTailKeep      int    `mapstructure:"compact_tail_keep"`      // 默认 6
    ToolResultMaxBytes   int    `mapstructure:"tool_result_max_bytes"`  // 默认 50 KiB = 51200
    CompactMaxRetries    int    `mapstructure:"compact_max_retries"`    // 默认 2
    SummarizeMaxTokens   int    `mapstructure:"summarize_max_tokens"`   // 默认 800
    SystemPromptAddition string `mapstructure:"system_prompt_addition"` // 拼到 system 末尾的固定段
    AssemblerEnabled     bool   `mapstructure:"assembler_enabled"`      // 默认 true;false 时 fallback 到 session.Messages() 直发
}
```

`config.yaml` 段追加对应默认。

### FR-13: Agent 端装配

```go
// internal/agent/agent.go（增量）
type Agent struct {
    // ... 已有字段
    assembler    ctxengine.ContextEngine   // v0 默认 *ctxengine.DefaultAssembler
    assemblerCfg ctxengine.Config
}

// NewAgentConfig 新增:
type NewAgentConfig struct {
    // ... 已有字段
    Assembler        ctxengine.ContextEngine  // 可选;nil = New 内部 NewDefaultAssembler
    AssemblerEnabled *bool                    // nil = 用 cfg.AssemblerEnabled
}

// agent-loop §9.7 两套 Config 字段不合并;保持 cfg(config.AgentConfig) / agent.Config / executor.Config 三套
```

### FR-14: executor 接缝替换

`internal/agent/executor/executor.go` 第 68-70 行的 TODO 替换为：

```go
// agent-loop 阶段一: 直发 (current)
// messages := d.Session().Messages()

// ctxengine 阶段二: 走 assembler
messages, assembled, err := d.Assembler().Assemble(ctx, ctxengine.AssembleParams{
    SessionID:  d.Session().ID,
    Messages:   d.Session().Messages(),
    SystemSections: d.SystemSections(),
    ToolBudget: d.Config().TokenBudget,
    AvailableTools: d.Tools().Names(),
})
if err != nil {
    return fmt.Errorf("executor: assemble: %w", err)
}
_ = assembled // 当前不用;留 emit 接口位
```

`executor.Deps` 新增两个 accessor：`Assembler() ctxengine.ContextEngine` + `SystemSections() []ctxengine.SystemSection`。

> ⚠️ **关于 Deps 增长**：`Agent` 已经满足 7 个方法（Deps interface）；再加 2 个。Agent 根包与 ctxengine 子包方向 = agent → ctxengine（保持），不引入循环。

**回退路径**：`NewAgentConfig.Assembler == nil && cfg.AssemblerEnabled == false` 时，executor 保留旧的 `messages := d.Session().Messages()`（旁路 assembler）。这样其它 spec 可以先 mock 验证，env switch。

### FR-15: Event 集成

`event.CompactionEvent` 是 agent-loop §FR-7 接口位，本 spec 实装。`event` 子包现状确认有 `CompactionEvent`（`event.CompactionEvent`）；若没有则补一个最小 stub（同 FR-7 模式）。

`Assembler.Assemble` 内部 emit `CompactionEvent` 调用：

```go
d.Emit(event.CompactionEvent{
    SessionID: p.SessionID,
    Before:    estimatedBefore,
    After:     estimatedAfter,
    Reason:    p.Reason,
    Success:   true,
})
```

`Assemble` 调用方（executor）**不直接 emit**，事件唯一发射点 = assembler 内部；executor 只传 Deps 接口的 `Emit(event.Event)`。

---

## 4. 实现方案

### 4.1 目录结构

```
src/darvin-agent/
├── cmd/
│   └── app/main.go                       # 不变（M2 已脱钩)
├── config.yaml                           # 小改:追加 ctxengine: 段
└── internal/
    ├── agent/
    │   ├── agent.go                      # 小改:加 assembler/assemblerCfg 字段;NewAgentConfig 加 2 个可选字段;Agent 隐式满足 Deps 第 8/9 个方法
    │   ├── dispatcher.go                 # 不变
    │   ├── errors.go                     # 不变
    │   │
    │   ├── ctxengine/                    # package ctxengine (新)
    │   │   ├── ctxengine.go              # 接口 10 个方法 + Info + ErrNotImplementedInV0
    │   │   ├── params.go                 # AssembleParams/CompactResult/IngestParams/...
    │   │   ├── tokens.go                 # EstimateMessageTokens + TokenEstimator 类型
    │   │   ├── tokens_test.go
    │   │   ├── assembler.go              # DefaultAssembler struct + NewDefaultAssembler
    │   │   ├── assemble.go               # Assemble() 7-step pipeline
    │   │   ├── assemble_test.go
    │   │   ├── compact.go                # Compact() + DefaultSummarizer
    │   │   ├── compact_test.go
    │   │   ├── ingest.go                 # Ingest + IngestBatch stub
    │   │   ├── after_turn.go             # AfterTurn stub
    │   │   ├── lifecycle.go              # Bootstrap + Maintain + Dispose stubs
    │   │   ├── subagent.go               # PrepareSubagentSpawn / OnSubagentEnded → ErrNotImplementedInV0
    │   │   ├── projection.go             # ContextProjection interface + in-memory registry
    │   │   ├── sections.go               # SystemSection + SkillSummary + Fact + MCPServerInfo 类型(v0 empty)
    │   │   ├── errors.go
    │   │   └── ctxengine_test.go         # 接口签名一致性测试 (用 reflect 验证方法集)
    │   │
    │   ├── llm/                          # 不变
    │   ├── tool/                         # 不变
    │   ├── executor/                     # 小改:executor.go line 70 替换;executor.Config 加 TokenBudget;executor.Deps 加 2 个方法
    │   ├── event/                        # 小改:确认 CompactionEvent 类型存在/补;无新事件
    │   ├── session/                      # 不变
    │   ├── store/                        # 不变
    │   └── queue/                        # 不变
    │
    ├── config/                           # 小改:加 AgentConfig 字段
    ├── database/                         # 不变
    └── logger/                           # 不变
```

### 4.2 包依赖关系（箭头 = import 方向）

```
ctxengine ──→ llm        (CompletionRequest 用于摘要调用)
           ──→ event     (CompactionEvent)
agent     ──→ ctxengine  (Agent.assembler 字段类型)
           ──→ executor  (已有)
executor  ──→ ctxengine  (executor.Deps.Assembler() 返回类型)
llm      (无内部依赖)
event    (无内部依赖)
```

agent ↔ executor ↔ ctxengine 三个包互相 import 时：
- agent → ctxengine（root → ctxengine,加)
- executor → ctxengine（executor 加,OK)
- ctxengine ✗（不 import agent/executor,避免循环)

ctxengine 通过 `ctxengine.Deps` 接口对外要求（与 agent-loop §9.1 executor.Deps 同形），agent 隐式满足。

### 4.3 ContextEngine 接口签名（10 个方法）

```go
package ctxengine

// Info / BootstrapParams / IngestParams / ... 见 FR-1 / FR-2 / FR-3

type Info struct {
    Name    string
    Version string
}

type ContextEngine interface {
    Info() Info

    Bootstrap(ctx context.Context, p BootstrapParams) error
    Maintain(ctx context.Context, p MaintainParams) error
    Dispose(ctx context.Context) error

    Ingest(ctx context.Context, p IngestParams) IngestResult
    IngestBatch(ctx context.Context, p IngestBatchParams) IngestResult
    AfterTurn(ctx context.Context, p AfterTurnParams) error

    Assemble(ctx context.Context, p AssembleParams) AssembleResult
    Compact(ctx context.Context, p CompactParams) CompactResult

    PrepareSubagentSpawn(ctx context.Context, p SubagentSpawnParams) (*SubagentSpawnPreparation, error)
    OnSubagentEnded(ctx context.Context, p SubagentEndedParams) error
}
```

**契约保证**：
- `Assemble` 是**幂等**的（相同输入→相同输出，多次调用不引入随机性）
- `Compact` 是**有副作用**的：可能向传入的 `*CheckPoint.Snapshot` 复制；不修改 `Messages`
- `Ingest` / `IngestBatch` / `AfterTurn` v0 是 no-op；调用方**必须**不假设它们产生变化
- `PrepareSubagentSpawn` / `OnSubagentEnded` 在 v0 返回 `nil, ErrSubAgentUnsupported`

### 4.4 Assemble 7-step pipeline

```go
func (a *DefaultAssembler) Assemble(ctx context.Context, p AssembleParams) AssembleResult {
    if err := ctx.Err(); err != nil {
        return AssembleResult{Messages: p.Messages, Stats: AssembleStats{}}
    }
    msgs := cloneMessages(p.Messages)  // 深拷贝,不修改调用方
    stats := AssembleStats{}

    budget := p.ToolBudget
    if budget <= 0 {
        budget = a.cfg.TokenBudget  // fallback 默认
    }

    // step 1: tool result 截断 (FR-7)
    for i, m := range msgs {
        if m.Role != llm.RoleTool { continue }
        if a.cfg.ToolResultMaxBytes > 0 && len(m.Content) > a.cfg.ToolStatsMaxBytes {
            // 截断
            originalLen := len(m.Content)
            m.Content = m.Content[:a.cfg.ToolResultMaxBytes] +
                fmt.Sprintf("\n[truncated %d bytes, total %d bytes]",
                    originalLen-a.cfg.ToolResultMaxBytes, originalLen)
            stats.TruncatedTools++
            stats.TruncatedBytes += int64(originalLen - a.cfg.ToolResultMaxBytes)
            msgs[i] = m
        }
    }

    // step 2: token 估算
    tokensBefore := estimateMessagesTokens(msgs, a.estimator)

    // step 3: 触发 compact?
    if tokensBefore > budget {
        compactRes := a.Compact(ctx, CompactParams{
            SessionID: p.SessionID,
            Messages:  msgs,
            Budget:    budget,
            Reason:    "budget_exceeded",
        })
        if compactRes.Success {
            msgs = compactRes.RetainedMessages
            tokensBefore = compactRes.TokensAfter
            stats.CompactionTriggered = true
        }
    }

    // step 4: 拼装 systemSections 到 SystemAddition
    sysAddition := a.composeSystemAddition(p.SystemSections)

    // step 5: emit CompactionEvent (本步)
    a.emit(event.CompactionEvent{
        SessionID: p.SessionID,
        Before:    estimateMessagesTokens(p.Messages, a.estimator),
        After:     estimateMessagesTokens(msgs, a.estimator),
        Reason:    "assemble",
        Success:   true,
        TruncatedBytes: stats.TruncatedBytes,
    })

    return AssembleResult{
        Messages:        msgs,
        EstimatedTokens: tokensBefore,
        SystemAddition:  sysAddition,
        Budget:          budget,
        Stats:           stats,
    }
}
```

`composeSystemAddition` 把传入的 `[]SystemSection` 按 `Priority` 升序拼接，与 `agent.cfg.Instructions` 合并。**v0 章节源 = Assembler 内置 default sections + 传入的 sections**；Skills/Memory/MCP 章节在那些子系统 spec 落地时填入 `p.AvailableSkills` 等字段，本 spec 留空。

### 4.5 Compact 实现

```go
func (a *DefaultAssembler) Compact(ctx context.Context, p CompactParams) CompactResult {
    snap := p.Checkpoint
    if snap == nil {
        snap = &CheckPoint{ID: newID(), CapturedAt: time.Now(), Snapshot: cloneMessages(p.Messages)}
    } else {
        snap.Snapshot = cloneMessages(p.Messages)  // 入口快照
    }

    tokensBefore := estimateMessagesTokens(p.Messages, a.estimator)

    // 不需要压缩
    if !p.Force && tokensBefore <= p.Budget {
        return CompactResult{
            Success: true,
            TokensBefore: tokensBefore,
            TokensAfter:  tokensBefore,
            RetainedMessages: p.Messages,
            Checkpoint: snap,
        }
    }

    // 切分
    tail := a.cfg.CompactTailKeep
    if tail <= 0 { tail = 6 }
    if tail >= len(p.Messages) {
        tail = len(p.Messages) - 1
        if tail < 0 { tail = 0 }
    }
    span := p.Messages[:len(p.Messages)-tail]
    if len(span) == 0 {
        // 全部 tail 都已超 budget 但又没法再摘要;返回 unrecoverable
        return CompactResult{
            Success: false, TokensBefore: tokensBefore, TokensAfter: tokensBefore,
            RetainedMessages: p.Messages, Checkpoint: snap,
        }
    }

    // 调 LLM 摘要
    summaryText, err := a.summarizer.Summarize(ctx, SummarizeRequest{
        Model:    a.deps.ModelName(),
        Messages: span,
        Hint:     "conversational summary; preserve tool input/output facts and decisions; max length: ",
        MaxTokens: a.cfg.SummarizeMaxTokens,
    })
    if err != nil {
        return CompactResult{
            Success: false, TokensBefore: tokensBefore, TokensAfter: tokensBefore,
            RetainedMessages: p.Messages, Checkpoint: snap,
        }
    }

    // 构造新 messages
    summaryMsg := llm.Message{
        Role: llm.RoleAssistant,
        Content: "[Conversation Summary]\n" + summaryText +
                 fmt.Sprintf("\n\n(Compacted at %s; original %d messages → tail %d messages)",
                     time.Now().Format(time.RFC3339), len(span), tail),
    }
    newMessages := append([]llm.Message{summaryMsg}, p.Messages[len(p.Messages)-tail:]...)

    // 预算重算 + 二次切分
    tokensAfter := estimateMessagesTokens(newMessages, a.estimator)
    retries := 0
    for tokensAfter > p.Budget && retries < a.cfg.CompactMaxRetries {
        // 把前一半 span 再摘要一遍
        half := len(span) / 2
        if half == 0 { break }
        span = p.Messages[:half]
        newSpan, err := a.summarizer.Summarize(ctx, SummarizeRequest{
            Model:    a.deps.ModelName(),
            Messages: span,
            Hint:     "compress further: ", MaxTokens: a.cfg.SummarizeMaxTokens,
        })
        if err != nil { break }
        summaryMsg.Content = "[Conversation Summary]\n" + newSpan +
            fmt.Sprintf("\n\n(Recompacted %d times)", retries+1)
        tailStart := half
        newMessages = append([]llm.Message{summaryMsg}, p.Messages[tailStart:]...)
        tokensAfter = estimateMessagesTokens(newMessages, a.estimator)
        retries++
    }

    if tokensAfter > p.Budget {
        return CompactResult{
            Success: false,
            TokensBefore: tokensBefore, TokensAfter: tokensAfter,
            RetainedMessages: p.Messages, Checkpoint: snap,
        }
    }
    return CompactResult{
        Success: true,
        TokensBefore: tokensBefore, TokensAfter: tokensAfter,
        RetainedMessages: newMessages,
        Summary: summaryMsg.Content,
        Checkpoint: snap,
    }
}
```

**关键不变量**：
- Compact **永不修改** `p.Messages` 切片本身；返回的是新切片 `RetainedMessages`
- Compact 内 `summarizer.Summarize` 用 `ctx`；ctx cancel 时立即返回 `Success=false`
- `p.Checkpoint` 如果调用方传入了指针，Compact 会在自己的 snapshot 上 mutate；调用方通过 `snap.ID` 跟踪是否使用同一快照

### 4.6 DefaultSummarizer

```go
type DefaultSummarizer struct {
    provider llm.ModelProvider
    deps     Deps
}

func (s *DefaultSummarizer) Summarize(ctx context.Context, req SummarizeRequest) (string, error) {
    // 用 provider.Complete (非流式);把 span messages + 一条 user "please summarize" 拼出 prompt
    // max_tokens 用 req.MaxTokens,默认 800
    // 返回 assistant content 字符串
    resp, err := s.provider.Complete(ctx, &llm.CompletionRequest{
        Model:    req.Model,
        Messages: req.Messages,
        System:   "You are a conversation summarizer. Output a concise summary. " + req.Hint,
        MaxTokens: req.MaxTokens,
        Stream:   false,
    })
    if err != nil { return "", err }
    return resp.Content, nil
}
```

**关键**：摘要 LLM 调用与 Agent 自身的 LLM 调用**共用同一 ModelProvider 实例，但使用不同的 `req.System`**——避免 Agent 当前的 system prompt 污染摘要。摘要请求的 `req.Messages` 是待摘要 span，**不写入 Agent Session**。

### 4.7 Ingest / IngestBatch / AfterTurn / Bootstrap / Maintain / Dispose

统一 stub：

```go
func (a *DefaultAssembler) Ingest(ctx context.Context, p IngestParams) IngestResult {
    if err := ctx.Err(); err != nil {
        return IngestResult{Success: false, Warnings: []string{err.Error()}}
    }
    a.mu.Lock()
    a.lastIngestAt[p.SessionID] = time.Now()
    a.mu.Unlock()
    return IngestResult{Success: true, TokensProcessed: 0}
}

// IngestBatch / AfterTurn / Bootstrap / Maintain / Dispose 类似 no-op + nil error
```

`Maintain` 在 v0 留接缝；后续 spec（Dreaming/Cron）把它接上。

### 4.8 SubAgent 接缝

```go
func (a *DefaultAssembler) PrepareSubagentSpawn(ctx context.Context, p SubagentSpawnParams) (*SubagentSpawnPreparation, error) {
    return nil, ErrSubAgentUnsupported
}
func (a *DefaultAssembler) OnSubagentEnded(ctx context.Context, p SubagentEndedParams) error {
    return ErrSubAgentUnsupported
}
```

调用方（agent dispatcher 或 future sub-agent spec）`errors.Is(err, ctxengine.ErrNotImplementedInV0)` 检查。

### 4.9 Projection（in-memory）

```go
type ContextProjection struct {
    ID         string
    Type       string  // "agent" | "tool" | "memory"
    CreatedAt  time.Time
    ExpiresAt  *time.Time
    State      map[string]any
}

func (a *DefaultAssembler) ProjectionCreate(ctx context.Context, p ContextProjection) error  // 不在 10 接口里;v0 旁路:Build() 返回新 struct 给调用方
```

`Projection` 接口**不在 ContextEngine 的 10 接口里**（OpenClaw 是 `Projection` 子接口）。v0 只通过 `DefaultAssembler` 暴露 in-memory map（`projections map[string]ContextProjection`），不在接口层暴露给外部；后续 spec 引入 SubAgent 时再展开。

### 4.10 executor.Deps 扩展 + Agent 满足

```go
// internal/agent/executor/executor.go
type Deps interface {
    Session() *session.Session
    Tools() *tool.Registry
    Provider() llm.ModelProvider
    ModelName() string
    Instructions() string
    Emit(event.Event)
    Config() Config
    // 新增:
    Assembler() ctxengine.ContextEngine
    SystemSections() []ctxengine.SystemSection
}

type Config struct {
    MaxTurns    int
    ToolTimeout time.Duration
    // 新增:
    TokenBudget int
}
```

```go
// internal/agent/agent.go
func (a *Agent) Assembler() ctxengine.ContextEngine { return a.assembler }

func (a *Agent) SystemSections() []ctxengine.SystemSection {
    // 暂返回 nil (FR-12 SystemPromptAddition 已通过 cfg 进 default assembler)
    return nil
}
```

### 4.11 executor 替换 line 70

```go
// internal/agent/executor/executor.go 第 68 行
// 旧:
//   messages := d.Session().Messages()
// 新:
if d.Assembler() == nil || (d.Config().TokenBudget <= 0 && !a.assemblerEnabled()) {
    // 回退路径
    messages = d.Session().Messages()
} else {
    assembled := d.Assembler().Assemble(ctx, ctxengine.AssembleParams{...})
    messages = assembled.Messages
}
```

> ⚠️ **回退条件**：v0 渲染让用户能用 `cfg.yaml` 一行 `assembler_enabled: false` 关掉 assembler，验证行为不变。这是 M3 验证 M2 没被破坏的关键。

---

## 5. 边界情况

| 场景 | 处理方式 |
|------|----------|
| session 为空 | `Assemble` 返回 `Messages: nil, EstimatedTokens: 0`；Compact 直接 `Success=true, no-op` |
| Compact 触发但 provider 失败 | `Compact` 返回 `Success:false, RetainedMessages: p.Messages`；Assembler 仍把原 messages 送 LLM；emit `CompactionEvent{Success:false}` |
| Compact 二次切分仍超 budget | 返回 `ErrCompactUnrecoverable`；同上一行 fallback |
| CompactionEvent subscriber channel 满 | 走 event bus drop-oldest 策略（已有）|
| Tools/Skills/Facts/MCPServers 注入值含不可序列化字段 | 跳过该条；emit warn log |
| Abort mid-Compact | ctx cancel → summarizer 立即返回 err → Compact 返回 `Success:false, RetainedMessages: original`；session 不变 |
| ToolResultMaxBytes = 0 | 关闭 tool result 截断（**配置错误**，zap warn 一次） |
| TokenBudget = 0 | 不限；不触发 Compact；zap warn 一次（建议 ≥ 4096） |
| CompactMaxRetries < 0 | 强制为 0；不二次切分 |
| Sessions 间串扰 | `lastIngestAt` 用 `map[session_id]time.Time` 隔离；projection 同理 |
| Configure 期间 panic | `NewDefaultAssembler` 内 `recover()`；返回 `ErrAssemblerNotConfigured` |
| 同一 ContextEngine 跨协程并发调用 | `sync.RWMutex` 保护；多次并发 Assemble 安全（输入是 immutable copy）|
| Default Summarizer Provider 不可用 | `Summarize` 立即返回 err `provider: missing`；Compact 失败 |
| SubAgent 接口调用 | `errors.Is(err, ErrNotImplementedInV0)` 返回 true；调用方按"未实现"分支 |

---

## 6. 涉及文件

### 6.1 新增（按包分组）

**`internal/agent/ctxengine/`** （新建 11 个文件)

| 文件 | 内容 |
|------|------|
| `ctxengine.go` | 接口 10 个方法 + Info |
| `params.go` | AssembleParams/CompactParams/IngestParams/... + SubagentSpawnParams |
| `tokens.go` | `TokenEstimator` 类型 + 默认实现 `EstimateCharsOver4` + `EstimateMessageTokens` |
| `assembler.go` | `DefaultAssembler` struct + `NewDefaultAssembler` + `Config`/`Deps` |
| `assemble.go` | `Assemble()` 7-step pipeline |
| `compact.go` | `Compact()` LLM-based + `DefaultSummarizer` |
| `ingest.go` | `Ingest` + `IngestBatch` stub |
| `after_turn.go` | `AfterTurn` stub |
| `lifecycle.go` | `Bootstrap` + `Maintain` + `Dispose` stub |
| `subagent.go` | `PrepareSubagentSpawn` + `OnSubagentEnded` → ErrNotImplementedInV0 |
| `projection.go` | `ContextProjection` struct + in-memory map |
| `sections.go` | `SystemSection` + `SkillSummary` + `Fact` + `MCPServerInfo` |
| `errors.go` | `ErrNotImplementedInV0` / `ErrSubAgentUnsupported` / `ErrCompactUnrecoverable` |
| `*_test.go` | 各文件对应单测 |

### 6.2 修改（既有文件)

| 文件 | 变更说明 |
|------|----------|
| `src/darvin-agent/internal/agent/agent.go` | **小改** `Agent` 加 `assembler`/`assemblerCfg` 字段；`NewAgentConfig` 加 `Assembler` / `AssemblerEnabled` 字段；`New` 默认构造 `NewDefaultAssembler`；新增 `Assembler()` / `SystemSections()` 方法（满足 `executor.Deps`） |
| `src/darvin-agent/internal/agent/executor/executor.go` | **小改** 第 68-70 行 TODO 替换为 assembler 调用；`Config` 加 `TokenBudget`；`Deps` 加 `Assembler()`/`SystemSections()` |
| `src/darvin-agent/internal/agent/event/event.go` | **小改** 确认 `CompactionEvent` 类型存在；若缺失按 agent-loop §FR-7 接口位补 |
| `src/darvin-agent/internal/config/config.go` | **小改** `AgentConfig` 加 8 个字段（FR-12） |
| `src/darvin-agent/config.yaml` | **小改** `agent:` 段追加对应默认 |
| `specs/features/agent-loop/2026-07-28-agent-loop-design.md` | **小改** §9.1 "Deps 接口化解循环" 段落追加 ctxengine 入列；新增 §10 「ContextEngine 接缝」指针到本 spec |

### 6.3 不改

| 文件 | 理由 |
|------|------|
| `src/darvin-agent/cmd/app/main.go` | M2 决定留空，context engine 接入不需要改动 main.go |
| `src/darvin-agent/internal/{database,logger}/` | 与 ContextEngine 无关 |
| `src/darvin-agent/internal/agent/{session,store,tool,queue,llm}/` | ContextEngine **消费** session.messages，**不写入** session；模型 provider 复用 |
| `src/darvin-agent/go.mod` / `go.sum` | 不引入新依赖（用 stdlib `unicode/utf8` + 现有 `llm.ModelProvider`）|
| `specs/features/agent-llm-encapsulation/*` | 本 spec 不修改模型 provider 抽象；复用现有 `llm.Complete` |

---

## 7. 验收标准

### 7.1 单元 / 集成测试

- `internal/agent/ctxengine/tokens_test.go`：
  - `EstimateMessageTokens("hello world")` 期望 3 (len("hello world")=11, 11/4=2.75 → 3)
  - 中日韩 emoji 字符计数 = 字符数 (用 `utf8.RuneCountInString`)
  - ToolCall args 计入（map[string]any 序列化为字符）
- `internal/agent/ctxengine/assemble_test.go`：
  - 短 messages 直接返回（不调 Compact） → Assembler 不接 provider 也 OK
  - Tool result 超长 → 截断 + stats 字段记
  - budget=0 → 不压缩（仅截断）
  - budget=16000, 总 tokens=20000 → 触发 Compact（fake summarizer 验证被调用次数 ≥ 1）
- `internal/agent/ctxengine/compact_test.go`：
  - tokensBefore <= budget → Return early（Success=true, TokensBefore==TokensAfter）
  - Force=true → 即使 tokens Before <= budget 也调 summarizer
  - summarizer 失败 → Success=false, RetainedMessages=原 Messages
  - 二次切分：tokens After > budget → 切半重试 → retry < maxRetries
  - context.Canceled mid-summary → Return Success:false
  - CheckPoint 入参被复制（Clone 后修改 CheckPoint 不会影响新 snapshot）
- `internal/agent/ctxengine/ctxengine_test.go`：
  - 用 `reflect` 检查 `*DefaultAssembler` 实现的 method 集 = 接口 10 个方法（签名一致，含参数/返回）
  - `SubAgent` 接口返回 `ErrSubAgentUnsupported`；`errors.Is(err, ErrNotImplementedInV0)` = true
- `internal/agent/ctxengine/lifecycle_test.go`：
  - Bootstrap / Maintain / Dispose 调用不返回 err
  - Ingest / IngestBatch / AfterTurn 调用 success=true, side effects 留记录
- `internal/agent/executor/executor_test.go`（扩展现有）：
  - `Assemble` 装上后，executor 不再走 `d.Session().Messages()` 直发；通过断言 `provider收到的 req.Messages != session.Messages()` 验证
  - `Assemble` 触发 Compaction → Compact 走 fake → executor 拿到的 req.Messages 已经过 assembler 改造
  - `AssemblerEnabled = false` 回退路径：旧直发行为不变
- `internal/agent/dispatcher_test.go`（扩展）：
  - `Assemble` 错误 → Agent 不挂，emit `AgentErrorEvent{Err: ErrAssemblerNotConfigured}` 之类（具体 err 由 ctxengine 包返回）

### 7.2 工具链

- `cd src/darvin-agent && go build ./...` 通过
- `go vet ./...` 无警告
- `go test -count=1 ./...` 全绿
- `go test -race ./internal/agent/...` 全绿
- `gofmt -l .` 无输出
- `go.mod` / `go.sum` 与现状一致（**不引入新依赖**)

### 7.3 手动验证（dev 模式）

1. **短会话直发验证**：`Assemble = off` 配置下，跑原 M2 prompt 行为不变
2. **长会话压缩验证**：编一个测试 session，120 条 messages，`TokenBudget = 16000`：观察 zap log 出现 `CompactionEvent{Reason:"budget_exceeded", Success:true}`
3. **Abort mid-Compact 验证**：在 Compact 尚未返回时 Abort → session.Messages() 不变，executor 收到 `Compact: Success:false` 后 fallback
4. **配置回退验证**：`config.yaml` 设 `assembler_enabled: false`，运行时 `go test` 全绿
5. **事件订阅验证**：订阅 `event.CompactionEvent`，收集空 session 的 Assemble → 收到 1 条 `{Reason:"assemble", Success:true, Before≈After≈0}` 事件

### 7.4 文档 / 一致性

- `internal/agent/ctxengine/ctxengine.go` 顶部 godoc 列出 10 个方法 + 接口可达性矩阵
- `specs/features/agent-loop/2026-07-28-agent-loop-design.md` §9.1 增加条目：executor.Deps 现在 9 个方法（含 Assembler/SystemSections）
- `specs/features/agent-loop/2026-07-28-agent-loop-design.md` §9.3/§9.5 追加"ContextEngine 落地后变更"段
- `docs/系统架构.md` "Agent 消息流转"图可读性更新：在 executor 框里标注 "1. assemble (ctxengine)" 而非 "1. = session.Msg()"
- agent-loop §1.3 `TODO(spec: future-context-engine)` 由本 spec 移除，executor.go 第 70 行注释相应更新

### 7.5 不在验收范围

- **DAG / 分支 / SubAgent** 真实实现：仅占位接口，调用 `ErrNotImplementedInV0`。
- **SQLite 持久化**：ContextEngine 全 in-memory；后续 store spec 单独评审。
- **真 LLM 集成测试**：本 spec 仅单测（fake summarizer）；真 LLM Compaction 测试需后续 IPC 落地的 build-tag e2e spec。
- **Memory / Skills / MCP 接入**：仅 `params.go` 留 `SkillSummary`/`Fact`/`MCPServerInfo` 类型 + 空 default sections；语义由对应子系统 spec 接入。
- **`bootstrap` / `maintain` 实质化**：仅 v0 stub；Memory spec / Dreaming spec 单独落地。
- **Token 精确计数**：字符数/4 已说明。
- **`agent-loop §9.3 cmd/app/main.go 不接线` 状态**：本 spec 不修。

---

## 8. 实现里程碑（7 个 milestone roadmap）

**注**：以下 7 个里程碑描述 spec 落地的实施顺序，**不是 spec 内的章节**。每个里程碑对应一个或多个 PR，所有里程碑在**同一份本 spec** 下推进。

| 里程碑 | 范围 | 工作量估 | 验收 |
|--------|------|----------|------|
| **M1 — pkg 骨架 + 接口契约** | 新建 `ctxengine/` 子包;定义 10 方法接口 + 全部 params/result struct + errors | 1d | `go build ./...` 通过；`ctxengine_test.go` 用 reflect 验证 method 集 |
| **M2 — token 估算 + tool 截断** | `tokens.go` + `assemble.go` 中的 step 1 + 6 | 0.5d | `tokens_test.go` 全绿；不带 Compact 的 Assemble 单测过 |
| **M3 — DefaultSummarizer + Compact LLM 调用** | `compact.go` + `DefaultSummarizer` + CheckPoint 复制语义 | 1d | `compact_test.go` 全绿（含 fake summarizer 与 abort 路径） |
| **M4 — executor seam 替换 + Deps 扩展** | `executor.go` 第 70 行替换；`executor.Deps` + `executor.Config` 扩展；`Agent` 满足 9 个方法 | 0.5d | executor / dispatcher 现有测试全绿；Assemble=off 回退路径 OK |
| **M5 — Agent 集成 + NewAgentConfig 字段** | `agent.go` 加 `assembler` 字段 + `New` 默认构造 + 2 个新方法 | 0.5d | `agent_test.go` 扩展；New 签名不变（向后兼容) |
| **M6 — Ingest / AfterTurn / Bootstrap / Maintain / Dispose + Projection in-memory** | 6 个 stub + `projection.go` map | 0.5d | `lifecycle_test.go` 全绿；10 接口 method 集全覆盖 |
| **M7 — 配置 + cmd 文档同步** | `config.go` 8 个新字段 + `config.yaml` 默认 + `systemd架构.md` 图更新 + agent-loop spec §9 同步 | 0.5d | `go build`/`go vet`/`go test` 全绿；`config.yaml` 默认生效；event bus 收到 CompactionEvent |

**总计**：约 4-5 个工作日。**前置依赖**：agent-loop（M2）已落地 ✓。

---

## 9. 实现偏差与说明

落地后实际代码与本 spec 的预期差异，逐条记录。

### 9.1 `Deps` 接口化解新依赖

类似 agent-loop §9.1：ctxengine 子包**不 import agent 根包**。`ctxengine.Deps` 接口要求 3 个方法（`Provider()` / `ModelName()` / `Logger()`），`Agent` 隐式满足。`executor.Deps` 加 2 个方法（`Assembler()` / `SystemSections()`），同上。

### 9.2 ToolResultMaxBytes 字段名

`Spec` §FR-12 写 `ToolResultMaxBytes`，agent.go 通过 viper 反序列化为 `agent.cfg.ToolResultMaxBytes`；Assembler 内部直接读这个字段。如未来需要重命名，单独改。

### 9.3 CompactionEvent 字段对齐

agent-loop §FR-7 留接口位但未实现。本 spec 实装：`CompactionEvent{SessionID, Before, After, Reason, Success, TruncatedBytes}`。`event` 子包当前可能用 `TokenCount` 而非 `Before/After`，实现期按实际结构字段名同步。

### 9.4 真 LLM Compaction 测试缺位

`compact_test.go` 用 fake `Summarizer`，**不依赖真 LLM**。真 LLM Compaction 烟测需等 IPC spec 落地 + 真 provider 配置（参考 agent-loop §9.6 同样的 build-tag 模式）。

### 9.5 SubAgent / DAG 接缝保留

`PrepareSubagentSpawn` / `OnSubagentEnded` 显式返回 `ErrSubAgentUnsupported`（= `ErrNotImplementedInV0` 包一层）。调用方 `errors.Is` 判定。后续 SubAgent spec 落地时**直接覆盖实现**，不需要改接口签名。

### 9.6 Compact API 边界：原文 messages 不变

`Compact` **绝不修改** `p.Messages` 切片：返回 `RetainedMessages` 是新切片；`p.Messages` 原地不变。这样 executor 可以决定"是否采用压缩结果"（如 ctx cancel 中途切换）。

### 9.7 Compaction 与 Session 的关系

本 spec 不修改 `session.Session`。摘要消息不通过 `session.Append()` 进入 session——除非 executor 后续选择 `RetainedMessages` 替换但此实现中**LlmReq.Messages 由 assembler 输出决定**。Session 仍是 append-only 的真相源；context engine 是消费侧适配器。

### 9.8 systemSections 接口开放

`executor.Deps.SystemSections()` 返回 `[]ctxengine.SystemSection`，但 v0 agent.go 总是返回 nil；接口开放是为了未来 Skills/Memory/MCP spec 填入章节时不改 executor。
