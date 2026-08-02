# Sub-spec 38 — Tool Registry Merge & Routing

> **父 spec**：[`2026-08-02-skills-and-mcp-modules-design.md`](./2026-08-02-skills-and-mcp-modules-design.md)
>
> **本 spec 范围**：把 skill 工具 + mcp 工具合并进 `tool.Registry`；改 `tool_start` / `tool_end` 事件携带 `kind` + `serverId?` + `skillId?`。**不包含** skills / mcp 模块本身（spec 31-37）、chat `/skill-name` 触发（spec 39）。
>
> 创建日期：2026-08-02
> 状态：⏳ 待启动
> 前置：[spec 26 tool-architecture-rework](./../tool-architecture-rework/2026-08-01-tool-architecture-rework-design.md) + [spec 31 skills-loader-and-registry](./2026-08-02-skills-loader-and-registry.md) + [spec 34 mcp-transport-and-client](./2026-08-02-mcp-transport-and-client.md) + [spec 35 mcp-registry-and-launcher](./2026-08-02-mcp-registry-and-launcher.md)

---

## 1. 概述

### 1.1 问题 / 背景

`internal/agent/tool/registry.go` 当前是**全局**单例查表，不感知 sessionKey / plugin 来源。本 spec 把 skill / mcp 工具作为 plugin 注册到 registry；同时把 tool_use 事件扩展为携带 `kind` 字段，让 renderer 端 `ToolCallGroup` 能按 kind 路由渲染（kind 已在 spec 02 落地）。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | `tool.Registry` 加 `kind` 字段：内置工具 vs skill vs mcp | registry.Get(name) 返回 Tool + Kind |
| G2 | skill 工具注册：`Plugin` interface + 注册入口 | 单测覆盖注册流程 |
| G3 | mcp 工具注册：`McpPlugin` + 注册入口（每个 mcp server 暴露 N 个 tool） | 单测覆盖 |
| G4 | `tool_start` / `tool_end` 事件加 `toolKind` + `skillId?` + `mcpServerId?` | live 验证 |
| G5 | agent executor 调度：根据 `toolKind` 路由到内置 / skill runner / mcp client | 单测覆盖 3 种调用路径 |
| G6 | 新 RPC `agent.tools.list` 合并返回内置 + skill + mcp tools | live 验证 |

### 1.3 非目标

- 不改 renderer 渲染逻辑（spec 02 已支持 kind 字段）
- 不改 session-aware registry（spec 26 落地；本 spec 只用其能力）
- 不实现 plugin marketplace（spec 26 已规划）
- 不动 darvin-api-extension 的 `DarvinToolKind` 字符串定义（已含 `skill` / `mcp`）

---

## 2. 用户场景

### 场景 1：registry 合并 3 类工具

**Given** App 启动完成：内置工具 9 个 + 5 bundled skill + 1 mcp filesystem（4 tools）= 共 18 个工具
**When** renderer 调 `agent.tools.list`
**Then** 返回 18 个 tool descriptor，每个含 `name` / `kind` / `description`

### 场景 2：agent 实际调用 skill 工具

**Given** `web-search` skill enabled；agent 决策调 `tool_use { name: "web_search", input: { query: "Go 1.23" } }`
**When** executor 调度
**Then**：
1. registry.Get("web_search") → 返回 kind=skill 的 Tool
2. executor 调 skill_runner.ExecuteByID("web-search", "...")
3. runner 返回 `SkillExecutionContext{system_prompt, args, tools}`
4. executor 进入 mini agent loop（只跑 skill 内的工具）
5. emit `tool_start { toolKind: 'skill', skillId: 'web-search' }`
6. mini loop 完成后 emit `tool_end { toolKind: 'skill', output: {...} }`

### 场景 3：agent 实际调用 mcp 工具

