# agents/ 接口解耦：agents 只依赖 internal/agents/protocol

> **范围**：让 `internal/agents/...` 不再 import 任何"能力包"（`internal/llm`、`internal/tools`、`internal/mcp`、`internal/skills`、`internal/acp`、`internal/gateway`），使目录改造 step 4 的**严格** `lint-agents-boundaries` 规则全绿。
>
> **前置**：目录改造 step 1-3 已完成并提交（`agent/→agents/`、`agents/llm→internal/llm`、`agents/tool→internal/tools`）。
>
> **本次不重做**：IPC schema、事件 schema、agent 行为、测试预期 —— 全部不动。本 spec 是纯结构重构，运行时行为不变。
>
> **仓库约定**：Go 注释只加在导出符号上；不改测试预期；commit 用 Conventional Commits 英文。

---

## 1. 概述

### 1.1 问题 / 背景

目录改造 step 4 想加一条铁律：`agents/` 是纯 framework，不能反向依赖能力包。但计划原文的严格规则现在**立刻会红**——实测 `go list -deps ./internal/agents/...` 有 7/8 个包依赖能力包：

| 包 | 依赖 | 说明 |
|----|------|------|
| `internal/agents`（root） | `llm`、`tools`、`mcp` | agent.go: `llm.ModelProvider` / `*tool.Registry` / `tool.NewBuiltins` |
| `internal/agents/executor` | `llm`、`tools`、`mcp` | executor.go 用了 llm 全量类型模型 + `*tool.Registry` |
| `internal/agents/event` | `llm` | event.go 的字段是 `llm.Message` / `llm.Usage` / `llm.FinishReason` |
| `internal/agents/ctxengine` | `llm` | assembler/params/compact/tokens 全用 llm 类型 |
| `internal/agents/queue/session/store` | `llm` | 经 event/session 传递 `llm.Message` |

mcp 是传递依赖：`tools/mcp.go → internal/mcp`。

根因：**类型定义住在能力包里**。`llm.Message`、`llm.ToolCall`、`*tool.Registry` 这些是 agents 循环的直接依赖，类型不搬走，边界就破不了。

### 1.2 目标

1. **新增 `internal/agents/protocol` 共享契约包**：framework 与 capability 共用的类型全部住这里，protocol 只依赖 stdlib
2. **agents/ 只 import protocol**：`internal/agents/...` 全部改引用 `protocol.X`，不再 import `llm`/`tools`/`mcp`
3. **llm/tools 对外兼容别名**：已迁移类型在 llm/tools 里保留 `type X = protocol.X` 别名，skills/acp/gateway/config/cmd 等外部消费者**零改动**编译
4. **具体实现由 cmd/app 组装注入**：agent.New 收到的是 `protocol.ModelProvider` / `protocol.ToolRegistry` 接口，concrete 实现（anthropic provider、`*tool.Registry`）从外部传入
5. **严格 lint 规则全绿**：新增 `src/darvin-agent/Makefile` 的 `lint-agents-boundaries`，按计划原文执行

### 1.3 非目标

- **不搬** llm 专属接线/配置：`ProviderConfig` / `ProviderFactory` / `RegisterProvider` / `NewProvider` / `ErrUnknownProvider` / `ErrMissingAPIKey` / `Logger` 留在 `internal/llm`
- **例外（实现期调整）**：`ModelDescriptor` 及其依赖元数据（`APIKind` / `InputModality` / `ThinkingLevel` / `ModelCost` / `Compat`）+ `ModelRegistry` / `DefaultModelRegistry` 迁入 protocol —— `dispatcher.go` 的 `emitContextUsage` 要读 `ContextWindow`，不搬则 dispatcher 仍 import llm，边界破
- **不拆** `*tool.Registry` 结构体：它仍是 concrete 类型住 `internal/tools`，只是通过 `protocol.ToolRegistry` 接口暴露给 agents
- **不改**事件/消息/SQL schema、IPC 协议、provider 行为
- **不做** agents 内部定义接口 + 外部注入以外的任何重写（executor 逻辑、ctxengine 算法一律不动）
- **不新增** agent 外部依赖循环（protocol 只 import stdlib）

---

## 2. 现状耦合（已实测验证）

