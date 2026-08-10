# MCP 完善设计文档（agent runtime + 页面）

## 1. 概述

### 1.1 问题 / 背景

darvin 的 MCP 模块（spec 34-37）已落地基础骨架：Go 端自建 JSON-RPC 客户端（stdio/http/sse 三 transport）+ Registry + npx resolver + 指纹缓存 + Notifier，main 端 SQLite 存 server 元数据，renderer 有 McpView 增删改查。但对照 **LobsterAI（openclaw）** 与 **DeepSeek-Reasonix** 的 MCP 实现，darvin 仍有明显缺口，分三类：

**A. 安全与健壮性缺口（风险最高，优先）**
1. **MCP 工具无权限门控**：`McpTool.Execute`（`src/darvin-agent/internal/tools/mcp.go:123`）直接 `CallTool`，不走 `EvaluatePermission`（`internal/tools/permission.go` 只处理 shell + 内置文件工具）。一个恶意/不可信的 MCP server 暴露的任何工具 agent 都能无确认调用。
2. **进程崩溃不自动恢复**：`connectServer`（`internal/mcp/registry_resolve.go:180`）里 reconnect factory 是 `return nil, errors.New("reconnect not implemented in v0")`。MCP 子进程崩了之后，工具调直到下一次 Register 才恢复。
3. **调用结果无资源预算**：`limitWriter`（`internal/tools/limitwriter.go`）只用于 shell 输出；MCP 的 `tools/call` 返回大 JSON / 大文本会整块进 agent 上下文。
4. **凭据可能泄露**：server 配置里的 env / header 含 API key，连接错误 / `LaunchResolution.Error` / UI 直接展示原文，日志也不脱敏。
5. **`mcp test` 只返回 ok/error**：没有「从错误文本反推认证缺口 → 引导 OAuth / 清凭据重试」的诊断分层（Reasonix `internal/mcpdiag` 的三态模型 none/possible/required）。

**B. 协议能力缺口**
6. **SSE / streamable HTTP 不完整**：`transport/http.go` 注释明示「SSE is accepted via the Accept header but v0 does not parse event frames」。服务端主动 notification（`tools/list_changed` / `resource_updated` / `log_message`）一律收不到；HTTP session 过期（401/410）不会自动重 initialize 重放。
7. **resources / prompts 完全未实现**：client 只有 `tools/list` + `tools/call`。Reasonix 支持异步拉取 prompts（斜杠命令 `/name`）与 resources（`@server:uri` 引用）。LobsterAI 虽未暴露，但 OpenClaw SDK 底层有。
8. **workspace roots 未注入**：`client.Initialize` 声明了 `capabilities.roots: {}` 空对象（`client.go:180`），但从未把 darvin 的 workspace 作为 root 暴露给 server——filesystem 类 MCP server 无法感知工作目录。
9. **uvx / go resolver 是 stub**（`launcher.go:196-199`），python 生态 MCP server 每次走 raw npx 兜底，慢且无缓存。

**C. 页面缺口**
10. **工具只有名字 chips**：`McpServerCard.vue` 只列 `exposedTools` 的名字，点不开 schema，看不到 readOnly/destructive 等安全标记。
11. **连接失败无详情**：`LaunchResolution` 有 `FailureStage / FailureStderr / FailureElapsed` 字段但 UI 不展示；错误只在 status badge 一行。
12. **无 resources / prompts 浏览、无服务器日志查看**。

### 1.2 目标

把 darvin 的 MCP 从「能连、能调工具」提升到「**安全、自愈、协议完整、UI 可诊断**」：MCP 工具纳入权限门控与资源预算；进程崩溃自动恢复；支持 streamable HTTP 完整帧解析、resources/prompts、workspace roots；页面能看工具 schema、失败详情与诊断引导。

### 1.3 非目标

- 不实现 server→client 的 **sampling / elicitation**（三方都未做，且属于高级能力，按 YAGNI 缓）。
- 不实现 **MCP marketplace**（`GitHubURL / RegistryID` 字段已预留但需要远程 registry 协议，单独 spec）。
- 不做 **per-project launch grant**（Reasonix 的 per-workspace 授权模型与 darvin 当前「全局启停 + 按工具权限」模型冲突，属于更大的授权架构调整，不在此 spec）。
- 不引入第三方 MCP SDK（darvin 坚持自建，与 Reasonix 一致；LobsterAI 依赖 OpenClaw 是另一条路线）。

---

## 2. 现状对比（参考调研结论）

