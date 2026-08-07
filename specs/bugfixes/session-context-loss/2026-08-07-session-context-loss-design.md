# 会话切换后上下文丢失 — 设计文档

## 1. 概述

### 1.1 问题

用户切换会话（切到另一会话再切回来）后，agent 完全不记得之前对话的内容。实测复现：

```
session A 发事实：名字=Zorblax Quill / 颜色=孔雀蓝 / 数字=42
→ 切到 B 发 "1+1"
→ 切回 A 问 "我叫什么"
agent 答："我不知道这些信息——你从未告诉过我..."
```

但持久化正常：`get_messages` 显示 session A 有 4 条消息（2 user + 2 assistant），UI 渲染历史完整。即 **UI 有历史、LLM 无上下文**。

### 1.2 根因（双因叠加）

**根因 B（触发条件）：切换 session 会重启整个 Go agent 子进程。**

`src/main/index.ts:184-189` `followActiveWorkspace` 在切 session 时调 `restartGoSubprocess`：
```ts
async function followActiveWorkspace(sessionId: string): Promise<void> {
  if (workspaceLoc && workspaceLoc.workspaceId === sessionId) return;
  workspaceLoc = resolveWorkspaceRoot(sessionId);
  await ensureWorkspaceRoot(workspaceLoc);
  await restartGoSubprocess(workspaceLoc.rootPath);   // 每次切会话都重启！
}
```
`restartGoSubprocess`（`index.ts:1119-1147`）`mgr.stop()` → `mgr.start()` 重新 spawn。`switch_session`（`index.ts:325-341`）、`create_session`（`:297-298`）、`delete_session`（`:382`）都会走到这里。Go 进程一重启，所有 in-memory Session 全部清空。

**根因 A（放大因素）：Go 重启后重建的 Agent 从不从持久化 store 恢复消息。**

- `internal/agentloop/factory.go:126` `Build` 用 `session.NewSession(sessionID)` 创建**空**内存 Session。
- 全仓搜 `ReplaceAll` / `Load`：只有 compact_context 手动压缩路径（`handlers.go:530`）会把消息放回内存 Session；**没有任何「创建 AgentLoopSession 时从 MessageStore 加载历史」的代码**。
- `getMessages`（`handlers.go:635`）读的是持久化 `MessageStore`，与 agent 内存 Session 是两套，所以 UI 与 LLM 上下文脱节。

注释也承认了设计意图：`index.ts:182`「切换会话会中断其它在途流式，成本约 1s，本版本接受（后续可做 Go set_workspace RPC 消除重启）」。**但配套的 store hydrate 一直没做**，导致重启后上下文彻底丢失。

### 1.3 目标

- **A（兜底）**：AgentLoopSession 创建时从持久化 `MessageStore` 加载该 session 历史消息进内存 Session。即使进程重启 / entry 重建，agent 也能恢复记忆。
- **B（治本）**：新增 Go 侧 `agent.set_workspace` RPC，main 端切 session 时不再重启 Go 子进程，而是运行时重锚 workspace。保留其它 session 的 in-memory 上下文与在途流式。

### 1.4 非目标

- 不解决「imported_files 相对路径跨 workspace 失效」的产品语义问题（与现状 restart 行为一致，见边界情况）。
- 不解决超长会话 token 预算（assembler 已负责压缩，hydrate 只负责把库里的消息还原）。
- 不引入会话级沙箱隔离（当前 fsSandbox 是进程级单例，set_workspace 全局切换，符合现状语义）。

---

## 2. 用户场景

### 场景 1: 切换会话再切回，agent 记得之前的对话

**Given** session A 里 agent 已知道用户名字/颜色/数字（已落库）
**When** 用户切到 session B 聊了无关内容，再切回 session A 提问「我叫什么」
**Then** agent 正确答出事实（恢复自持久化消息，或恢复自未重启时保留的内存 Session）

### 场景 2: 重启应用后，agent 记得之前的对话

