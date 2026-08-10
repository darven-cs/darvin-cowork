# sessionruntime 包改名设计文档

> 背景：`specs/refactors/agentloop-naming-review/2026-08-10-agentloop-naming-review-design.md` 已评估 `internal/agentloop` 命名偏窄（实际是 per-session agent 运行时），三方案中用户选择**改名**。
> 本 spec 是执行稿：包名 `agentloop` → `sessionruntime`，类型名同步理顺，参考 DeepSeek-Reasonix 的「单数领域名词 + 复合词」包名风格（其 `botruntime` / `taskcontract` / `taskmonitor` 为同类先例）。

## 1. 概述

### 1.1 问题 / 背景

`internal/agentloop` 实际承担「**per-session agent 运行时**」的 4 类职责：Loop 串行调度、Agent 装配（AgentFactory）、历史恢复（hydrate）、生命周期容器。但包名字面只覆盖「Loop」，消费方 gateway 读起来是 `entry.AgentLoop.Loop.Submit(...)` 双层 loop 语义，且类型 `AgentLoopSession` 把「session 运行时容器」叫成了「loop session」。

darvin 分层现状：

| 层 | 包 | 命名 |
|---|---|---|
| 进程装配 | `internal/runtime` | ✅ 清晰（Build/Run/Shutdown） |
| 入口 | `internal/gateway` | ✅ 清晰（WS + JSON-RPC） |
| **per-session 运行时** | **`internal/agentloop`** | ❌ 名不符实 |
| 执行抽象 | `internal/harness` | ✅ 清晰 |
| Agent 本体 | `internal/agents` | ✅ 清晰 |
| 能力层 | `tools` / `llm` / `skills` / `mcp` / `memory` / `subagent` | ✅ 清晰 |

只有 `agentloop` 一层命名与职责错位。

### 1.2 目标

- 包名 `agentloop` → **`sessionruntime`**（per-session agent 运行时：装配 + 调度 + 生命周期 + 恢复）。
- 类型 / 方法 / 字段名同步理顺，去除 `AgentLoop` / `AgentLoopSession` 残留。
- 纯重命名，零行为变更；`go build` / `go vet` / `go test ./...` 全绿。

### 1.3 非目标

- 不改包内类型 `Loop`（它就是串行 turn 循环，名字准确）。
- 不改 `AgentFactory` / `PromptRequest` / `SkillInvocation` / `RunTicket`（名字清晰）。
- 不做拆包（保持现状的包边界，只改名）。
- 不动 renderer / IPC（Go 端内部改名，协议不变）。

## 2. 参考对照（DeepSeek-Reasonix 命名哲学）

Reasonix 的 `internal/`（约 80 个包）命名规律：

- **单数领域名词**：`agent` / `tool` / `skill` / `memory` / `store` / `event` / `command` / `capability`。
- **复合词可接受**：`botruntime`（bot 运行时）、`taskcontract`（任务契约）、`taskmonitor`（任务监控）、`desktoplauncher`。
- **loop / run / executor 不单独成包**：全部收在 `agent` 包内以领域文件名组织（`run_loop.go` / `task.go` / `scheduler.go` / `subagent_store.go`）。

darvin 采用多包分层（与 Reasonix 单包+领域文件不同），但**包名语义**对齐其「单数领域名词 + 复合词」风格：`sessionruntime` 与 Reasonix `botruntime` 同构，表达「一个 session 的运行时」。

## 3. 实施方案

### 3.1 包改名

```
internal/agentloop/  →  internal/sessionruntime/
```

包内 10 个文件 `package agentloop` → `package sessionruntime`：

- factory.go / factory_test.go / hydrate.go / hydrate_test.go / loop.go / loop_harness_test.go / loop_test.go / session.go / testharness.go

### 3.2 类型 / 方法 / 字段改名