### 2.1 LobsterAI（TS 壳 + OpenClaw runtime）可借鉴点

- npx 前置安装 + **源配置 sha256 指纹状态机**（pending/installing/ready/failed/unsupported + stale installing 自愈 150s + 可恢复错误自动重试）——darvin 已同源移植大半（`resolver_fingerprint.go`），但缺 stale 自愈的 UI 可见性。
- Windows 进程树 kill + `windowsHide` init 脚本——darvin 跨平台性可借鉴。
- header key 强制小写防 WAF 重复头。
- **ask-user HTTP bridge**（服务端经 HTTP 回调 Electron 弹窗确认）——与 darvin 的 `RequestPermission` 机制同思路，darvin 已有 permission_request 事件通道，无需另起炉灶。

### 2.2 Reasonix（纯 Go 自建）可借鉴点

- **三层启动模型 + 缓存占位 PINNED**（`internal/plugin/lazy.go`）：eager 同步 / background 异步 / lazy 进程空闲。缓存命中时不 spawn 进程，占位工具带缓存 schema 进注册表且整个 session 不换（保住 provider 工具数组字节稳定）。**darvin 目前 tools/list 是启动时一次性拉取，无缓存命中空转**。
- **readOnlyHint / destructiveHint → Plan / read-only 过滤 + 执行前缓存-实时安全一致性校验**（TOCTOU 防护）。MCP 工具 schema 的 `annotations` 字段（2025-03-26 协议修订加入）正是为此设计。
- **工具结果资源预算**：stderr tail 16KB、失败摘要 500 字符、图片 4MiB×5、HTTP body 16MiB。
- **凭据脱敏的身份归一化**：`__redacted__` 替换 API key/token 值，API key 轮换不使授权/缓存失效。
- **mcpdiag 认证诊断状态机**：从错误文本 + 传输类型 + 凭据配置反推 OAuth 缺口。
- **HTTP session 自愈**：过期自动重 initialize 并重放调用。
- **工具名规范化 + collision hash**：非法字符 → `_` + fnv32a 6 位哈希防跨服务器同名冲突。

---

## 3. 用户场景

### 场景 1: 不可信 MCP server 的危险工具被拦
**Given** 用户添加了一个 community MCP server，其中一个工具 `deleteAllData` 带 `destructiveHint` 注解
**When** agent 在对话中调用 `mcp__community__deleteAllData`
**Then** executor 弹 permission_request 确认（复用现有 shell 危险命令的确认 UI），用户拒绝则该调用返回「permission denied」，不触达 server

### 场景 2: MCP 子进程崩溃后自动恢复
**Given** filesystem MCP server 在运行中被系统杀掉
**When** 下一次 agent 调用 `mcp__filesystem__list_directory`
**Then** McpTool 发现 client 不可用 → 触发 reconnect（registry 重建 transport + 重握手 + 重拉 tools/list）→ 调用成功；`connectionStatus` 经 notification 推回 UI 恢复 connected

### 场景 3: 超大工具结果不撑爆上下文
**Given** 某 MCP server 的 `search` 返回 10MB JSON
**When** agent 调用它
**Then** 结果被 limitWriter 截断到预算上限（如 256KB）+ 附「已截断」标记，agent 上下文只进预算内的部分

### 场景 4: 认证失败可诊断
**Given** 用户配置了一个需要 OAuth 的 streamable HTTP MCP server
**When** 点「测试连接」失败（401 unauthorized）
**Then** 卡片显示诊断：`认证失败：服务器需要 OAuth 授权` + 引导按钮（打开授权 / 清除凭据后重试），而不是一行裸错误

### 场景 5: 查看工具 schema 与安全标记
**Given** McpView 里某 server 已连接
**When** 用户点卡片上的工具名
**Then** 展开 schema（JSON 视图）+ readOnly / destructive / openWorld 标记徽章

### 场景 6: 浏览 server 的 resources / prompts
**Given** 某 MCP server 声明了 resources / prompts 能力
**When** 用户查看该 server 详情
**Then** 能看到资源列表（URI + mimeType）与提示模板列表（name + description），可一键触发 prompt 插入聊天

### 场景 7: 慢启动 server 不阻塞应用启动
**Given** 用户配置了一个 npm 包 MCP server（缓存命中）
**When** 应用启动
**Then** 不 spawn 进程，agent 工具面注册占位工具（带缓存 schema）；首次真实调用才连；session 内占位不替换

---

## 4. 功能需求

