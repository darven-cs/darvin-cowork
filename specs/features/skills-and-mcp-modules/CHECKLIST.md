# Skills + MCP 模块 跟踪表

> **中心化跟踪表**。每份子 spec 的 §7 验收是「设计层」细颗粒清单；本表是「执行层」一栏一格，落地时只勾这里。
>
> 创建日期：2026-08-02
> 调研 / 索引 doc：[`2026-08-02-skills-and-mcp-modules-design.md`](./2026-08-02-skills-and-mcp-modules-design.md)
> 进度规则：子 spec 的核心 FR 全部勾上 → 标 `✅ 完成`；部分落地 → `🚧 进行中`；碰到阻塞 → `⛔ 阻塞`（要在后面写原因）

---

## 当前进度

**进行中 1 / 9。** spec 31 Go 端骨架已落地，待用户重启 Electron 验证启动日志，再继续 spec 32。

| # | spec | 状态 | 进度 | 关键路径 |
|---|------|------|------|---------|
| 31 | [skills-loader-and-registry](./2026-08-02-skills-loader-and-registry.md) | 🚧 进行中 | 14/14（待人工重启验证） | Go 端 skills 骨架 |
| 32 | [skills-ipc-and-bootstrap](./2026-08-02-skills-ipc-and-bootstrap.md) | ⏳ 待启动 | 0/9 | 31 完成后 |
| 33 | [skills-renderer-view](./2026-08-02-skills-renderer-view.md) | ⏳ 待启动 | 0/8 | 32 完成后 |
| 34 | [mcp-transport-and-client](./2026-08-02-mcp-transport-and-client.md) | ⏳ 待启动 | 0/12 | 并行 32 |
| 35 | [mcp-registry-and-launcher](./2026-08-02-mcp-registry-and-launcher.md) | ⏳ 待启动 | 0/13 | 34 完成后 |
| 36 | [mcp-main-store-and-ipc](./2026-08-02-mcp-main-store-and-ipc.md) | ⏳ 待启动 | 0/11 | 34 + 35 完成后 |
| 37 | [mcp-renderer-view](./2026-08-02-mcp-renderer-view.md) | ⏳ 待启动 | 0/8 | 36 完成后 |
| 38 | [tool-registry-merge-and-routing](./2026-08-02-tool-registry-merge-and-routing.md) | ⏳ 待启动 | 0/12 | 31 + 34 + 35 完成后 |
| 39 | [skill-user-invocation](./2026-08-02-skill-user-invocation.md) | ⏳ 待启动 | 0/8 | 32 + 38 完成后 |

**图例**：⏳ 待启动 / 🚧 进行中 / ✅ 完成 / ⛔ 阻塞

---

## 启动顺序

```
[26 tool-architecture-rework]  ← 必备前置
        │
        ├─────────────────┐
        ▼                 ▼
[31 skills-loader]    [34 mcp-transport]
        │                 │
        ▼                 ├─→ [35 mcp-registry-and-launcher]
[32 skills-ipc]                 │
        │                       │
        ▼                       ▼
[33 skills-renderer-view]  [36 mcp-main-store-and-ipc]
                                │
                                ▼
                          [37 mcp-renderer-view]

        [31 + 34 + 35] ──→ [38 tool-registry-merge-and-routing]

                  [32 + 38] ──→ [39 skill-user-invocation]
```

---

## 各 spec 核心 FR（执行层）

### 31 · skills-loader-and-registry

> Go 端 skills 骨架——loader / registry / scanner / runner。