**Given** filesystem mcp 已 connected + 暴露 `read_file`
**When** agent 决策调 `tool_use { name: "mcp:filesystem:read_file", input: { path: "/tmp/foo" } }`
**Then**：
1. registry.Get 路由到 mcp 注册项 → 返回 kind=mcp 的 Tool
2. executor 解析 serverID（`filesystem`）+ toolName（`read_file`）
3. mcp_registry.CallTool("filesystem", "read_file", input)
4. emit `tool_start { toolKind: 'mcp', mcpServerId: 'filesystem' }`
5. mcp server 返回 → emit `tool_end { toolKind: 'mcp', output: {...} }`

### 场景 4：agent 看到内置工具

**Given** executor 调 `tool_use { name: "bash", input: { command: "ls" } }`
**When** 调度
**Then**：
1. registry.Get("bash") → 返回 kind=builtin 的 Tool
2. executor 走内置 `bash` 实现（spec 25 tool 架构）
3. emit `tool_start { toolKind: 'bash' }`
4. emit `tool_end { toolKind: 'bash', output: "..." }`

### 场景 5：用户禁用 skill 后 agent 看不到

**Given** `web-search` skill enabled，agent tool 列表含 `web_search`
**When** 用户 setEnabled(false)
**Then**：
1. registry 移除 `web_search`（kind=skill）
2. agent 下次 LLM 决策时收不到 `web_search` schema
3. 即使用户 prompt「搜索 X」，agent 不会调 web_search

### 场景 6：mcp connection lost

**Given** filesystem mcp connected，tools 列表有 4 个 mcp tool
**When** transport 断开（stdin EOF）
**Then**：
1. registry 移除 4 个 mcp tool
2. agent 下次决策收不到这 4 个 tool
3. emit `mcp_connection_changed { status: 'error' }` → renderer 卡片状态变 error

---

## 3. 功能需求

### FR-1: Tool interface 扩展

```go
// internal/agent/tool/tool.go 增量
type Kind string
const (
    KindBuiltIn Kind = "builtin"
    KindSkill   Kind = "skill"
    KindMcp     Kind = "mcp"
)

type Tool interface {
    Name() string
    Kind() Kind
    Description() string
    Schema() ToolSchema              // JSON schema for LLM tool definition
    Execute(ctx context.Context, input map[string]any) (ToolResult, error)
}

type ToolSchema struct {
    Name        string         `json:"name"`
    Description string         `json:"description"`
    InputSchema map[string]any `json:"inputSchema"`
}

type ToolResult struct {
    Content []ToolContentBlock
    IsError bool
}

type ToolContentBlock struct {
    Type string // text / image / ...
    Text string
}

type Plugin interface {
    PluginID() string
    Register(reg ToolRegistrar) error
    Unregister(reg ToolRegistrar) error
}

type ToolRegistrar interface {
    RegisterTool(t Tool) error
    UnregisterTool(name string) error
}
```

### FR-2: Registry 改造

```go
// internal/agent/tool/registry.go 增量
type Entry struct {
    Tool     Tool
    Kind     Kind
    Metadata map[string]any  // kind=skill 时含 skillID；kind=mcp 时含 mcpServerID + mcpToolName
}

type Registry struct {
    mu      sync.RWMutex
    entries map[string]*Entry
}

func NewRegistry() *Registry
func (r *Registry) Register(t Tool, meta map[string]any) error
func (r *Registry) Unregister(name string) error
func (r *Registry) Get(name string) (*Entry, bool)
func (r *Registry) List() []*Entry
func (r *Registry) ListByKind(kind Kind) []*Entry

// 会话感知（spec 26 落地后接入）
func (r *Registry) GetForSession(name string, sessionID string) (*Entry, bool)
```

### FR-3: SkillPlugin

