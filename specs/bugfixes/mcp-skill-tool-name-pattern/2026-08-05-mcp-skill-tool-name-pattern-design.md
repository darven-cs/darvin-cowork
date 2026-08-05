# MCP / Skill 工具名命名空间修复 — 设计文档

## 1. 概述

### 1.1 问题 / 背景

用户在重启应用后实测复现（playwright 连接运行中的 Electron 实例，当前会话新发
消息即触发）：

```
executor: stream: [anthropic] invalid_request_error (status=400):
Invalid 'tools[2].name': string does not match pattern.
Expected a string that matches the pattern '^[a-zA-Z0-9_-]+$'.
```

根因已定位：工具名命名空间用**冒号**做分隔符，而 Anthropic API 只允许
`^[a-zA-Z0-9_-]+$`。

- `internal/tools/mcp.go:69` — `mcpToolName()` 返回 `mcp:<server>:<tool>`，
  即 `mcp:filesystem:list_directory`。
- `internal/skills/plugin.go:58` — `skillToolName()` 返回 `skill:<id>`，
  即 `skill:code-review`（同类隐患，skill 启用即触发）。

`tools/registry.go:138` 的 `Specs()` 按名字典序发给 LLM。内置工具名
（`edit_file` / `list_dir` / `read_file` / `shell` / `write_file`）全部合法，
排在最前的两个合法，**第三个**就是第一个 MCP 工具 `mcp:filesystem:list_directory`
→ 报错 `tools[2]`。`llm/anthropic/convert.go:180` 的 `convertTools` 对名字
**原样透传**，不做任何清洗，所以冒号直达 API 被拒。

### 1.2 目标

- MCP 工具名改为 `mcp__<server>__<tool>`（如 `mcp__filesystem__list_directory`）。
- Skill 工具名改为 `skill__<id>`（如 `skill__code-review`）。
- 修复后带 MCP 工具的会话能正常走 LLM 请求，不再 400。

### 1.3 非目标

- **不做渲染层改动**：前端 `getToolKind`（`toolDisplay.ts:45`）不解析
  `mcp:`/`skill:` 前缀，工具 kind 来自 Go 端事件归属，改名后无需动 renderer。
- **不迁移已持久化的旧工具名**：历史消息里已存的 `mcp:filesystem:read_file`
  保持原样展示，仅新请求使用新格式（纯展示差异，无功能影响）。
- **不改 MCP 工具名合法性校验**：MCP 规范本身约束工具名 `^[a-zA-Z0-9_-]{1,64}$`，
  server id 由应用生成（`mcp_${randomUUID()}` / `filesystem`）必然合法，改分隔符
  即够，无需加 sanitize / 校验。
- **不改旧 spec 历史文档**：`specs/features/*` 属历史设计记录，保持原样。

## 2. 用户场景

### 场景 1: 带 MCP 工具的会话正常对话

**Given** Filesystem MCP server 已连接（工具 `list_directory` / `read_file` /
`write_file` 注册为 `mcp__filesystem_*`）
**When** 用户在会话里发一条消息
**Then** Anthropic 请求不再报 `tools[N].name` 400，模型正常收到 8 个工具
（5 内置 + 3 MCP）并开始回复。

### 场景 2: 启用 skill 后不复发同类错误

**Given** 启用某用户可调 skill（工具名 `skill__<id>`）
**When** 会话发起 LLM 请求
**Then** 工具数组里 `skill__<id>` 通过 `^[a-zA-Z0-9_-]+$` 校验，不再 400。

### 场景 3: 工具路由不受影响

**Given** 模型调用 `mcp__filesystem__read_file`
**When** Go 端 `Registry.Get(name)` 命中 → `McpTool.Execute`
**Then** `Execute` 仍用原始 `toolDesc.Name`（`read_file`）调 MCP server，路由与
事件归属（`mcpServerID` / `mcpToolName` metadata）不变。

## 3. 功能需求

### FR-1: `mcpToolName` 改用双下划线分隔

`internal/tools/mcp.go`：

```go
// 旧：return "mcp:" + serverID + ":" + toolName
func mcpToolName(serverID, toolName string) string { return "mcp__" + serverID + "__" + toolName }
```

### FR-2: `skillToolName` 改用双下划线分隔

`internal/skills/plugin.go`：

```go
// 旧：return "skill:" + skillID
func skillToolName(skillID string) string { return "skill__" + skillID }
```

### FR-3: 同步更新测试与注释中的工具名