> 分三个批次（Phase A 安全/健壮性 → Phase B 协议 → Phase C 页面）。每个批次独立可验收，可单独裁剪。实现顺序按依赖：A 是 B/C 的安全底座，B 的 resources/prompts 是 C 页面的数据源。

### Phase A — 安全与健壮性（Runtime 底座）

**FR-A1: MCP 工具权限门控**
- `McpTool` 增加安全分类：解析 MCP schema 的 `annotations.readOnlyHint / destructiveHint / openWorldHint`（2025-03-26 协议字段，兼容 2024-11-05——server 不声明则按「未知 = 需谨慎」处理或按 server 来源信任级别）。
- `Registry.EvaluatePermission` 扩展：对 KindMcp 工具，`destructiveHint=true` 或无 readOnlyHint 的调用返回 `Need=true` 弹确认；`readOnlyHint=true` 直接放行。复用现有 `RequestPermission` 通道（executor.go:432）。
- server 级信任开关：`ServerSpec` 加 `trustLevel: 'trusted' | 'ask'`（默认 ask），trusted 放行、ask 全确认——给用户「这个 server 我可信」的控制面。

**FR-A2: MCP 调用结果资源预算**
- `McpTool.Execute` 结果用 limitWriter 截断（预算常量，建议 256KB），截断时 result 附加截断标记与总字节数。
- 多 content 块拼接时累计预算（Reasonix `parseToolResult` 同思路）。

**FR-A3: 凭据脱敏**
- 新增 `internal/mcp/redact.go`：对 `ServerSpec.Env / Headers / URL` 里 key 命中 `token/secret/api_key/bearer/authorization/password` 的值替换为 `***`。
- 应用于：`LaunchResolution.Error / FailureStderr`、`ConnectionError`、gateway wire、main 端 `launchError`、renderer 展示。

**FR-A4: 进程崩溃自愈 + 自动重连**
- 实现 `Client.WithReconnectFactory` 的真实 factory（`registry_resolve.go:180` 换掉 stub）：按 `spec + fingerprint` 重建 transport → 重握手 → 重拉 tools/list。
- `McpTool.Execute` / `Registry.CallTool` 发现 transport 断开时触发一次重连重试（reasonix `callTransport` 重放思路）。
- 崩溃检测：stdio transport 的 waitForExit 已存在（`transport/stdio.go:117`），在 exit 时广播 `ConnectionDisconnected` + 标记 stale，下一次调用惰性重建。

**FR-A5: 连接诊断（mcpdiag 风格）**
- 新增 `internal/mcp/diagnose.go`：`DiagnoseAuth(transportType, connected, errText, url, hasCreds) → none|possible|required` + 可操作建议文案。
- `handleMcpTest` 返回诊断字段（`authStatus` + `suggestion`）；`McpServerCard` 展示诊断徽章 + 引导按钮。

### Phase B — 协议能力扩展

**FR-B1: streamable HTTP / SSE 完整帧解析**
- `transport/http.go`：解析 `text/event-stream` 响应的 SSE 帧（`event: message` / `data:`），正确路由 response / notification；实现 session 过期（401/410）自动重 initialize + 重放当前调用。
- `transport/sse.go`：补齐长连 GET + `endpoint` 事件 → POST 端点 + pending map 关联 + 有界回复队列（Reasonix `sseReplyQueueBound=16` 防洪泛）。

**FR-B2: 服务端 notifications 处理**
- client 层收 `notifications/tools/list_changed` → 通知 registry 重拉 tools/list → 触发 `SessionManager.RefreshAllTools`（复用 `refreshToolsIfNeeded`）。
- 收 `notifications/log_message` → 打到 agent 日志 / UI 服务器日志（Phase C 数据源）。

**FR-B3: workspace roots 注入**
- `client.Initialize` 的 `capabilities.roots` 填真实 `roots[{uri, name}]`（workspace 绝对路径 `file://`）；处理 server 的 `roots/list` 反向请求（Reasonix `protocol_inbound.go` 模式）。

**FR-B4: resources / prompts 支持**
- client 新增 `ListResources / ReadResource / ListPrompts / GetPrompt`。
- registry `ServerStatus` 加 `Resources / Prompts` 字段（连接后异步拉取，失败 warn 不阻断，Reasonix StartPhaseB 模式）。
- gateway 新增 RPC：`agent.mcp.resources.list / resource.read / prompts.list / prompt.get`。
- **prompt 触发集成**：prompt 可作为「半成品用户消息」注入聊天（Reasonix `/name` 斜杠命令思路），或用一次性参数填充后直接发给 agent。