```go
// internal/agent/tool/skill.go
package tool

import (
    "context"
    "darvin-cowork/internal/skills"
)

type SkillPlugin struct {
    registry *skills.SkillRegistry
}

func NewSkillPlugin(reg *skills.SkillRegistry) *SkillPlugin

func (p *SkillPlugin) PluginID() string { return "skill" }

func (p *SkillPlugin) Register(reg ToolRegistrar) error {
    for _, entry := range p.registry.ListEnabled() {
        t := &SkillTool{
            skillEntry: entry,
            runner:     p.runner,  // 共享 SkillRunner
        }
        if err := reg.RegisterTool(t); err != nil { return err }
    }
    return nil
}

func (p *SkillPlugin) Unregister(reg ToolRegistrar) error {
    for _, entry := range p.registry.List() {
        _ = reg.UnregisterTool(skillToolName(entry.ID))
    }
    return nil
}

// SkillTool 实现 Tool interface
type SkillTool struct {
    skillEntry *skills.SkillEntry
    runner     *skills.SkillRunner
}

func (t *SkillTool) Name() string { return skillToolName(t.skillEntry.ID) }
func (t *SkillTool) Kind() Kind { return KindSkill }
func (t *SkillTool) Description() string { return t.skillEntry.Description }
func (t *SkillTool) Schema() ToolSchema {
    return ToolSchema{
        Name: t.Name(),
        Description: t.skillEntry.Description,
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "args": map[string]any{"type": "string"},
            },
        },
    }
}

func (t *SkillTool) Execute(ctx context.Context, input map[string]any) (ToolResult, error) {
    args, _ := input["args"].(string)
    sec, err := t.runner.ExecuteByID(ctx, t.skillEntry.ID, args)
    if err != nil { return ToolResult{}, err }

    // 跑 mini agent loop
    result, err := runMiniAgentLoop(ctx, sec)
    if err != nil { return ToolResult{IsError: true}, err }

    return ToolResult{
        Content: []ToolContentBlock{{Type: "text", Text: result.Text}},
        IsError: false,
    }, nil
}

func skillToolName(skillID string) string { return "skill:" + skillID }
```

### FR-4: McpPlugin

```go
// internal/agent/tool/mcp.go
package tool

import (
    "context"
    "darvin-cowork/internal/mcp"
)

type McpPlugin struct {
    mcpRegistry *mcp.Registry
}

func NewMcpPlugin(reg *mcp.Registry) *McpPlugin

func (p *McpPlugin) PluginID() string { return "mcp" }

func (p *McpPlugin) Register(reg ToolRegistrar) error {
    for _, status := range p.mcpRegistry.List() {
        if !status.Connected { continue }
        for _, td := range status.Tools {
            t := &McpTool{
                serverID:   status.ServerID,
                toolDesc:   td,
                mcpReg:     p.mcpRegistry,
            }
            if err := reg.RegisterTool(t); err != nil { return err }
        }
    }
    return nil
}

func (p *McpPlugin) Unregister(reg ToolRegistrar) error {
    for _, status := range p.mcpRegistry.List() {
        for _, td := range status.Tools {
            _ = reg.UnregisterTool(mcpToolName(status.ServerID, td.Name))
        }
    }
    return nil
}

// McpTool
type McpTool struct {
    serverID string
    toolDesc mcp.ToolDescriptor
    mcpReg   *mcp.Registry
}

func (t *McpTool) Name() string { return mcpToolName(t.serverID, t.toolDesc.Name) }
func (t *McpTool) Kind() Kind { return KindMcp }
func (t *McpTool) Description() string { return t.toolDesc.Description }
func (t *McpTool) Schema() ToolSchema {
    return ToolSchema{
        Name: t.Name(),
        Description: t.toolDesc.Description,
        InputSchema: t.toolDesc.InputSchema,
    }
}

func (t *McpTool) Execute(ctx context.Context, input map[string]any) (ToolResult, error) {
    result, err := t.mcpReg.CallTool(ctx, t.serverID, t.toolDesc.Name, input)
    if err != nil {
        return ToolResult{IsError: true, Content: []ToolContentBlock{{Type: "text", Text: err.Error()}}}, nil
    }
    var blocks []ToolContentBlock
    for _, c := range result.Content {
        blocks = append(blocks, ToolContentBlock{Type: c.Type, Text: c.Text})
    }
    return ToolResult{Content: blocks, IsError: result.IsError}, nil
}

func mcpToolName(serverID, toolName string) string { return "mcp:" + serverID + ":" + toolName }
```

### FR-5: Executor 路由