- 11 个 agents 非测试文件直接 import `llm`/`tools`：`agent.go`、`agent_mini_loop.go`、`dispatcher.go`、`event/event.go`、`session/session.go`、`executor/executor.go`、`ctxengine/{assembler,params,compact,tokens,assemble}.go`
- `agent_mini_loop.go` 的 `buildSkillTools` 在 agents 内部 `tool.NewRegistry()` / `full.List()` / `reg.RegisterTool(...)` 构造 concrete registry
- `agent.go:220` `tool.NewBuiltins` 是 agents 内部的 builtins 默认构造
- `llm` 包内部（同包）用裸标识符引用类型，别名化后无需改内部引用
- 外部消费者（skills/acp/gateway/config/cmd）引用 `llm.X`/`tool.X` 的地方：cmd 6 处、config 5 处、acp 3 处、gateway 2 处、skills 4 处（多为测试）

---

## 3. 设计

### 3.1 新增 `internal/agents/protocol`（共享契约，只依赖 stdlib）

**从 `internal/llm` 迁入**（types.go / events.go / provider.go）：

- 数据：`Role`、`Message`、`ImageBlock`、`Tool`、`ParameterSchema`、`ParameterProperty`、`ToolChoice`、`CompletionRequest`、`CompletionResponse`、`ToolCall`、`ToolResult`、`FinishReason`、`Usage`
- 流事件：`StreamEvent` + `StartEvent`、`AssistantMessage`、`TextDeltaEvent`、`ThinkingDeltaEvent`、`ToolCallStartEvent`、`ToolCallDeltaEvent`、`ToolCallEndEvent`、`DoneEvent`、`ErrorEvent`
- 流句柄：`StreamingResponse` + `NewStreamingResponse`（`Err`/`SetErr`/`Close` 方法）
- 接口：`ModelProvider`
- 模型元数据 + 注册表：`APIKind` / `InputModality` / `ThinkingLevel` / `ModelCost` / `Compat` / `ModelDescriptor` / `ModelRegistry` / `NewModelRegistry` / `DefaultModelRegistry`（dispatcher 读 `ContextWindow` 需要）

**从 `internal/tools` 迁入**（tool.go / permission.go）：

- 数据：`Result`、`Kind`、`Entry`、`Plugin`、`ToolRegistrar`、`PermissionEval`
- 接口：`Tool`
- **新增** `ToolRegistry` 接口（agents 消费面，见下）

**`ToolRegistry` 接口**（覆盖 agents 全部 registry 调用）：

```go
type ToolRegistry interface {
	Get(name string) Tool
	GetEntry(name string) (*Entry, bool)
	Specs() []Tool
	List() []*Entry
	SetGrantedReads(paths []string)
	ApprovePath(p string)
	EvaluatePermission(toolName string, args map[string]any) PermissionEval
	// ScopedForSkill 返回只含指定工具的 registry；空集合 => 空 registry。
	// 从 agents/agent_mini_loop.buildSkillTools 迁入。
	ScopedForSkill(allow []string) ToolRegistry
}
```

### 3.2 `internal/llm` 改造（提供方实现 + 兼容别名）

- `types.go`：删除已迁移类型定义，替换为 `type X = protocol.X` 别名；`ProviderConfig` 在 registry.go 不动
- `events.go`：全部改为 `type X = protocol.X` 别名
- `provider.go`：`ModelProvider` / `StreamingResponse` → 别名；`NewStreamingResponse` 迁到 protocol，anthropic 改调 `protocol.NewStreamingResponse`
- `model_registry.go`：删除（已迁 protocol）
- `registry.go` / `compat.go` / `errors.go` / `httpclient.go`：同包裸标识符引用，别名后自动兼容，**不动**
- `anthropic/*`：加 `import protocol`，构造流处改 `protocol.NewStreamingResponse`

> 效果：`llm.Message`、`llm.CompletionRequest` 等对外符号仍在（等价于 protocol 类型），skills/acp/gateway/config/cmd 零改动。

### 3.3 `internal/tools` 改造（concrete 实现 + 别名 + ScopedForSkill）

- `tool.go`：已迁移类型删除，改 `type X = protocol.X` 别名
- `permission.go`：`PermissionEval` → 别名（`ClassifyPermission` 逻辑留在 tools）
- `registry.go`：`*Registry` 结构体与方法保留；新增 `ScopedForSkill(allow []string) ToolRegistry`（迁入 `buildSkillTools` 逻辑：空集合→`NewRegistry()`；否则按名过滤 `List()` 后 `RegisterTool(e.Tool, e.Kind, e.Metadata)`）
- `mcp.go`：`llm` import 换 `protocol`（干净起见；`llm.Tool` 已是别名）

> `tool.Tool` 别名后，`tools` 内置工具与 skills/mcp 插件实现的仍是同一个接口，签名不变。

### 3.4 `internal/agents` 改造（只依赖 protocol）