**FR-B5: uvx resolver**
- `launcher.go` 把 `uvx` 从 stub 升级为完整 resolver：`uvx` 装到 `<rootDir>/<serverID>-<pkg>`，复用 npxResolver 的 state machine 模式（view/install/bin 解析），失败回退 raw command。

### Phase C — 页面完善

**FR-C1: 工具 schema / 安全标记展示**
- `McpServerCard` 的工具 chips 可点击展开 schema（只读 JSON 视图）+ readOnly/destructive/openWorld 徽章（数据来自 Phase A/B 的 annotation 解析）。

**FR-C2: 连接失败详情**
- `McpServerCard` 失败态展示 `FailureStage / FailureElapsed / FailureStderr`（截断）+ 诊断建议（FR-A5）+ 「清除凭据重试」按钮（有凭据时）。

**FR-C3: resources / prompts 浏览**
- `McpView` 或卡片详情加 resources / prompts 区块：资源列表（URI + mimeType，可复制）、prompt 列表（name + description，点击 → 填充到 Composer）。

**FR-C4: 服务器运行日志查看（可选）**
- 收 `log_message`（FR-B2）+ stderr tail（16KB 环形缓冲）→ 卡片内日志抽屉。

---

## 5. 实现方案

### 5.1 Phase A 关键设计

**权限门控数据流**（对齐现有 executor 门控）：
```
McpPlugin.Register → McpTool{annotations: 解析自 ToolDescriptor.InputSchema.annotations}
executor.executeOneTool(c.Name) → d.EvaluatePermission(name, args)
  └─ Registry.EvaluatePermission 扩展分支：
      kind==mcp && server.trustLevel==ask:
          destructiveHint || !readOnlyHint → Need=true, Level=caution/destructive
      kind==mcp && server.trustLevel==trusted → safe
  └─ eval.Need → d.RequestPermission(...)（复用现有 permission_request 事件）
```

- `ToolDescriptor` 加 `Annotations` 字段（`internal/mcp/tool.go`）：`{readOnlyHint, destructiveHint, openWorldHint *bool}`，`tools/list` 反序列化时容错缺失。
- `ServerSpec` 加 `TrustLevel string`（`trusted|ask`，默认 `ask`），fingerprint 不包含 trustLevel（权限策略变更不触发重装）。
- 常量：`mcpResultCap = 256 << 10`（256KB，对齐现有 shell 输出风格）。

**重连实现**：
- `connectServer` 的 factory 改为闭包：捕获 `spec + fingerprint`，`resolveOrRaw` 重建 transport，复用 `connectServer` 的 Step 2/3 逻辑（提取 `openClientTransport(spec, res)` 纯函数）。
- `Registry.CallTool` 加一次重连重试：`if err == transport closed && attempt==0 → reconnect + retry`。

**诊断实现**：
```go
type AuthDiagnosis struct {
  Status string // "none" | "possible" | "required"
  Suggestion string
}
func DiagnoseAuth(t transport.TransportType, connected bool, errText string,
                  url string, hasExplicitCreds bool) AuthDiagnosis
```
- 规则（移植 Reasonix `mcpdiag/auth.go`）：401/403/unauthorized/forbidden/login required → `required`（且 streamable-http + https/loopback + 无显式凭据才 eligible，否则 `none` + 建议手动配凭据）；未失败但 connecting/disconnected → `possible`。

### 5.2 Phase B 关键设计

**SSE 帧解析**（`transport/http.go`）：Recv 时按 `Content-Type` 分派——`application/json` 直接返回；`text/event-stream` 逐 event 缓冲，`event: message` + `data:` 组装 JSON 帧，非 message 事件（notification）放入 pending notification 通道由 client 层消费。session 过期判定在 `http.go` 读 `Mcp-Session-Id` header + 401/410。

**roots 注入**：`Initialize` 参数改为 `roots: {listChanged: false}`，client 持有 `workspaceRoot`（runtime 注入），同时实现 `roots/list` 反向 handler（`protocol_inbound.go` 模式：reader 循环里遇到 id + method 的反向请求，走 replyLoop 回写）。

**resources/prompts 拉取时机**：`connectServer` 握手成功后，对声明能力（`InitializeResult.Capabilities`）的 server 异步拉 resources/prompts（带 10s timeout，失败仅 warn），结果存 `serverEntry.status.Resources/Prompts` 并广播 `McpServersChanged`。

### 5.3 Phase C 关键设计