**Given** session A 有历史对话（已落库）
**When** 重启 Electron / Go agent，重新进入 session A 提问
**Then** agent 能引用历史对话内容（hydrate 从 store 恢复）

### 场景 3: 切换会话不中断其它 session 的在途流式

**Given** session A 正在流式输出（run in-flight）
**When** 用户切到 session B
**Then** A 的流式输出不被中断（消除重启），切回 A 时输出继续/已完整

---

## 3. 功能需求

### FR-A1: AgentLoopSession 创建时 hydrate 历史

`factory.NewAgentLoopSession`（懒建链的唯一收口）在 `Build` 之后、任何 `Submit` 之前，从 `f.MessageStore` 加载该 session 消息并 `Session().ReplaceAll()`。`MessageStore` 为 nil 时跳过（沿用 dispatcher nil-store 语义）。

### FR-A2: MessageRecord → protocol.Message 转换规则

- `role=="user"` → `{Role: RoleUser, Content}`
- `role=="assistant"` → `{Role: RoleAssistant, Content, ToolCalls: json.Unmarshal(rec.ToolCalls)}`；再对每个 `tc.Result != nil` 追加 `{Role: RoleTool, Content: tc.Result.Content, ToolCallID: tc.ID}`
- `role=="system"` → 跳过（Anthropic converter 会丢弃 messages 里的 system，见 `llm/anthropic/convert.go:107-113`）
- 其它 / `!rec.Done` 的 assistant 残留行 → 跳过（streaming 中断残留，见边界情况）
- 顺序按 `MessageStore.List` 返回（`timestamp asc`），直接 append

### FR-A3: hydrate 幂等与失败策略

- 天然幂等：`attachAgentLoopLocked` 只在 `e.AgentLoop == nil` 时调一次，每 entry 只 hydrate 一次；`ReplaceAll` 覆写兜底。
- 失败策略：warn-and-continue（与 `factory.go:139-152` 插件失败风格一致），不阻塞 AgentLoopSession 构造。

### FR-B1: Go 侧 `agent.set_workspace` RPC

- params：`{ sessionId, rootPath }`；`rootPath` 必须绝对路径且为存在的目录（`os.Stat` + `IsDir` 校验）。
- 生效内容：
  - `fsSandbox.SetRoot(rootPath)`：带锁更新 `root`/`realRoot`（复用 `newFsSandbox` 的 Abs + EvalSymlinks 逻辑），并给现有无锁读 root 的点补锁。
  - `Handler.WorkspaceRoot = rootPath`（`handlers.go:290-292`，供 `import_files` containment check）。
  - 重扫项目 skills：`skills.Bootstrap` 重新装载 `<workspace>/skills`，再 `SessionManager.RefreshAllTools()` 刷新工具面。
- 失败返回 RPC error；成功返回 `{ rootPath }`。

### FR-B2: main 端切换不再重启

- `followActiveWorkspace`（`index.ts:184-189`）：`ensureWorkspaceRoot` 后改调 `client.request('agent.set_workspace', { rootPath })`，不再 `restartGoSubprocess`。
- `setWorkspaceRootTo`（`index.ts:643-658`）：写映射后改调 `agent.set_workspace`，不再重启。
- 保留重启的两个场景：
  - **首次启动 / 启动期重连**（`index.ts:1187`）：`mgr.start()` 注入 `DARVIN_AGENT_WORKSPACE` env，Go 冷启动用。
  - **`set_llm_config` anthropic 场景**（`index.ts:1032`）：写入 yaml 后必须重启加载新 key，**保留 restartGoSubprocess**。
- `AgentClient` 新增 `setWorkspace(rootPath)` 方法（`client.ts`），对应 `darvin-api.ts` 的契约更新。

### FR-B3: workspace 变更广播保持现状

`broadcastWorkspaceChanged` / `workspaceLoc` 更新逻辑不变（main 端是 workspace 文件的 source of truth，见调研：`list_workspace_files` / `read_workspace_file` 走 Node fs，不依赖 Go）。

---

## 4. 实现方案

### 4.1 A: hydrate（Go）

