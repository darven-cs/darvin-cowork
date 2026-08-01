# 会话工作区隔离 + 上下文用量上报 + 工作目录打开

> 编号 **补充 10**。修复 live 实测的 3 个问题：(1) 导入文件不按会话隔离——切换会话后前端仍显示 bootstrap 会话的 workspace；(2) 上下文用量（圆环/token）从不显示——Go agent 不发 `context_usage` 事件；(3) composer context 行的工作目录不可点击打开文件夹。**提前执行，先于 05-08。**

## 1. 背景

### 1.1 问题 1：文件不区分会话（live 复现）

当前架构下 `workspaceLoc` 是 main 进程**单例全局**（`src/main/index.ts:101`），只在启动 bootstrap 时由 active session 解析一次并**保持固定**（`index.ts:96-100` 注释「v0 在启动期由 active session 解析一次并保持固定」）。`switch_session`（`index.ts:252-266`）只调 `agent.set_active_session` + 广播，**不更新 workspaceLoc**。

后果：
- `list_imported_files` / `import_files` / `remove_imported_file` / `get_workspace_info` / `reveal_workspace` 全部走固定的 `workspaceLoc.workspaceId`（bootstrap 会话），与 active session 无关。
- Go agent 的 fsSandbox 根由子进程 env `DARVIN_AGENT_WORKSPACE` 决定（`restartGoSubprocess(workspaceRoot)` 注入），启动后同样固定 → agent 的 `read_file` / `write_file` / shell 都落在 bootstrap 会话的 workspace。
- renderer 的 `useImportedFiles`（`src/renderer/composables/useImportedFiles.ts`）是模块级单例（`files` / `workspaceBytes` ref），初始 `listImportedFiles()` + `onWorkspaceChanged` 回调直接覆盖全局 ref，**不 watch active session**。

live 实测：切到会话 `2Iqp...` 后，`getActiveSession()` 返回 `2Iqp...`，但 `getWorkspaceInfo().label` 仍为 `bJmq...`（bootstrap 会话），`listImportedFiles()` 返回 bootstrap 会话的文件。前端 ImportedFilesBar / context 行展示与 active session 错位。

### 1.2 问题 2：上下文用量从不显示（live 复现）

spec 03 落地了圆环组件与 renderer 端 `contextUsageBySessionId`，但 **Go agent 从不 emit `context_usage` 事件**（spec 03 已知 gap：「Go 不推 context_usage 事件 → 真实会话圆环不会出现」）。`done` 事件只带**单次 turn** 的 usage（供 TurnMeta），不是 session 级上下文快照。

但 Go 侧**数据都在**：
- `Agent.LastUsage()`（`agent.go:287`）返回最近一次 turn 的 API usage，其中 `PromptTokens` = 该 turn 发送给 LLM 的完整 prompt token 数 ≈ 当前上下文占用。
- 模型上下文窗口在 `llm.DefaultModelRegistry.Get(modelID).ContextWindow`（anthropic = 200000）。

live 实测：composer 工具栏 `.context-usage-indicator` 不存在（`ringPresent: false`）。

### 1.3 问题 3：工作目录不可点击（live 复现）

`ComposerContextRow.vue` 的工作目录 chip 是 `<span>`（`workspaceTag: SPAN`），不可点击。`window.darvin.revealWorkspace()`（main 已实现 `shell.showItemInFolder`）没被接上。

## 2. 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | workspace 跟随 active session：导入/移除/列表/用量/reveal 都作用于当前会话 | 切换会话后 `getWorkspaceInfo().label` + `listImportedFiles()` 与 active session 一致；ImportedFilesBar 只显示当前会话文件 |
| G2 | Go agent 每次对话完成后 emit `context_usage` | 完成一次 prompt 后圆环出现并显示 percent / used / context tokens |
| G3 | agent 文件工具（read/write/shell）读写的 workspace 与 active session 一致 | 会话 A 导入的文件在会话 B prompt 时读不到（沙箱随会话切换） |
| G4 | context 行工作目录可点击打开文件夹 | 点击 chip 调 `revealWorkspace()`，系统文件管理器打开该会话 workspace |

## 3. 非目标

- 不做「最近目录」历史 / 目录选择器（沿用 09 的 v0 只读 + 打开）。
- 不改 LLM 上下文窗口数值（用 `DefaultModelRegistry` 既有 `ContextWindow`）。
- 不做动态 `set_workspace` RPC 免重启方案（见 §7 已知成本）；切换会话的子进程重启成本先接受。
- 不处理多窗口 / 多会话并行流式在切换时的中断（记录为已知边界，见 §6）。