- [x] `internal/skills/{loader,registry,scanner,runner,frontmatter,types,bootstrap}.go` 落地
- [x] `frontmatter.go` ParseFrontmatter（name / description / version / invocation）
- [x] `loader.go` 4 类 SkillSourceLoader（bundled + user；v0 不实现 github / npm）
- [x] `registry.go` SkillRegistry（byID + byPath + List/ListEnabled + SetEnabled）
- [x] `scanner.go` 4 类规则（.go / .py / .sh / .js）+ 5 等级 + 5s timeout
- [x] `runner.go` SkillRunner（ExecuteByID + ExecuteByUserInvocation）
- [x] `cmd/app/main.go` 注入 SkillRegistry + SkillRunner
- [x] bundled 5 个 SKILL.md（code-review / api-design / testing / web-search / docx）
- [x] `loader_test.go` 5 用例（bundled 5 / user 加载 / 缺目录 / risk 填充）
- [x] `registry_test.go` 5 用例（Load / Get / user 覆盖 / SetEnabled / Snapshot / ListBySource）
- [x] `scanner_test.go` 7 用例（safe / .go exec / .py subprocess / .sh curl|sh critical / .js eval / 大文件跳过 / 空目录）
- [x] `frontmatter_test.go` 3 用例（正常 / 缺 name 或 description / 未知字段）
- [x] `runner_test.go` 5 用例（命中 / 不存在 / disabled / user invocation 允许 / 拒绝）
- [x] 状态：🚧 进行中（待人工重启 Electron 验证启动日志）

### 32 · skills-ipc-and-bootstrap

> Go ↔ Main IPC + main 端 skillsManager + bundled 5 skill。

- [ ] `src/main/libs/skillManager.ts` 落地（bootstrap + setEnabled + reloadFromDisk + watcher）
- [ ] SQLite 表 `skill_state` 创建
- [ ] chokidar fs watcher（userData/SKILLs/**/SKILL.md）
- [ ] IPC channels `skills:list` / `skills:set_enabled`
- [ ] preload `window.darvin.skills.{list, setEnabled, onChanged}`
- [ ] AgentClient `skills.{list, setEnabled, bootstrap, onChanged}`
- [ ] Go 端 handler `agent.skills.{list, set_enabled, bootstrap}` + notification `agent.skills.changed`
- [ ] `skillManager.test.ts` 7 用例
- [ ] 状态：⏳ 待启动

### 33 · skills-renderer-view

> renderer UI——SkillsView + 4 子组件 + i18n 30+ key。

- [ ] `SkillsView.vue` 三 tab（已安装 / 市场 / 设置）
- [ ] `useSkills.ts` composable（refresh + setEnabled 乐观更新 + 订阅 onChanged）
- [ ] `SkillCard.vue`（name / description / version / switch / 风险徽章）
- [ ] `SkillMarketplace.vue`（本地文件选择器 + GitHub URL）
- [ ] `SkillSecurityReportModal.vue`（risk level + score + findings 列表）
- [ ] `SkillSettingsPanel.vue`（bundled skill 列表）
- [ ] `SkillDetailsModal.vue`（详情 + 升级 / 卸载按钮）
- [ ] i18n +30 key（zh + en 对齐）
- [ ] 移除 `AppShell.vue` 的 skills PlaceholderView 路由
- [ ] `useSkills.test.ts` + `SkillCard.test.ts`
- [ ] live 验证：装 / 卸 / 启停 / 升级 / 安全报告 modal
- [ ] 状态：⏳ 待启动

### 34 · mcp-transport-and-client

> Go 端 stdio + http transport + JSON-RPC 2.0 client。

- [ ] `internal/mcp/transport/transport.go` Transport interface + Frame + ErrTransportClosed
- [ ] `internal/mcp/transport/stdio.go` StdioTransport（spawn + Content-Length frame + SIGTERM/KILL）
- [ ] `internal/mcp/transport/http.go` HTTPTransport（POST + headers + Mcp-Session-Id）
- [ ] `internal/mcp/client.go` JSON-RPC Client（Call + Initialize + ListTools + CallTool）
- [ ] `internal/mcp/types.go` Request / Response / RPCError / InitializeResult / ToolDescriptor / CallToolResult
- [ ] `internal/mcp/client.go` CallWithRetry（指数退避最多 3 次）
- [ ] `stdio_test.go` 6 用例（Connect / Send+Recv frame / 子进程崩溃 / Close）
- [ ] `http_test.go` 5 用例（httptest mock / 200 OK / 500 错 / timeout）
- [ ] `client_test.go` 6 用例（Initialize / ListTools / CallTool / RPC error / 序列化）
- [ ] `reconnect_test.go` 3 用例（断开 → 重连 / RPC error 不重试 / 3 次上限）
- [ ] cmd `main.go` import mcp（占位）
- [ ] 状态：⏳ 待启动