**新增 `src/darvin-agent/internal/agentloop/hydrate.go`**：
- `func hydrateSession(ctx context.Context, f *AgentFactory, sess *session.Session) error`
  - `if f.MessageStore == nil { return nil }`
  - `rows, err := f.MessageStore.List(ctx, sess.ID, 0, 0)`（默认 limit 1000）
  - 逐行转 `[]protocol.Message`（规则见 FR-A2）
  - `sess.ReplaceAll(msgs)`
- helper `func recordToMessages(rec store.MessageRecord) ([]protocol.Message, error)`：单测用。

**修改 `src/darvin-agent/internal/agentloop/factory.go`**：
- `NewAgentLoopSession`（`factory.go:67-89`）里 `Build` 成功后、返回前调用 `hydrateSession`；失败 `f.Logger.Warn`，不阻塞。

**新增 `src/darvin-agent/internal/agentloop/hydrate_test.go`**：
- 注入 SQLite/memory MessageStore，预存 user + assistant（带 ToolCalls JSON，含 `result`）记录，断言 `Agent.Session().Messages()` 数量/顺序，特别是 tool 消息重建与 `ToolCallID == tc.ID`。
- 空历史 / nil MessageStore：返回空 Session。
- 参考 `factory_test.go:25-38` `newTestFactory()`，补一个带 `MessageStore` 的变体。

### 4.2 B: set_workspace RPC（Go + main）

**Go — `src/darvin-agent/internal/tools/sandbox.go`**：
- 新增 `SetRoot(newRoot string) error`：复用 Abs + EvalSymlinks，锁内更新 `root`/`realRoot`。
- 给 `Resolve`（`sandbox.go:100`）、`shell.go:105`（默认 cwd 读 root）、`openRootFile` 等无锁读点补锁或使用安全快照。

**Go — `src/darvin-agent/internal/tools/registry.go`**（或 runtime 层）：
- 暴露 `SetWorkspaceRoot(newRoot string) error` 入口，转发到 `sb.SetRoot`。

**Go — `src/darvin-agent/internal/gateway/handlers.go`**：
- `dispatchRequest`（`:340-409`）加 `case "agent.set_workspace"` → `handleSetWorkspace`。
- `handleSetWorkspace`：校验绝对路径 + 目录存在；调 sandbox `SetRoot`；更新 `h.WorkspaceRoot`；重扫项目 skills + `RefreshAllTools`；返回 `{ rootPath }`。
- 新增 `SetWorkspaceParams` / `SetWorkspaceResult` 类型（带 JSON tag）。

**Go — skills 重扫**：
- `runtime/skills.go` 现有 `bootstrapSkills(ctx, log, workspace, toolsReg)` 可复用；set_workspace 路径需要一次带新 workspace 的重新装载。具体：从 runtime 持有 `skillsResult` 的 registry，或用现有 `skillPlugin` 重建注册面。

**main — `src/main/runtime/client.ts`**：
- 新增 `setWorkspace(rootPath: string): Promise<...>`，`request('agent.set_workspace', { rootPath })`。

**main — `src/shared/darvin-api.ts`**：
- `DarvinApi` 增加 set_workspace 相关契约（如 `setAgentWorkspace(rootPath)`）或复用现有 IPC 形状；保持一致类型。

**main — `src/main/index.ts`**：
- `followActiveWorkspace`（`:184-189`）：`ensureWorkspaceRoot` 后改调 `client.setWorkspace(rootPath)`，不再 `restartGoSubprocess`。
- `setWorkspaceRootTo`（`:643-658`）：写映射 + `client.setWorkspace` + `broadcastWorkspaceChanged`。
- `restartGoSubprocess`（`:1119`）保留，仅启动期 + set_llm_config 用。

### 4.3 依赖注入链路确认

`runtime/factory.go` 已把 `MessageStore` 注入 `AgentFactory.MessageStore`（`factory.go:28`），A 方案零额外装配；`runtime.go` 已构造 `tools` Registry，B 方案 sandbox 引用可从 `Runtime` 透传给 handler。