## 4. 设计要点

### 4.1 main：workspace 跟随 active session

新增 helper，在 active session 变化的三个入口统一调用：

```ts
async function followActiveWorkspace(sessionId: string): Promise<void> {
  if (workspaceLoc && workspaceLoc.workspaceId === sessionId) return;
  workspaceLoc = resolveWorkspaceRoot(sessionId);
  await ensureWorkspaceRoot(workspaceLoc);
  await restartGoSubprocess(workspaceLoc.rootPath); // 重锚 agent 沙箱根
}
```

调用点与顺序（先改 workspace 再广播，避免 renderer 读到中间态）：

- **`darvin:switch_session`**：`agent.set_active_session` 成功后 → `await followActiveWorkspace(r.sessionId)` → `refreshSessionsAndBroadcast()` → `broadcastActiveSession()` → `broadcastWorkspaceChanged(r.sessionId)`。
- **`darvin:create_session`**：`agent.create_session` 成功后 → `cache.activeSessionId = r.session.id` → `await followActiveWorkspace(r.session.id)` → `subscribeEvents` → 广播同上。
- **`darvin:delete_session`**：删完 → 若 active 推进到 `nextActiveSessionId`，`followActiveWorkspace(nextActiveSessionId)`；若 next 为 null 则 `workspaceLoc = null`（无会话空态）。

> `restartGoSubprocess`（`index.ts:550`）已实现「停旧进程 → 以 workspaceRoot 起新进程 → connect → subscribeAllSessions → eventRouter.start」，复用即可。二进制缺失返回 false 不抛，UI 用 broadcast 兜底（files 来自 DB，不依赖沙箱）。

### 4.2 renderer：`useImportedFiles` 按 session 重置

`useImportedFiles.ts`：
1. `useSession()` 引入 `activeSessionId`；模块级 `watch(() => session.activeSessionId.value, ...)` 注册一次：
   - 变化时立即 `files.value = []` / `workspaceBytes.value = 0` / `notice.value = null`（防旧数据闪现）；
   - 再 `listImportedFiles()` refetch 当前会话文件（main 已按 workspaceLoc 服务）。
2. `onWorkspaceChanged` 回调加**会话过滤**：`if (info.sessionId !== session.activeSessionId.value) return;`（防跨会话广播串数据）。

### 4.3 Go：emit `context_usage` 事件

**新事件**（`src/darvin-agent/internal/agent/event/event.go`）：

```go
// ContextUsageEvent reports the session's context occupancy after a turn
// completes; the renderer drives the ring from this snapshot.
type ContextUsageEvent struct {
	EventBase
	UsedTokens    int
	ContextTokens int
	Percent       int
}
func (ContextUsageEvent) isAgentEvent()     {}
func (ContextUsageEvent) EventName() string { return "context_usage" }
```

**emit 点**（`dispatcher.go` Run 循环）：在 `RunEndEvent` 之后、队列检查之前调用 `a.emitContextUsage()`：

```go
func (a *Agent) emitContextUsage() {
	used := a.LastUsage().PromptTokens
	if used <= 0 {
		return
	}
	ctx := 0
	if d, ok := llm.DefaultModelRegistry.Get(a.ModelName()); ok {
		ctx = d.ContextWindow
	}
	if ctx <= 0 {
		return
	}
	a.bus.Emit(event.ContextUsageEvent{
		EventBase:    event.EventBase{EventCommon: event.EventCommon{SessionID: a.session.ID}},
		UsedTokens:   used,
		ContextTokens: ctx,
		Percent:      int(float64(used) / float64(ctx) * 100),
	})
}
```

> `used <= 0`（run 在首次 LLM 前就 error）跳过，避免 0% 假圆环。
>
> **落地补充（live 实测）**：当前部署走代理 base_url，anthropic `message_start` 不上报 `input_tokens` → `LastUsage().PromptTokens` 恒为 0。故 `used <= 0` 时退回 `ctxengine.EstimateMessageTokens` 对会话消息做 rune/4 本地估算（上下文占用近似值），估算仍为 0 才跳过。已知代价：代理不报 input_tokens 时圆环 percent 偏低（rune/4 低估长会话），有真实 usage 的直连环境仍走 API 上报值。

**ledger 序列化**（`eventledger.go mapEventToTS` 加 case）：