- 复用现有 `mcp.connection_changed` / `mcp.resolution_changed` 事件流；`ServerStatus` wire 加 `resources / prompts / authDiagnosis` 字段（`handler_mcp.go` `McpServerWire` 扩展）。
- schema 查看：`McpServerCard` 内 `<details>` 展开 + `JSON.stringify(schema, null, 2)` 只读 pre（走 `@theme` token，不引第三方 JSON 渲染库）。
- prompt 触发：点 prompt → 复制 name/description 到 Composer 或直接走 `chat:send` 派发。

---

## 6. 边界情况

| 场景 | 处理方式 |
|------|---------|
| MCP server 不声明 annotations（2024-11-05 老协议） | 按 `trustLevel=ask` 全确认兜底；声明 `readOnlyHint` 才放行 |
| readOnlyHint 与 destructiveHint 同时 true | destructive 优先（Reasonix 同规则） |
| 重连后 tools/list 变化 | 工具面全量刷新（RefreshAllTools），与新工具列表对齐 |
| 超大 MCP 结果 | limitWriter 截断 + 标记；不阻塞、不 OOM |
| 凭据在 env 且被复制进 args | 脱敏覆盖 env/headers/url/args 中的 secret-ish 值 |
| HTTP session 过期时调用重放 | 重 initialize 后重放一次；若再次失败返回错误不无限重试 |
| 慢启动 npm server | lazy 占位（FR-B 可选扩展），首次调用才连，缓存命中不 spawn |
| macOS / Windows 平台差异 | reconnect 复用现有 transport 构建逻辑，不新增平台分支 |
| 诊断对 stdio server 的 OAuth | 不判 required（stdio 无原生 OAuth，给「手动配置凭据」建议） |

---

## 7. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/darvin-agent/internal/mcp/tool.go` | ToolDescriptor + Annotations 字段 + 反序列化容错 |
| `src/darvin-agent/internal/mcp/server_spec.go` | ServerSpec + TrustLevel 字段 |
| `src/darvin-agent/internal/tools/mcp.go` | McpTool 解析 annotations / 结果预算 / 重连重试 |
| `src/darvin-agent/internal/tools/permission.go` | EvaluatePermission 加 KindMcp 分支 |
| `src/darvin-agent/internal/mcp/registry_resolve.go` | 真实 reconnect factory + openClientTransport 提取 |
| `src/darvin-agent/internal/mcp/client.go` | ListResources/ReadResource/ListPrompts/GetPrompt + roots 注入 |
| `src/darvin-agent/internal/mcp/redact.go` | 新增：凭据脱敏 |
| `src/darvin-agent/internal/mcp/diagnose.go` | 新增：认证诊断 |
| `src/darvin-agent/internal/mcp/transport/http.go` | SSE 帧解析 + session 自愈 |
| `src/darvin-agent/internal/mcp/transport/sse.go` | 补齐长连 + endpoint + 有界队列 |
| `src/darvin-agent/internal/mcp/launcher.go` | uvx resolver 完整化 |
| `src/darvin-agent/internal/mcp/registry.go` | ServerStatus + Resources/Prompts + notifications 处理 |
| `src/darvin-agent/internal/gateway/handler_mcp.go` | McpServerWire 扩展 + resources/prompts/diagnose RPC |
| `src/darvin-agent/internal/agents/ctxengine/assembler.go` | （可选）MCP annotations 进系统提示 |
| `src/shared/darvin-api.ts` | McpServer + ExposedTool + 诊断/资源/prompt 类型 |
| `src/renderer/components/mcp/McpServerCard.vue` | schema 展开 + 安全徽章 + 失败详情 + 诊断引导 |
| `src/renderer/components/mcp/McpServerFormModal.vue` | trustLevel 字段 |
| `src/renderer/views/McpView.vue` | resources/prompts 区块（按需） |
| `src/renderer/services/i18n.ts` | mcp 新增 key（zh/en 对齐） |

## 8. 验收标准