- 11 个文件：`import "internal/llm"`/`"internal/tools"` 全部换 `"internal/agents/protocol"`；`llm.X`/`tool.X` 标识符改 `protocol.X`
- `agent.go`：
  - `provider llm.ModelProvider` → `provider protocol.ModelProvider`；`NewAgentConfig.Provider` 同理
  - `tools *tool.Registry` → `tools protocol.ToolRegistry`；`NewAgentConfig.Tools` 同理；`Tools() *tool.Registry` → `Tools() protocol.ToolRegistry`
  - `runSkillTools *tool.Registry` → `runSkillTools protocol.ToolRegistry`
  - `EvaluatePermission` 返回 `protocol.PermissionEval`
  - `RecordUsage`/`LastUsage` → `protocol.Usage`
  - **builtins 默认构造外移**：`New` 里 `tool.NewBuiltins` 分支删除；`NewAgentConfig.Tools == nil` → 返回 `ErrToolsRequired`
- `agent_mini_loop.go`：`buildSkillTools` 删除，改 `a.runSkillTools = a.tools.ScopedForSkill(names)`；`skillTools []protocol.Tool`
- `executor/executor.go`：`Deps.Tools() protocol.ToolRegistry`、`Deps.Provider() protocol.ModelProvider`、`RecordUsage`/`LastUsage` → `protocol.Usage`、`EvaluatePermission` → `protocol.PermissionEval`、`entryAttrs(reg protocol.ToolRegistry, ...)`；`llm.X`/`tool.X` → `protocol.X`
- `event/event.go`、`ctxengine/*`、`dispatcher.go`、`session/session.go`：`llm.X` → `protocol.X`
- 测试文件（`package agent` 内部测试）允许 import `tools`/`llm` 构造 concrete 实例——`go list -deps`（不带 `-test`）不统计测试依赖，严格规则不受影响

### 3.5 cmd/app + acp/factory 接线

- `cmd/app/main.go`：steerAgent 构造补 `Tools: <registry>`；主 AgentFactory 的 `Tools` 显式传入
- `internal/acp/factory.go`：`Build` 里 `f.Tools == nil` 时用 `tool.NewBuiltins(...)` 兜底（acp 不是 agents，允许 import tools）

### 3.6 新增 `src/darvin-agent/Makefile` + 严格 lint 规则

仓库当前无 Makefile，新建 `src/darvin-agent/Makefile`（计划原文规则）：

```makefile
# 不允许 agents/ 内部 import 任何"能力"包(llm/tools/skills/mcp/acp/gateway)
.PHONY: lint-agents-boundaries
lint-agents-boundaries:
	@echo ">>> Checking agents/ boundary violations..."
	@bad=0; \
	for pkg in $$(go list ./internal/agents/... 2>/dev/null); do \
		illegal=$$(go list -deps $$pkg 2>/dev/null | \
			grep -E 'darvin-cowork/backend/internal/(llm|tools|skills|mcp|acp|gateway|commands|memory|cron)' || true); \
		if [ -n "$$illegal" ]; then \
			echo "❌ $$pkg imports: $$illegal"; \
			bad=1; \
		fi; \
	done; \
	if [ $$bad -eq 0 ]; then \
		echo "✅ agents/ does not depend on capability packages"; \
	else \
		echo "❌ Violations found; agents/ must stay framework-only"; \
		exit 1; \
	fi
```

---

## 4. 验收

1. `go build ./...` 全绿
2. `go test ./...` 全绿（`TestHandleAbort_RoutesBySessionIDAndRunID` 已知偶发超时，单独跑通过即可）
3. `make lint-agents-boundaries` 输出 `✅ agents/ does not depend on capability packages`
4. 外部消费者零行为变化：skills/acp/gateway/cmd 的 `llm.X`/`tool.X` 引用未改（编译即证明）

## 5. 提交计划（step 4 拆两个 commit）

```bash
# Commit A：接口解耦（protocol 包 + llm/tools 别名 + agents 改造 + 接线）
git add src/darvin-agent
git commit -m "refactor(agents): decouple agents/ from capability packages via protocol contract"

# Commit B：严格 lint 规则
git add src/darvin-agent/Makefile
git commit -m "chore(agent): add lint-agents-boundaries to prevent capability backflow"
```

## 6. 风险

- **类型搬移量大**：别名机制保证外部兼容，编译期兜底；漏改 agents 内引用由 `go build` 报错兜底
- **`ErrToolsRequired` 改动面**：builtins 默认外移，相关测试补 `Tools` 参数（机械改动）
- **纯结构重构**：运行时行为不变；测试预期不动