```go
// internal/agent/executor/executor.go 增量
func (e *Executor) dispatchToolCall(ctx context.Context, name string, input map[string]any) (ToolResult, error) {
    entry, ok := e.toolReg.Get(name)
    if !ok { return ToolResult{}, fmt.Errorf("tool not found: %s", name) }

    // 提取 metadata
    meta := entry.Metadata

    // emit tool_start
    e.emit(ToolStartEvent{
        ToolName:  name,
        ToolKind:  string(entry.Kind),
        SkillID:   meta["skillID"].(string),       // kind=skill 时非空
        McpServerID: meta["mcpServerID"].(string), // kind=mcp 时非空
        Input:     input,
    })

    // 路由执行
    result, err := entry.Tool.Execute(ctx, input)

    // emit tool_end
    var content string
    for _, c := range result.Content { content += c.Text }
    e.emit(ToolEndEvent{
        ToolName:  name,
        ToolKind:  string(entry.Kind),
        SkillID:   meta["skillID"].(string),
        McpServerID: meta["mcpServerID"].(string),
        Output:    content,
        IsError:   err != nil || result.IsError,
    })

    return result, err
}
```

### FR-6: Event 类型扩展

```go
// internal/agent/event/event.go 增量
type ToolStartEvent struct {
    Common      EventCommon
    ToolName    string
    ToolKind    string         // builtin / skill / mcp
    SkillID     string         // kind=skill 时非空
    McpServerID string         // kind=mcp 时非空
    Input       map[string]any
    StartedAt   time.Time
}

type ToolEndEvent struct {
    Common      EventCommon
    ToolName    string
    ToolKind    string
    SkillID     string
    McpServerID string
    Output      string
    IsError     bool
    FinishedAt  time.Time
}
```

### FR-7: agent.tools.list RPC

```go
// internal/gateway/handlers.go 增量
h.handle("agent.tools.list", func(params json.RawMessage) (any, error) {
    entries := h.ToolRegistry.List()
    out := make([]map[string]any, 0, len(entries))
    for _, e := range entries {
        out = append(out, map[string]any{
            "name":         e.Tool.Name(),
            "kind":         string(e.Kind),
            "description":  e.Tool.Description(),
            "inputSchema":  e.Tool.Schema().InputSchema,
            "metadata":     e.Metadata,
        })
    }
    return map[string]any{"tools": out}, nil
})
```

### FR-8: renderer DarvinEvent 增量（`src/shared/darvin-api.ts`）

```typescript
// 已有的 tool_start / tool_end 增量
| { type: 'tool_start'; sessionId; toolUseId; tool; toolKind?: 'bash'|'read'|'write'|'edit'|'todowrite'|'web_search'|'web_fetch'|'image_gen'|'video_gen'|'skill'|'mcp'; skillId?: string; mcpServerId?: string; input; createdAt }
| { type: 'tool_end'; sessionId; toolUseId; tool; toolKind?: ...; skillId?: string; mcpServerId?: string; output; isError; createdAt }
```

`toolKind` / `skillId` / `mcpServerId` 都是可选（兼容 v0 不带字段的旧事件）。

### FR-9: cmd 接入

```go
// cmd/app/main.go 增量
skillPlugin := tool.NewSkillPlugin(skillsReg)
mcpPlugin := tool.NewMcpPlugin(mcpRegistry)

toolReg := tool.NewRegistry()
if err := skillPlugin.Register(toolReg); err != nil { log.Warn("skill plugin register", "err", err) }
if err := mcpPlugin.Register(toolReg); err != nil { log.Warn("mcp plugin register", "err", err) }

// 订阅 skill 变化 → 重新注册
h.OnSkillsChanged(func() {
    _ = skillPlugin.Unregister(toolReg)
    _ = skillPlugin.Register(toolReg)
})
// 订阅 mcp connection 变化 → 重新注册
h.OnMcpConnectionChanged(func(serverID string, status string) {
    _ = mcpPlugin.Unregister(toolReg)
    _ = mcpPlugin.Register(toolReg)
})
```

---

## 4. 实现方案