- [ ] Phase A: 不可信 server 的 destructive 工具弹 permission_request；拒绝后不触达 server（executor + permission 单测）
- [ ] Phase A: MCP 子进程 kill 后下一次调用自动重连成功，UI 恢复 connected（单测 + CDP 手动）
- [ ] Phase A: 10MB 工具结果被截断到预算且带标记（单测）
- [ ] Phase A: env/header 里 `Bearer xxxx` 在错误/UI 显示为 `***`（单测）
- [ ] Phase A: 401 测试连接返回诊断 `required` + 建议文案（单测）
- [ ] Phase B: SSE 帧解析、session 过期重放（transport 单测）
- [ ] Phase B: `tools/list_changed` notification → 工具面自动刷新（registry 单测）
- [ ] Phase B: `roots/list` 反向请求返回 workspace URI（client 单测）
- [ ] Phase B: resources/prompts 拉取 + `agent.mcp.*` 新 RPC（单测 + CDP）
- [ ] Phase B: `uvx` resolver 完整走通（launcher 单测）
- [ ] Phase C: 卡片工具名可展开 schema + 安全徽章（CDP）
- [ ] Phase C: 失败详情（stage/stderr/耗时）+ 诊断引导 + 清凭据重试（CDP）
- [ ] Phase C: resources / prompts 区块展示（CDP）
- [ ] 全量：`npm run lint` + `npm run test` + `cd src/darvin-agent && go build/vet/test ./...` 全绿
- [ ] i18n zh/en key 对齐（assertSameKeys）

---

## 附：实现批次建议

- **第一批（建议先做，1 个 PR 内）**：FR-A1 权限门控 + FR-A2 结果预算 + FR-A3 凭据脱敏 —— 三者都在 tools/mcp.go + permission.go 一条链路，改动小、安全价值最高。
- **第二批**：FR-A4 自动重连 + FR-A5 诊断 + Phase C 的 FR-C1/FR-C2（失败详情）——运行时自愈 + UI 可诊断。
- **第三批**：Phase B 协议（SSE/roots/resources/prompts/uvx）。
- **第四批**：Phase C 剩余（resources/prompts 浏览、日志抽屉）。

若范围需要裁剪，第一批 + 第二批是「安全 + 自愈 + 可诊断」的最小闭环，建议优先。

---

## 落地状态（2026-08-10 全部批次完成）

**Phase A（安全 / 健壮性）**：✅
- FR-A1 权限门控：`ToolDescriptor.Annotations`（readOnly/destructive/openWorld hint）+ `ServerSpec.TrustLevel`（trusted/ask，built-in 默认 trusted）+ `McpTool.ClassifyDanger` + `EvaluatePermission` DangerClassifier 分支；TS 侧 trustLevel 走通 wire/store/FormModal。
- FR-A2 结果预算：`McpTool.Execute` limitWriter 256KB + 截断标记。
- FR-A3 凭据脱敏：`internal/mcp/redact.go`（RedactString/RedactMap/RedactResolution），gateway wire 边界应用。
- FR-A4 自动重连：`Registry.CallTool` 连接错误 → 同步 `connectServer` 重建 → 重试一次；修复 `isConnectionError` 识别 `transport.ErrTransportClosed`。
- FR-A5 认证诊断：`internal/mcp/diagnose.go`（none/possible/required）+ `handleMcpTest` 返回 authStatus/authSuggestion。

**Phase B（协议）**：✅
- FR-B1 streamable HTTP：HTTPTransport 解析 SSE `message` event；401/410 → `ErrSessionExpired`；client 重握手重放。
- FR-B2 notifications：stdio inbound channel → client Notifications() → registry 消费 `tools/list_changed` → re-list + `OnToolsChanged` → refreshToolsIfNeeded。
- FR-B3 roots：`Client.SetRoots` + `roots/list` 反向请求回复；runtime `setWorkspace` 注入 `file://<root>`。
- FR-B4 resources/prompts：client 4 方法 + registry 异步拉取缓存 + 4 个 gateway RPC + TS 全链路（client/preload/main/useMcpServers）。
- FR-B5 uvx resolver：`uv tool install --prefix` 预装 + bin 解析，失败回退 raw。

**Phase C（页面）**：✅
- FR-C1 工具 schema 展开 + R/D 安全徽章（annotation 上 wire）。
- FR-C2 失败详情：failureStage/failureElapsedMs/failureStderr 上 wire + McpServerCard 详情块 + 诊断。
- FR-C3 resources/prompts 懒加载浏览区块。
- FR-C4 运行日志抽屉：stdio stderr 200 行环形缓冲 + `agent.mcp.logs.get` + 卡片日志 toggle。

**验证**：`go build/vet/test ./...` 22 包全绿（新增 redact 6 + annotation/trust round-trip + ClassifyDanger 4 + truncation + trust wiring + reconnect 2 + diagnose 9 + SSE 3 + inbound 3 + uvx 2）；vitest 247 全绿；lint 干净；`npm run build:agent` 成功。