### 35 · mcp-registry-and-launcher

> Go 端 McpRegistry + ResolverManager（4 类 resolver）+ npx 优化。

- [ ] `internal/mcp/types.go` +ServerSpec / TransportType / ResolverKind / ResolutionStatus / LaunchResolution / ServerStatus
- [ ] `internal/mcp/registry.go` McpRegistry（Register / Unregister / SetEnabled / List / Get / GetTools / GetToolsByName）
- [ ] `internal/mcp/launcher.go` ResolverManager + npxResolver（完整）+ uvx / go / raw（stub）
- [ ] `internal/mcp/resolver_fingerprint.go` ComputeFingerprint（sha256(command|args|env|platform|arch)）
- [ ] `internal/mcp/persistence.go` ResolutionPersistence interface + InMemory impl
- [ ] 陈旧 installing 检测（30min 自动重试）
- [ ] Registry 与 Client 集成（fallback 到原始 command if resolver failed）
- [ ] `registry_test.go` 6 用例
- [ ] `launcher_test.go` 7 用例（npx 成功 / 失败 / scoped / fallback / stale installing）
- [ ] `resolver_fingerprint_test.go` 4 用例
- [ ] `persistence_test.go` 3 用例
- [ ] cmd 注入 ResolverManager + Registry
- [ ] 状态：⏳ 待启动

### 36 · mcp-main-store-and-ipc

> main 端 mcpManager + SQLite store + IPC + bundled filesystem MCP。

- [ ] SQLite 表 `mcp_servers` + `mcp_launch_resolutions` + cascade delete
- [ ] `src/main/libs/mcpStore.ts`（create / get / list / update / delete / saveResolution / loadResolutions）
- [ ] `src/main/libs/mcpManager.ts`（bootstrap + createServer + updateServer + deleteServer + setEnabled + testConnection + retryResolution）
- [ ] IPC channels `mcp:list` / `mcp:create` / `mcp:update` / `mcp:delete` / `mcp:set_enabled` / `mcp:test` / `mcp:retry_resolution`
- [ ] IPC push `mcp:servers_changed` + `mcp:connection_changed`
- [ ] preload `window.darvin.mcp.*` 9 个方法
- [ ] AgentClient `mcp.*` 9 个方法
- [ ] Go 端 handler `agent.mcp.{list, register, update, unregister, set_enabled, test, retry_resolution, bootstrap}` + notification `mcp.connection_changed` + `mcp.resolution_changed`
- [ ] bundled filesystem MCP（Go 写，darvin-agent `mcp-filesystem` subcommand）
- [ ] `mcpStore.test.ts` + `mcpManager.test.ts`
- [ ] live 验证：bundled filesystem 自动注册 + connected + 4 tools
- [ ] 状态：⏳ 待启动

### 37 · mcp-renderer-view

> renderer UI——McpView + 3 子组件 + i18n 35+ key。

- [ ] `McpView.vue`（list + [+ 新增] + 空态）
- [ ] `useMcpServers.ts` composable（refresh / create / update / remove / setEnabled / test / retryResolution）
- [ ] `McpServerCard.vue`（name / description / transport / switch / connection / launch 状态 / tools 列表 / 4 按钮）
- [ ] `McpConnectionStatus.vue`（4 状态 + 颜色 + dot 动画 + tooltip）
- [ ] `McpLaunchStatus.vue`（5 状态 + 颜色）
- [ ] `McpServerFormModal.vue`（按 transportType 切字段：stdio 命令+args+env / http url+headers）
- [ ] i18n +35 key（zh + en 对齐）
- [ ] 移除 `AppShell.vue` 的 mcp PlaceholderView 路由
- [ ] `useMcpServers.test.ts` + `McpServerCard.test.ts` + `McpServerFormModal.test.ts`
- [ ] live 验证：装 / 卸 / 启停 / 测试 / 重试 launch
- [ ] 状态：⏳ 待启动

