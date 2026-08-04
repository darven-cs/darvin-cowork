# 06 — ctx Engine 真正接通

> 状态: 草案 v1 · 2026-08-04
> 父 spec: `00-harness-architecture-design.md`
> 前置: `01-harness-core-interface.md`, `02-agent-refactor.md`, `05-tool-surface-bridge.md`
> 输出: `internal/agents/ctxengine/` 改造 + `agent.Agent` 改造

## 1. 目标

darvin-cowork 的 `internal/agents/ctxengine/` 当前**已经写完但未启用**:

- `DefaultAssembler` 已实现,有完整的 token budget / compact / summarization 逻辑
- `protocol.AssembleParams` 已有结构
- 但 `executor.RunConversation` 走的是 `d.Session().Messages()` legacy 路径
- 启动 config `AssemblerEnabled: false`(默认 false)
- `SkillSummary` 已定义但 `AvailableSkills` 永远空

本 spec 启用整套 ctx engine,并把它的触发入口接到 harness.Compact capability。

## 2. 现状审计

### 2.1 Agent.AssemblerEnabled 状态

```go
// agent.go:328
func (a *Agent) AssemblerEnabled() bool { return a.assemblerEnabled }
```

- `NewAgentConfig.AssemblerEnabled` 默认 `false`(Go bool 零值)
- `cfg.yaml` 用户通过 `assembler_enabled: true` 设置
- 当前没有任何 session 默认开,只手工调

### 2.2 executor 双路径

```go
// internal/agents/executor/executor.go:140-158
assemblerEnabled := d.Assembler() != nil && d.AssemblerEnabled()
if !assemblerEnabled {
    messages = d.Session().Messages()       // legacy 路径
} else {
    assembled := d.Assembler().Assemble(ctx, ctxengine.AssembleParams{
        SessionID: d.Session().ID, Messages: d.Session().Messages(),
        ToolBudget: d.Config().TokenBudget, LastUsage: d.LastUsage(),
        SystemSections: d.SystemSections(),
    })
    messages = assembled.Messages            // assembler 路径
}
```

两条路径并存,默认走 legacy。assembler 路径从未在生产启用。

### 2.3 SkillSummary 空载

```go
// internal/agents/ctxengine/params.go
type AssemblerParams struct {
    AvailableSkills []SkillSummary   // 永远空
    // ...
}
```

```go
// internal/agents/ctxengine/sections.go
// SkillSummary is the metadata the context engine injects into the
// <available_skills> system section. **AvailableSkills is empty in the
// current build (no skills system wired in yet).**
```

### 2.4 Compact 触发逻辑存在但无消费者

```go
// internal/agents/ctxengine/compact.go
// Compact() / CompactIfNeeded() 已实现
// 但只有 gateway.handlers.handleCompactContext 主动调它
// executor.RunConversation 不主动触发
```

## 3. 目标架构

### 3.1 启用 Assembler(默认开)

```go
// config.yaml 默认值
agents:
  defaults:
    assembler_enabled: true   # 从 false 改 true
```

Go 端:

```go
// internal/agents/agent.go: NewAgentConfig 处理
// 保留 AssemblerEnabled 字段(用户可显式 false)
// 但默认 yaml → true(从 config 解析层,不是 Go 零值)
```

### 3.2 触发 harness.Compact 而不是手动 RPC

当前:renderer 发 `agent.compact_context` → handler.handleCompactContext → `agent.New(...).RunConversation()`

目标:handler.handleCompactContext 调 `harness.Compact(ctx, params)`,harness 内部决定走 DefaultAssembler 还是别的策略:

```go
// embeddedHarness.Compact (新)
func (h *embeddedHarness) Compact(ctx, params) (*CompactResult, error) {
    assembler := h.assembler.(*ctxengine.DefaultAssembler)
    if params.AutoTrigger {
        // 内部触发:在 RunAttempt 内部 token 超 budget 时调
        return assembler.CompactIfNeeded(ctx, params.SessionID)
    }
    // 手动触发:handler 主动调
    return assembler.Compact(ctx, params)
}
```

### 3.3 AvailableSkills 真的注入

```go
// Agent.New() 时从 Skills registry 拉
func (a *Agent) initAssemblerParams() ctxengine.AssemblerParams {
    skills := a.skills.ListSummaries()    // 新增 helper
    return ctxengine.AssemblerParams{
        AvailableSkills: skills,
        AvailableMcp:    a.mcp.ListSummaries(),  // 新增 helper
    }
}
```

每次 Agent.Run 起头调一次,刷新 AvailableSkills 列表。

### 3.4 Tool result 大小限制生效