### 4.1 文件清单

```
src/darvin-agent/internal/agent/tool/
├── tool.go                    +Kind / ToolSchema / ToolResult / ToolContentBlock / Plugin / ToolRegistrar
├── registry.go                改造：加 Kind + Metadata
├── skill.go                   🆕 SkillPlugin + SkillTool
├── mcp.go                     🆕 McpPlugin + McpTool
├── plugin.go                  🆕 PluginRegistry
├── executor/executor.go       改造：dispatchToolCall 加 kind 字段 + emit ToolStart/EndEvent
├── event/event.go             增量：ToolStartEvent / ToolEndEvent 加 kind / skillID / mcpServerID
├── registry_test.go           🆕 合并后的 registry 测试
├── skill_test.go              🆕 SkillPlugin 测试
├── mcp_test.go                🆕 McpPlugin 测试
└── executor_routing_test.go   🆕 executor 路由测试

src/darvin-agent/internal/gateway/
└── handlers.go                +agent.tools.list handler

src/darvin-agent/cmd/app/
└── main.go                    +skillPlugin / mcpPlugin 注入 + 订阅变化重新注册

src/shared/
└── darvin-api.ts              +toolKind / skillId / mcpServerId 字段（可选）
```

### 4.2 关键代码片段（见 FR-1 ~ FR-7）

### 4.3 关键决策与理由

#### 4.3.1 Tool interface 加 Kind() 方法（不改 Name / Execute）

**理由**：spec 26 tool-architecture-rework 已规划；本 spec 是其落地执行。

#### 4.3.2 skill / mcp 工具名加前缀（`skill:<id>` / `mcp:<server>:<tool>`）

**理由**：避免跟内置工具名冲突；renderer 端 `ToolCallGroup` 按 `kind` 渲染时也能用。

#### 4.3.3 plugin 重新注册（不是动态 patch）

**理由**：每次 skill / mcp 变化 → 全部 Unregister + Register，简化实现。性能开销可接受（一般 skill/mcp 数量 < 50）。

#### 4.3.4 mini agent loop 跑 skill

**理由**：skill 是 prompt + 工具集；执行 = 走 LLM with skill system prompt + skill tools。复用现有 executor。

### 4.4 测试策略

| 测试 | 覆盖 |
|------|------|
| `registry_test.go` | Register / Unregister / Get / List / ListByKind / 并发安全 |
| `skill_test.go` | SkillPlugin.Register 把 5 个 skill 注册为 5 个 SkillTool；SkillTool.Execute 跑 mini loop |
| `mcp_test.go` | McpPlugin.Register 把 4 个 mcp tool 注册；McpTool.Execute 转发到 mcp registry |
| `executor_routing_test.go` | dispatchToolCall 按 Kind 路由 builtin / skill / mcp；emit event 带正确字段 |

---

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| skill 同 id 注册两次 | 第二次覆盖；Unregister + Register |
| mcp connection 抖动 | 每次状态变化都重新 register；高频时 main 端节流 |
| agent 调用 disabled skill 工具 | registry.Get 返回 ok=false → executor 返回 error "tool not found" |
| agent 调用 disconnected mcp 工具 | 同上 |
| mini agent loop 跑超时 | context cancel + emit tool_end with isError=true |
| mcp server 返回 content 是 image（不是 text） | ToolContentBlock 加 image 类型（v0 仅 text） |
| skill 工具 schema 输入校验 | 复用 executor 现有 schema 校验 |
| 内置工具名与 skill/mcp 重名 | 命名空间前缀（`skill:` / `mcp:`）天然隔离 |

---

## 6. 涉及文件