---

## 5. 边界情况

| 场景 | 处理方式 |
| ---- | -------- |
| `MessageStore` 为 nil（单测/精简路径） | hydrate 跳过，返回空 Session，行为与现状一致 |
| `role=="system"` 的历史行 | 跳过（Anthropic 会丢 system；避免脏上下文） |
| assistant 残留行 `Done=false`（streaming 中断） | 跳过，避免把半截文本喂进 LLM |
| assistant 行带 `ToolCalls[].Result` | 必须重建 `RoleTool` 消息且紧跟 assistant 行（否则 Anthropic 400 `invalid_request_error`） |
| 超长 session > 1000 条 | `List` 默认 limit 1000，超过部分不 hydrate；LLM 上下文受 token 预算限制，assembler 会压缩，可接受 |
| hydrate 并发 | 创建阶段 `attachAgentLoopLocked` 持 `m.mu`，无 run 在途，无竞态 |
| `set_workspace` 时 workspace 目录不存在 | RPC 返回 error，main 端 toast；`ensureWorkspaceRoot` 已先建目录 |
| `set_workspace` 并发（工具执行中途改 root） | sandbox `SetRoot` 带锁；per-call 读 root 取快照，不中断 in-flight run |
| imported_files 跨 workspace | 按 session 隔离，行不删不迁；RelativePath 相对各自 session 的 workspace，语义正确，无需处理 |
| set_llm_config | 保留 `restartGoSubprocess`（必须冷启动加载新 key） |
| 首次启动 | 保留 `mgr.start()` env 注入（冷启动） |

---

## 6. 涉及文件

| 文件 | 变更说明 |
| ---- | -------- |
| `src/darvin-agent/internal/agentloop/hydrate.go` | **新增**：hydrateSession + recordToMessages |
| `src/darvin-agent/internal/agentloop/hydrate_test.go` | **新增**：转换/顺序/tool 重建测试 |
| `src/darvin-agent/internal/agentloop/factory.go` | `NewAgentLoopSession` 调 hydrate |
| `src/darvin-agent/internal/tools/sandbox.go` | 新增 `SetRoot` + 读点补锁 |
| `src/darvin-agent/internal/tools/registry.go` | 暴露 `SetWorkspaceRoot` |
| `src/darvin-agent/internal/gateway/handlers.go` | `agent.set_workspace` handler + 类型 + 更新 `Handler.WorkspaceRoot` |
| `src/darvin-agent/internal/gateway/handlers_test.go` | set_workspace 单测（校验、更新、失败路径） |
| `src/darvin-agent/internal/runtime/runtime.go` | set_workspace 依赖注入（sandbox 引用、skills 重扫入口） |
| `src/main/runtime/client.ts` | 新增 `setWorkspace` 方法 |
| `src/shared/darvin-api.ts` | set_workspace 契约 |
| `src/main/index.ts` | `followActiveWorkspace` / `setWorkspaceRootTo` 改调 RPC，不再重启 |
| `src/main/libs/user-paths.ts` | （如需要）workspace 相关 helper 调整 |

---

## 7. 验收标准

### 用户场景
- [ ] **场景 1**：切到 B 再切回 A，agent 能答出 A 里的历史事实（实测通过）
- [ ] **场景 2**：重启后重新进入 session A，agent 能引用历史对话（hydrate 生效）
- [ ] **场景 3**：A 在途流式不因切到 B 而中断

### 自动化
- [ ] `go test ./...`（`src/darvin-agent/`）通过，含 hydrate_test.go 与 handlers_test.go 新增用例
- [ ] `npm run lint` 通过
- [ ] `npm run test`（vitest）通过

### 手动验证
- [ ] `npm start` 起应用，新建会话发事实 → 切走 → 切回 → 提问能回忆
- [ ] 重启应用，同会话提问能回忆（hydrate）
- [ ] 切会话观察 DevTools console 无「restart」日志（确认不再重启），在途流式不断
- [ ] 设置里改 anthropic API key → 仍正常重启并生效（保留路径）