### 38 · tool-registry-merge-and-routing

> 把 skill / mcp 工具合并进 tool.Registry；改 tool_start / tool_end 事件。

- [ ] `internal/agent/tool/tool.go` +Kind / ToolSchema / ToolResult / ToolContentBlock / Plugin / ToolRegistrar
- [ ] `internal/agent/tool/registry.go` 改造（加 Kind + Metadata）
- [ ] `internal/agent/tool/skill.go` SkillPlugin + SkillTool
- [ ] `internal/agent/tool/mcp.go` McpPlugin + McpTool
- [ ] `internal/agent/executor/executor.go` 改造 dispatchToolCall（按 Kind 路由 + emit 事件）
- [ ] `internal/agent/event/event.go` ToolStartEvent / ToolEndEvent 加 kind / skillID / mcpServerID
- [ ] RPC `agent.tools.list` 合并返回内置 + skill + mcp
- [ ] cmd 注入 SkillPlugin + McpPlugin + 订阅变化重新注册
- [ ] `registry_test.go` + `skill_test.go` + `mcp_test.go` + `executor_routing_test.go`
- [ ] `src/shared/darvin-api.ts` tool_start / tool_end 加可选 toolKind / skillId / mcpServerId 字段
- [ ] live 验证：合并 tool list 18 项 + 实际调用 skill / mcp
- [ ] 状态：⏳ 待启动

### 39 · skill-user-invocation

> chat `/skill-name args` 显式触发 skill。

- [ ] `Composer.vue` 改造：自动补全浮层（`/` 触发 / 过滤 / 选中 / Escape 关闭）
- [ ] `useSkills.ts` 改造：暴露 `userInvocable` 字段
- [ ] `src/main/index.ts` `chat:send` handler 检测 `/` 前缀 + 解析 skillId/args
- [ ] `//` 转义保留 `/` 文本
- [ ] `agentClient.invokeSkill({ sessionId, skillId, args })`
- [ ] Go 端 handler `agent.skill.invoke_user`
- [ ] `Agent.RunSkillSession` mini agent loop
- [ ] i18n +10 key
- [ ] live 验证：`/code-review src/api/handler.go` 触发 + 错误处理
- [ ] 状态：⏳ 待启动

---

## 主 spec 验收（全部 9 份子 spec 落地后）

- [ ] 侧栏 `技能` 跳 SkillsView，5 个 bundled skill 显示
- [ ] 装 / 卸 / 启停 / 升级 / 安全报告 modal 工作
- [ ] 侧栏 `MCP` 跳 McpView，bundled filesystem 已连接 + 4 tools
- [ ] 新增 stdio / http / 删除 / 启停 / 测试 / 重试 launch 工作
- [ ] agent 实际调用 skill / mcp 工具，renderer 按 `kind: 'skill' | 'mcp'` 渲染
- [ ] `/code-review src/api/handler.go` 触发 skill
- [ ] SQLite `mcp_servers` + `mcp_launch_resolutions` + `skill_state` 3 表齐全
- [ ] `npm run lint` + `npm run test` + `npm run build:agent` + `go test ./...` 全绿
- [ ] i18n 新增 110+ key，zh / en 双语齐全

---

## 状态变更日志

> 每完成一份子 spec，在此处记一行：日期 / spec / 「完成说明」。

- 2026-08-02 · 主 spec · 完成调研 + checklist + 9 份子 spec 拆分；待用户确认后启动 spec 31
- 2026-08-02 · spec 31 · Go 端 skills 骨架落地：types / frontmatter / loader(bundled+user) / registry / scanner / runner / bootstrap 全部实现并通过 `go test ./...`；bundled 5 个 SKILL.md 已 embed 到 `cmd/app/resources/skills-bundled/`；`cmd/app/main.go` 注入 SkillRegistry + SkillRunner。`go vet ./...` 干净，gofmt 在本批改动文件上干净，`npm run build:agent` 成功，`npm run lint` + `npm run test` 全绿。**待人工重启 Electron 验证启动日志**中出现 `skills: loaded ... bundled=5 user=0 total=5`。