| 文件 | 变更 |
|------|------|
| `src/darvin-agent/internal/agent/tool/tool.go` | +Kind / ToolSchema / ToolResult / ToolContentBlock / Plugin / ToolRegistrar |
| `src/darvin-agent/internal/agent/tool/registry.go` | 改造 |
| `src/darvin-agent/internal/agent/tool/skill.go` | 🆕 |
| `src/darvin-agent/internal/agent/tool/mcp.go` | 🆕 |
| `src/darvin-agent/internal/agent/tool/plugin.go` | 🆕 |
| `src/darvin-agent/internal/agent/tool/registry_test.go` | 🆕 |
| `src/darvin-agent/internal/agent/tool/skill_test.go` | 🆕 |
| `src/darvin-agent/internal/agent/tool/mcp_test.go` | 🆕 |
| `src/darvin-agent/internal/agent/executor/executor.go` | 改造 |
| `src/darvin-agent/internal/agent/event/event.go` | 增量 |
| `src/darvin-agent/internal/agent/executor/executor_routing_test.go` | 🆕 |
| `src/darvin-agent/internal/gateway/handlers.go` | +agent.tools.list handler |
| `src/darvin-agent/cmd/app/main.go` | +plugin 注入 |
| `src/shared/darvin-api.ts` | +可选字段 |

---

## 7. 验收标准

**通用**：
- [ ] `cd src/darvin-agent && go build ./...` 编译通过
- [ ] `go vet ./...` 无警告
- [ ] `npm run lint` + `npm run test` 通过

**FR-1 Tool interface**：
- [ ] Kind 3 类（builtin / skill / mcp）
- [ ] Schema 返回 ToolSchema 结构
- [ ] Plugin interface 4 方法

**FR-2 Registry**：
- [ ] Register / Unregister / Get / List / ListByKind 工作
- [ ] 并发安全

**FR-3 SkillPlugin**：
- [ ] Register 把 5 个 bundled skill 注册为 5 个 SkillTool
- [ ] SkillTool.Name() 返回 `skill:<id>`
- [ ] SkillTool.Execute 调 runner + mini loop
- [ ] Unregister 清理

**FR-4 McpPlugin**：
- [ ] Register 把 4 个 mcp tool 注册为 4 个 McpTool
- [ ] McpTool.Name() 返回 `mcp:<server>:<tool>`
- [ ] McpTool.Execute 转发到 mcp registry
- [ ] Unregister 清理

**FR-5 Executor 路由**：
- [ ] dispatchToolCall 按 Kind 路由
- [ ] emit ToolStartEvent 含 toolKind + skillID/mcpServerID
- [ ] emit ToolEndEvent 含 toolKind + skillID/mcpServerID + output + isError

**FR-6 Event 类型**：
- [ ] ToolStartEvent / ToolEndEvent 含 kind 字段

**FR-7 agent.tools.list**：
- [ ] 返回内置 + skill + mcp 工具

**FR-8 renderer 事件**：
- [ ] DarvinEvent.tool_start / tool_end 加可选字段
- [ ] 旧事件（无字段）兼容

**FR-9 cmd 接入**：
- [ ] skill / mcp plugin 注册
- [ ] 订阅 skill/mcp 变化 → 重新注册

**集成手测**：

```bash
# 启动 App
npm start

# renderer console:
const r = await window.darvin.agent.tools.list()
# 期望：{ tools: [
#   { name: 'bash', kind: 'builtin', ... },
#   { name: 'skill:code-review', kind: 'skill', ... },
#   { name: 'mcp:filesystem:list_directory', kind: 'mcp', ... },
#   ...
# ] }

# 发 prompt：「用 web-search 搜索 Go 1.23」
# 期望：tool_use event toolKind='skill' skillId='web-search'
# 期望：tool_end event 同上 + 输出

# 发 prompt：「读 /tmp/foo.txt」
# 期望：tool_use event toolKind='mcp' mcpServerId='filesystem' toolName='read_file'
```

---

## 8. 与其他 spec 的关系

**前置**：
- spec 26 tool-architecture-rework（plugin loader 落地）
- spec 31（skills loader / runner）
- spec 34 / 35（mcp transport / registry）

**下游依赖**：
- spec 39（chat `/skill-name`）通过 registry 走 skill 工具入口

**并行**：spec 33（renderer UI）/ spec 37（mcp renderer UI）

---

## 9. 状态变更日志

- 2026-08-02 · 完成 spec 设计；待用户确认后启动实现