凡引用 `mcp:...` / `skill:...` 工具名的断言、事件名、注释全部换成新格式，
保证 grep 无冒号残留。

## 4. 实现方案

### 4.1 两处命名函数（唯一生产代码改动）

`mcpToolName` / `skillToolName` 各一行。名字只作为 registry key 用，路由靠
metadata（`mcpServerID`/`mcpToolName`/`skillID`）与 `McpTool` / `SkillTool`
结构体字段，改名零副作用。

### 4.2 测试改动清单（逐文件）

| 文件 | 替换 |
|------|------|
| `internal/tools/mcp_test.go` | `mcp:filesystem:read_file`→`mcp__filesystem__read_file`；`mcp:filesystem:list_directory`→`mcp__filesystem__list_directory`；`mcp:fs:read_file`→`mcp__fs__read_file` |
| `internal/skills/plugin_test.go` | `skill:web-search`→`skill__web-search`；`skill:code-review`→`skill__code-review` |
| `internal/agents/agent_mini_loop_test.go` | `skill:code-review`→`skill__code-review`；`skill:web-search`→`skill__web-search`；`mcp:filesystem:list_directory`→`mcp__filesystem__list_directory` |
| `internal/gateway/handlers_test.go` | `skill:test-skill`→`skill__test-skill`；`skill:code-review`→`skill__code-review` |
| `internal/gateway/eventledger_test.go` | 事件 `Name: "skill:web-search"`→`"skill__web-search"`；wire 断言 `"tool": "skill:web-search"`→`"skill__web-search"` |
| `internal/tools/registry_test.go` | stub 名 `skill:a/b/z`→`skill__a/b/z`；`mcp:c/x`→`mcp__c/x`（统一命名空间约定） |

### 4.3 注释

- `cmd/app/main.go:311` 注释 `skill:<id> 与 mcp:<server>:<tool>` →
  `skill__<id> 与 mcp__<server>__<tool>`。

### 4.4 文档核查

`docs/agent/05_MCP_INTEGRATION.md:144` 与 `00_OVERVIEW.md:301,316` 是配置 YAML
的 `mcp:` key，与工具名无关 → **不改**。`specs/features/*` 历史设计文档不改。

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| 历史会话里已存 `mcp:filesystem:read_file` | 展示原样，新请求用新名；无迁移 |
| MCP 工具名含下划线（如 `list_allowed_directories`） | `mcp__fs__list_allowed_directories` 仍合法（下划线在允许集内） |
| 无 MCP / skill 工具的会话 | 只有 5 内置工具，本来就合法，不受影响 |
| 第三方 MCP server id / 工具名含点 | MCP 规范约束工具名 `[a-zA-Z0-9_-]`、server id 应用生成，均合法；不额外 sanitize |
| 前端 `getToolKind` 对未知名的兜底 | 展示的是原始字符串（改名前后同性质），非回归 |

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/darvin-agent/internal/tools/mcp.go` | `mcpToolName` 分隔符 `:`→`__` |
| `src/darvin-agent/internal/skills/plugin.go` | `skillToolName` 分隔符 `:`→`__` |
| `src/darvin-agent/cmd/app/main.go` | 注释更新 |
| `src/darvin-agent/internal/tools/mcp_test.go` | 断言工具名更新 |
| `src/darvin-agent/internal/skills/plugin_test.go` | 断言工具名更新 |
| `src/darvin-agent/internal/agents/agent_mini_loop_test.go` | 断言工具名更新 |
| `src/darvin-agent/internal/gateway/handlers_test.go` | 断言工具名更新 |
| `src/darvin-agent/internal/gateway/eventledger_test.go` | 事件名 / wire 断言更新 |
| `src/darvin-agent/internal/tools/registry_test.go` | stub 名统一新格式 |

## 7. 验收标准

- [x] 场景 1：playwright 连运行中应用，带 MCP 的会话发消息不再报
      `Invalid 'tools[2].name'`，模型正常返回（实测：新发「1+1等于多少」正常回复，
      「列目录」触发工具调用并返回表格）
- [x] 场景 2：启用 skill 的会话不触发同类 400（`skill__<id>` 合法）
- [x] 场景 3：模型调用 `mcp__filesystem__read_file` 能正常路由并执行
- [x] `grep -rE '"mcp:|"skill:|mcp:<|skill:<' src/darvin-agent` 无生产代码残留
      （仅剩 `registry.go` / `client.go` 的错误**消息字符串**，非工具名，保留）
- [x] `go build` / `go vet` 通过
- [x] `go test -short ./...` 全绿（27 包）