`internal/agents/ctxengine/params.go.ToolResultMaxBytes` 当前是 50KB 但不真限制,executor 拿到的 tool result 是原始大小。改造后:executor 拿到的 Result 过 tooldridge.MaxResultBytes(见 05 spec),然后 ctx engine 看到的已经是截断后的。

## 4. 详细改动

### 4.1 `internal/agents/ctxengine/params.go` 扩字段

```go
// 新增字段
type AssemblerParams struct {
    // ... 现有字段
    AvailableSkills []SkillSummary       // 已存在,现在真填
    AvailableMcp    []McpSummary         // 新增
    SystemSections  []SystemSection      // 新增(由 Agent 注入)
    SystemPromptAddition string           // 新增(由 Config 注入)
}

type McpSummary struct {
    ServerID  string
    Name      string
    ToolCount int
    Tools     []ToolSummary   // 截断显示
}
```

### 4.2 `internal/agents/ctxengine/sections.go` 真渲染 skills/mcp

```go
// 改造前
func renderAvailableSkillsSection(skills []SkillSummary) string {
    if len(skills) == 0 { return "" }      // 永远返回 ""
    // ...
}

// 改造后
func renderAvailableSkillsSection(skills []SkillSummary) string {
    if len(skills) == 0 {
        return "<available_skills>\n(none registered)\n</available_skills>"
    }
    var b strings.Builder
    b.WriteString("<available_skills>\n")
    for _, s := range skills {
        fmt.Fprintf(&b, "  - %s: %s\n", s.Name, s.Description)
    }
    b.WriteString("</available_skills>")
    return b.String()
}
```

### 4.3 `internal/agents/agent.go` 加 skills / mcp 列表

```go
// Agent 新增字段
type Agent struct {
    // ... 现有
    skills  *skills.Registry    // 注入
    mcp     *mcp.Registry        // 注入
}

// 改造 NewAgentConfig
type NewAgentConfig struct {
    // ... 现有
    Skills *skills.Registry
    Mcp    *mcp.Registry
}

func (a *Agent) listSkillSummaries() []ctxengine.SkillSummary {
    if a.skills == nil { return nil }
    out := make([]ctxengine.SkillSummary, 0, len(a.skills.Names()))
    for _, name := range a.skills.Names() {
        sk := a.skills.Get(name)
        if sk == nil { continue }
        out = append(out, ctxengine.SkillSummary{
            Name: sk.Name(),
            Description: sk.Description(),
            // ...
        })
    }
    return out
}

func (a *Agent) listMcpSummaries() []ctxengine.McpSummary {
    if a.mcp == nil { return nil }
    return a.mcp.ListSummaries()
}
```

### 4.4 `internal/agents/executor/executor.go` 路径决策

```go
// 改造前:
assemblerEnabled := d.Assembler() != nil && d.AssemblerEnabled()
if !assemblerEnabled {
    messages = d.Session().Messages()
} else {
    // 调 Assemble
}

// 改造后:同逻辑(不改),但加 token 超 budget 检查
assemblerEnabled := d.Assembler() != nil && d.AssemblerEnabled()
if assemblerEnabled {
    // 主动 compact 触发
    if d.Assembler().ShouldCompact(assembled) {
        if res, err := d.CompactHarness(); err == nil && res.Compacted {
            // emit compact event,继续下一轮
        }
    }
}
```

### 4.5 harness.Compact 接入

```go
// internal/harness/builtin-embedded.go
func (h *embeddedHarness) Compact(ctx, params) (*CompactResult, error) {
    // 拿到对应 session 的 Agent
    a := h.agentsBySession[params.SessionID]
    if a == nil { return nil, errors.New("session not found") }

    if !params.AutoTrigger {
        // 手动:用户主动请求,允许 skip
        res, err := a.Assembler().Compact(ctx, ...)
        return &CompactResult{...}, err
    }
    // 自动:由 executor 在 token 超 budget 时调
    res, err := a.Assembler().CompactIfNeeded(ctx, ...)
    return res, err
}
```

## 5. 不动的东西

- `executor.RunConversation` 算法(turn loop 逻辑) **不动**
- `protocol.CompletionRequest` / `protocol.Usage` / `protocol.Message` **不动**
- `internal/llm/` (provider 实现) **不动**
- `ctxengine.DefaultAssembler` 已有逻辑(compact / token count / summarise) **不动**

## 6. 与 OpenClaw 差异

| OpenClaw | darvin-cowork | 原因 |
|---|---|---|
| `compact.queued.ts` (~800 行, 完整 queued/background compactor) | ~150 行 | darvin-cowork 暂不做 queued 模式(等 Phase 7+) |
| `compaction-recovery.ts` | 不实现 | 暂不需要 recovery,Manual 模式够用 |
| `harness.compact` 完整 capability | **实现** | 完整平移 |
| `agent.compact_context` RPC | **保留** | 给 renderer 手动触发 |
| Auto-trigger from executor | **实现** | 跟 OpenClaw 一致 |