| 旧 | 新 | 位置 |
|---|---|---|
| `AgentLoopSession` | `SessionRuntime` | `session.go` 定义 |
| `NewAgentLoopSession` | `NewSessionRuntime` | `factory.go` 构造 + 调用处 |
| `entry.AgentLoop`（字段） | `entry.SessionRuntime` | `gateway/sessionmgr.go` SessionEntry 字段 + 所有引用 |
| `CodeNoAgentLoopSession` | `CodeNoSessionRuntime` | `gateway/jsonrpc.go` 错误码 + 引用处 |

### 3.3 import 替换

10 个文件 import `"darvin-cowork/backend/internal/agentloop"` → `"darvin-cowork/backend/internal/sessionruntime"`：

- `internal/runtime/{runtime,harness,factory}.go`
- `internal/gateway/{sessionmgr,handler_prompt,handler_skill}.go`
- `internal/gateway/{gateway_integration_test,handlers_test,sessionmgr_test,handlers_skill_test}.go`

同步所有 `agentloop.` 前缀调用 → `sessionruntime.`（如 `sessionruntime.PromptRequest`、`sessionruntime.NewSessionRuntime(...)`、`sessionruntime.AgentFactory`）。

### 3.4 注释同步（仅文字，无 import）

以下文件注释引用了 `agentloop.`，改为 `sessionruntime.`：

- `internal/agents/agent.go:192`（`subagents wired by agentloop.AgentFactory`）
- `internal/agents/agent_mini_loop.go:20`
- `internal/agents/executor/executor.go:100`
- `internal/subagent/manager.go:65`
- `internal/gateway/server.go:59`

### 3.5 文档同步

| 文档 | 改动 |
|---|---|
| `CLAUDE.md` | 第 200 行 `internal/agentloop/` → `internal/sessionruntime/`，同时补全 4 类职责（顺带解决「文档比代码窄」） |
| 既有 specs | 引用 `internal/agentloop/xxx.go` 的旧 spec 路径**不逐一改写**（历史决策记录），新 spec 一律用 `sessionruntime` |

## 4. 涉及文件

| 文件 | 变更 |
|---|---|
| `internal/sessionruntime/*.go`（10 个，由 agentloop/ 改名） | `package agentloop` → `package sessionruntime`；`AgentLoopSession` → `SessionRuntime`；`NewAgentLoopSession` → `NewSessionRuntime` |
| `internal/runtime/runtime.go` / `harness.go` / `factory.go` | import + `agentloop.` 前缀 |
| `internal/gateway/sessionmgr.go` | import + `AgentLoop` 字段 → `SessionRuntime` + `agentloop.AgentLoopSession` 类型引用 |
| `internal/gateway/handler_prompt.go` / `handler_skill.go` | import + `entry.AgentLoop` → `entry.SessionRuntime` + 错误码常量 |
| `internal/gateway/handler_mcp.go` / `jsonrpc.go` / `handler_session.go` | `CodeNoAgentLoopSession` → `CodeNoSessionRuntime` |
| `internal/gateway/{gateway_integration_test,handlers_test,sessionmgr_test,handlers_skill_test}.go` | import + 类型/字段引用 |
| `internal/agents/agent.go` / `agent_mini_loop.go` / `executor/executor.go` / `subagent/manager.go` / `gateway/server.go` | 注释文字同步 |
| `CLAUDE.md` | 第 200 行描述更新 |

## 5. 验收标准

- [ ] `internal/agentloop/` 目录不存在，`internal/sessionruntime/` 存在；包内 `package sessionruntime`
- [ ] 全仓无 `agentloop` 残留（`grep -rn "agentloop" src/darvin-agent/ --include="*.go"` 为空）
- [ ] 无 `AgentLoopSession` / `NewAgentLoopSession` / `CodeNoAgentLoopSession` 残留
- [ ] `go build ./...`、`go vet ./...`、`go test ./...` 全绿
- [ ] `goimports -l .` 输出为空（import 分组未破坏）
- [ ] CLAUDE.md 第 200 行更新为 `sessionruntime` 且覆盖 4 类职责
- [ ] gateway 读路径自检：`entry.SessionRuntime.Loop.Submit(sessionruntime.PromptRequest{...})` 语义清晰