```go
case event.ContextUsageEvent:
	usage := map[string]any{
		"sessionId":     common.SessionID,
		"usedTokens":    e.UsedTokens,
		"contextTokens": e.ContextTokens,
		"percent":       e.Percent,
		"status":        "unknown", // renderer deriveContextStatus 按 percent 阈值派生
		"updatedAt":     time.Now().UnixMilli(),
	}
	return map[string]any{"type": "context_usage", "sessionId": common.SessionID, "usage": usage}
```

> renderer 端 `useMessages.appendEventFor` 已消费 `context_usage`（spec 03），`ContextUsageIndicator` 已按 `percent` 渲染 + `deriveContextStatus` 派生 5 态——**渲染层零改动**。

### 4.4 context 行工作目录可点击

`ComposerContextRow.vue`：左 chip 由 `<span>` 改为 `<button>`，`@click="window.darvin.revealWorkspace()"`，样式 `cursor-pointer hover:bg-surface-2 hover:text-text`，`:title`/`:aria-label` 用 `t('imported.reveal')`（「在文件管理器中打开」）。右侧 Agent 选择器保持只读。

## 5. 用户场景

### 场景 1：切换会话看到各自的文件
**Given** 会话 A 导入了 `a.csv`
**When** 用户点侧栏切到会话 B
**Then** ImportedFilesBar 清空并显示会话 B 的文件（B 无导入则为空文案）；context 行工作目录变为 B 的 basename；切回 A 恢复显示 `a.csv`

### 场景 2：对话后看到上下文用量
**When** 用户在任意会话发一条消息并等回复完成
**Then** composer 工具栏右侧出现圆环，显示 percent + tooltip「已用 X / 上下文 Y」（`y` 来自模型 `ContextWindow`）

### 场景 3：打开会话工作目录
**When** 用户点 context 行 `[📁 label ▾]`
**Then** 系统文件管理器打开该会话的 workspace 目录

## 6. 验收

- [ ] 切换会话后 `getWorkspaceInfo().label` + `listImportedFiles()` 与 active session 一致；ImportedFilesBar 只显示当前会话文件（会话过滤生效）
- [ ] 会话 A 导入文件，切到 B 不显示，切回 A 显示
- [ ] 完成一次 prompt 后圆环出现（percent / used / context tokens 正确，tooltip 正常）；再发一轮 usage 更新
- [ ] context 行工作目录点击打开系统文件管理器
- [ ] Go：`go build` / `go vet` / gateway+dispatcher 单测通过；`npm run lint` + `npm run test`（含新增 context_usage 序列化用例）通过
- [ ] **已知边界**：切换会话会重启子进程（~1s），中断其它会话在途流式；后台流式 session 的 running 指示可能残留到下轮事件（不阻塞本 spec）

## 7. 依赖

- **前置**：03（圆环组件 / `contextUsageBySessionId` 消费）、04（compaction 事件）、09（composer 工具栏 / context 行）
- **涉及 main / shared**：`src/main/index.ts`（`followActiveWorkspace` + 三个入口调用 + broadcast）
- **涉及 renderer**：`src/renderer/composables/useImportedFiles.ts`、`src/renderer/components/chat/ComposerContextRow.vue`
- **涉及 Go**：`src/darvin-agent/internal/agent/event/event.go`、`src/darvin-agent/internal/agent/dispatcher.go`、`src/darvin-agent/internal/gateway/eventledger.go`
- **不改**：`src/shared/darvin-api.ts`（`context_usage` 事件与 `DarvinContextUsage` 已存在，无需契约变更）
- **已知成本**：切换会话重启子进程。后续可做 Go `set_workspace` RPC（运行时重锚沙箱）消除重启，本 spec 不做。

## 8. 参考

- `src/main/index.ts`：`workspaceLoc`（:101）、`restartGoSubprocess`（:550）、`switch_session`（:252）、`create_session`（:220）、`delete_session`（:268）、`broadcastWorkspaceChanged`（:104）
- `src/renderer/composables/useImportedFiles.ts`：单例 ref + `onWorkspaceChanged` + `listImportedFiles`
- `src/renderer/components/chat/ComposerContextRow.vue`：工作目录 chip
- `src/darvin-agent/internal/agent/dispatcher.go`：Run 循环 / `RunEndEvent` emit 点
- `src/darvin-agent/internal/agent/agent.go`：`LastUsage` / `ModelName`
- `src/darvin-agent/internal/agent/llm/model_registry.go`：`DefaultModelRegistry.Get(id).ContextWindow`
- `src/darvin-agent/internal/gateway/eventledger.go`：`mapEventToTS`