## 7. 测试要求

### 7.1 既有测试必须全过

```
$ go test -count=1 -short ./internal/agents/ctxengine/...
$ go test -count=1 -short ./internal/agents/...
```

`compact_test.go` / `assemble_test.go` / `tokens_test.go` 全部 PASS。

### 7.2 新增测试

| 文件 | 测试 | 覆盖 |
|---|---|---|
| `sections_test.go` | `TestRenderAvailableSkillsEmpty` | 空 list → "<none registered>" 字符串 |
| `sections_test.go` | `TestRenderAvailableSkillsNonEmpty` | 3 个 skill → 正确格式 |
| `sections_test.go` | `TestRenderAvailableMcp` | MCP 服务器列表 |
| `agent_ctx_test.go` | `TestListSkillSummariesEmpty` | skills=nil → nil |
| `agent_ctx_test.go` | `TestListSkillSummariesPopulated` | 3 skills → 3 summaries |
| `executor_compact_test.go` | `TestAutoCompactOnTokenOverflow` | token 超 budget → CompactIfNeeded 触发 |
| `embedded_harness_test.go` | `TestEmbeddedHarnessCompact` | harness.Compact 走 assembler |
| `embedded_harness_test.go` | `TestEmbeddedHarnessCompactAutoTrigger` | 自动触发路径 |

总新增: ≥ 8 个 case。

## 8. Phase 5 提交清单

```bash
$ git add internal/agents/ctxengine/ internal/agents/agent.go internal/agents/executor/
$ git add internal/harness/builtin-embedded.go
$ go test -count=1 -short ./...
$ go test -count=1 ./internal/agents/... ./internal/harness/...
$ git commit -m "feat(ctx-engine): wire up Assembler + AvailableSkills + harness.Compact

改造:
- cfg.yaml 默认 assembler_enabled: true(从 false 改)
- internal/agents/ctxengine/sections.go: AvailableSkills / AvailableMcp
  真渲染到 system prompt
- internal/agents/agent.go: 注入 skills + mcp registry,新增 ListSummaries
- internal/agents/executor/executor.go: token 超 budget 主动触发 compact
- internal/harness/builtin-embedded.go: Compact capability 走 DefaultAssembler

Config 改动:agents.defaults.assembler_enabled = true(从 false)

Spec: specs/features/agent-harness-architecture/06-ctx-engine-binding.md"
```

## 9. 风险与缓解

| 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|
| 启用 Assembler 后 LLM call 第一次变慢(token 算多了) | 中 | 中 | TokenEstimator 已有,cheap;只首次 Build 调用时算 |
| AvailableSkills 渲染太长撑爆 system prompt | 中 | 高 | 限制显示前 20 个 skill,后面 "+N more, use list_skills to see all" |
| ToolResultMaxBytes 实际生效(配合 05 spec),某些 skill 失效 | 中 | 中 | Skill output 现在是 max 50KB,rendered 文本 50KB 通常够;log 截断事件,人工 case-by-case |
| 手动 compact 触发后,executor 内部又触发一次 → 死循环 | 低 | 高 | 加 guard:同一 session 同一 turn 内最多 compact 一次 |
| harness.Compact 调用时,Agent 不在 (entry 已 evict) | 中 | 中 | embeddedHarness.Compact 查 agentsBySession map,miss 时返 "session not found",handler 返 CodeSessionNotFound |

## 10. 验收标准

1. `go build ./...` 通过
2. 既有 ctx engine 13 个 test 全过
3. 既有 6 个 executor test 全过
4. 启动后 session 0(system prompt) 包含 `<available_skills>` 块(可通过 `agent.get_messages` RPC 验证)
5. 启动后 session 0 包含 `<available_mcp>` 块
6. 跑 5 轮长对话,触发至少 1 次自动 compact(executor log: `auto compact triggered`)
7. `agent.compact_context` 手动 RPC 工作(从 renderer 调)
8. smoke test: 一次 `client.prompt` → LLM first chunk 延迟增加 < 5%

## 11. 与其它 spec 的接口

- **01 spec**: Harness.Compact capability,本 spec 第一个真实实现
- **02 spec**: Agent 注入 skills / mcp registry(02 spec 的 Agent 重构 阶段加)
- **03 spec**: Selection 不直接用 ctx engine
- **04 spec**: Gateway 调 harness.Compact,本 spec 是它的**第一个真实消费者**
- **05 spec**: Tool result middleware 配合本 spec 的 ToolResultMaxBytes 真正生效
