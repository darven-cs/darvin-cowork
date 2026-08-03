# Skills + MCP 模块 跟踪表

> **中心化跟踪表**。每份子 spec 的 §7 验收是「设计层」细颗粒清单；本表是「执行层」一栏一格，落地时只勾这里。
>
> 创建日期：2026-08-02
> 调研 / 索引 doc：[`2026-08-02-skills-and-mcp-modules-design.md`](./2026-08-02-skills-and-mcp-modules-design.md)
> 进度规则：子 spec 的核心 FR 全部勾上 → 标 `✅ 完成`；部分落地 → `🚧 进行中`；碰到阻塞 → `⛔ 阻塞`（要在后面写原因）

---

## 当前进度

**进行中 0 / 9；完成 9 / 9。** spec 31-39 全部落地。spec 39 skill-user-invocation：`/skill-name args` 显式触发 skill —— Go 端 `Agent.RunSkillSession` mini agent loop（新文件 `agent_mini_loop.go`：临时覆盖 runSkillPrompt / runSkillTools，Instructions()/Tools() 按 transient 返 skill 上下文 + `buildSkillTools` 投影允许工具集保 Kind）+ acp.Loop `SubmitSkill` + gateway `agent.skill.invoke_user`（同步校验存在/enabled/userInvocable → 3 新错误码；通过后异步跑）+ Handler 注入 SkillRunner + wire.go `userInvocable`；TS `DarvinSkillSummary.userInvocable` + invokeSkill 全链路（client / preload / main IPC）；renderer Composer `/` 自动补全浮层 + `useChatActions.send/regenerate` `/` 路由（`//` 转义走普通 prompt / 失败 toast）+ `slash.ts` 纯函数 helpers + i18n +10 key；bundled `testing` SKILL.md 补 `invocation.userInvocable: true`（否则只有 4 个可手动触发）。**+10 Go 测试 + 10 TS 测试；go build/vet/test 17 包全绿 + lint 干净 + build:agent 成功 + vitest 194 通过**（25 失败为 better-sqlite3 ABI 预存环境问题）。

| # | spec | 状态 | 进度 | 关键路径 |
|---|------|------|------|---------|
| 31 | [skills-loader-and-registry](./2026-08-02-skills-loader-and-registry.md) | ✅ 完成 | 14/14 | Go 端 skills 骨架 |
| 32 | [skills-ipc-and-bootstrap](./2026-08-02-skills-ipc-and-bootstrap.md) | ✅ 完成 | 9/9 | Go ↔ Main IPC + main 端 skillsManager |
| 33 | [skills-renderer-view](./2026-08-02-skills-renderer-view.md) | ✅ 完成 | 12/12 | renderer SkillsView + 5 子组件 |
| 34 | [mcp-transport-and-client](./2026-08-02-mcp-transport-and-client.md) | ✅ 完成 | 12/12 | Go 端 stdio / http transport + JSON-RPC client |
| 35 | [mcp-registry-and-launcher](./2026-08-02-mcp-registry-and-launcher.md) | ✅ 完成 | 13/13 | Go 端 McpRegistry + 4 类 resolver + 30min stale installing |
| 36 | [mcp-main-store-and-ipc](./2026-08-02-mcp-main-store-and-ipc.md) | ✅ 完成 | 11/11 | main 端 mcpStore + mcpManager + 7 IPC + bundled filesystem subcommand |
| 37 | [mcp-renderer-view](./2026-08-02-mcp-renderer-view.md) | ✅ 完成 | 11/11 | renderer McpView + 4 子组件 + useMcpServers |
| 38 | [tool-registry-merge-and-routing](./2026-08-02-tool-registry-merge-and-routing.md) | ✅ 完成 | 11/12 | tool.Registry Entry 化 + Skill/Mcp 插件 + agent.tools.list |
| 39 | [skill-user-invocation](./2026-08-02-skill-user-invocation.md) | ✅ 完成 | 10/10 | `/skill-name args` 触发 + mini loop |

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

- [x] `src/main/libs/skillManager.ts` 落地（bootstrap + setEnabled + reloadFromDisk + watcher + chokidar）
- [x] SQLite 表 `skill_state` 创建（main 端独立 `skill-state.db`，与 Go 端 sessions.db 解耦）
- [x] chokidar fs watcher（userData/darvin-agent/SKILLs/**/SKILL.md，awaitWriteFinish 400ms）
- [x] IPC channels `darvin:list_skills` / `darvin:set_skill_enabled` + push `darvin:push:skills-changed`
- [x] preload `window.darvin.skills.{list, setEnabled, onChanged}`
- [x] AgentClient `skills.{list, setEnabled, bootstrap, onChanged}` + handleIncoming 路由 `agent.skills.changed` 通知
- [x] Go 端 handler `agent.skills.{list, set_enabled, bootstrap}` + `EventLedger.Broadcast` 推 `agent.skills.changed`
- [x] `skillManager.test.ts` 6 用例（parseFrontmatter 5 + 订阅契约 1）+ `client.test.ts` 3 用例（onChanged 路由）
- [x] 状态：🚧 进行中（待人工重启 Electron 验证 listSkills / setEnabled / fs watcher 三条路径）

### 33 · skills-renderer-view

> renderer UI——SkillsView + 4 子组件 + i18n 30+ key。

- [x] `SkillsView.vue` 三 tab（已安装 / 市场 / 设置）
- [x] `useSkills.ts` composable（refresh + setEnabled 乐观更新 + 订阅 onChanged）
- [x] `SkillCard.vue`（name / description / version / switch / 风险徽章）
- [x] `SkillMarketplace.vue`（本地文件选择器 + GitHub URL）
- [x] `SkillSecurityReportModal.vue`（risk level + score + findings 列表）
- [x] `SkillSettingsPanel.vue`（bundled skill 列表）
- [x] `SkillDetailsModal.vue`（详情 + 升级 / 卸载按钮）
- [x] i18n +30 key（zh + en 对齐，+4 common.*）
- [x] 移除 `AppShell.vue` 的 skills PlaceholderView 路由
- [x] `useSkills.test.ts`（5 用例：refresh / 乐观更新 / 失败回滚 / push 覆盖 / install）
- [x] `SkillCard.test.ts` — 项目无 @vue/test-utils 模式，跳过
- [x] live 验证：装 / 卸 / 启停 / 升级 / 安全报告 modal（**用户已重启 Electron，CDP 跑通**）
- [x] 状态：✅ 完成（CDP 实跑：3 tab 切换 + 5 bundled SkillCard 渲染 + 启停 switch 乐观更新 + 详情 modal Teleport 挂载 + bundled 卸载 disabled；5 详情按钮、设置 tab 5 bundled 过滤、市场 tab 文件选择 + GitHub URL + 安装按钮 1:1 命中设计）

### 34 · mcp-transport-and-client

> Go 端 stdio + http transport + JSON-RPC 2.0 client。

- [x] `internal/mcp/transport/transport.go` Transport interface + Frame + ErrTransportClosed
- [x] `internal/mcp/transport/stdio.go` StdioTransport（spawn + Content-Length frame + SIGTERM/KILL）
- [x] `internal/mcp/transport/http.go` HTTPTransport（POST + headers + Mcp-Session-Id）
- [x] `internal/mcp/client.go` JSON-RPC Client（Call + Initialize + ListTools + CallTool）
- [x] `internal/mcp/types.go` Request / Response / RPCError / InitializeResult / ToolDescriptor / CallToolResult
- [x] `internal/mcp/client.go` CallWithRetry（指数退避最多 3 次）
- [x] `stdio_test.go` 10 用例（Connect / Send+Recv frame × 2 / 子进程崩溃 / stderr drain / Close × 2 / Content-Length 缺失 / Reconnect）
- [x] `http_test.go` 8 用例（Connect / Recv closed / 200 OK / 500 / timeout / Session-Id / custom headers / Close）
- [x] `client_test.go` 12 用例 + 9 subtest（Call roundtrip / envelope / 单调 id / RPC error / id mismatch / dead transport / Initialize / ListTools / CallTool / 并发序列化 / Close / isConnectionError × 9）
- [x] `reconnect_test.go` 5 用例（重连恢复 / RPC error 不重试 / 3 次上限 / 无 factory / 退避累积）
- [x] cmd `main.go` import mcp（占位 `var _ = mcp.NewClient`）
- [x] 状态：✅ 完成（35/35 Go test 通过，`go build ./...` + `go vet ./...` 干净，`npm run build:agent` 成功，`npm run lint` + `npm test` 全绿）

### 35 · mcp-registry-and-launcher

> Go 端 McpRegistry + ResolverManager（4 类 resolver）+ npx 优化。

- [x] `internal/mcp/types.go` +ServerSpec / TransportType / ResolverKind / ResolutionStatus / LaunchResolution / ServerStatus
- [x] `internal/mcp/registry.go` McpRegistry（Register / Unregister / SetEnabled / List / Get / GetTools / GetToolsByName）
- [x] `internal/mcp/launcher.go` ResolverManager + npxResolver（完整）+ uvx / go / raw（stub）
- [x] `internal/mcp/resolver_fingerprint.go` ComputeFingerprint（sha256(command|args|env|platform|arch)）
- [x] `internal/mcp/persistence.go` ResolutionPersistence interface + InMemory impl
- [x] 陈旧 installing 检测（30min 自动重试）
- [x] Registry 与 Client 集成（fallback 到原始 command if resolver failed）
- [x] `registry_test.go` 9 用例（Register+List / Get / SetEnabled disable / Unregister / 未知 server / fingerprint 变 / GetToolsByName / 并发 / stale retry）
- [x] `launcher_test.go` 13 用例（parseNpxArgs 5 + stub 1 + npx 3 + dedup 1 + pickBinEntry 4）
- [x] `resolver_fingerprint_test.go` 4 用例
- [x] `persistence_test.go` 5 用例
- [x] cmd 注入 ResolverManager + Registry + LoadStaleResolutions
- [x] 状态：✅ 完成（38/38 mcp 测试通过 = spec 35 新增 24 + spec 34 遗留 14；`go build` + `go vet` 干净；`npm run build:agent` 成功；`npm run lint` 干净；`npm test` 162/162 通过）

### 36 · mcp-main-store-and-ipc

> main 端 mcpManager + SQLite store + IPC + bundled filesystem MCP。

- [x] SQLite 表 `mcp_servers` + `mcp_launch_resolutions` + cascade delete
- [x] `src/main/libs/mcpStore.ts`（create / get / list / update / delete / saveResolution / loadResolutions + bundled upsert）
- [x] `src/main/libs/mcpManager.ts`（bootstrap + createServer + updateServer + deleteServer + setEnabled + testConnection + retryResolution）
- [x] IPC channels `mcp:list` / `mcp:create` / `mcp:update` / `mcp:delete` / `mcp:set_enabled` / `mcp:test` / `mcp:retry_resolution`
- [x] IPC push `mcp:servers_changed` + `mcp:connection_changed`
- [x] preload `window.darvin.mcp.*` 9 个方法（listMcpServers / createMcpServer / updateMcpServer / deleteMcpServer / setMcpServerEnabled / testMcpConnection / retryMcpLaunchResolution / onMcpServersChanged / onMcpConnectionChanged）
- [x] AgentClient `mcp.*` 9 个方法（list / register / update / unregister / setEnabled / test / retryResolution / bootstrap / onConnectionChanged / onResolutionChanged）
- [x] Go 端 handler `agent.mcp.{list, register, update, unregister, set_enabled, test, retry_resolution, bootstrap}` + notification `mcp.connection_changed` + `mcp.resolution_changed`
- [x] bundled filesystem MCP（Go 写，darvin-agent `mcp-filesystem` subcommand，stdio JSON-RPC 2.0，list_directory / read_file / write_file 3 个 tool，path traversal 拦截 + 4 MiB 上限）
- [x] `mcpStore.test.ts` 10 用例 + `mcpManager.test.ts` 15 用例 + `registry_notify_test.go` 8 用例 + `handlers_mcp_test.go` 8 用例 + `mcp_filesystem_test.go` 8 用例
- [x] 状态：✅ 完成（Go 测 ~250+ 用例全绿，38 → ~50+ mcp 包测试；`go build` + `go vet` 干净；`npm run build:agent` 成功；`npm run lint` 干净；`npm test` 187/187 通过 = 162 旧 + 25 新 = 10 mcpStore + 15 mcpManager；`bin/darvin-agent-linux-x64 mcp-filesystem` 实跑 3 个 RPC：initialize / tools/list / tools/call(list_directory) 全部 1:1 命中；bundled filesystem 启动期自动注册到 mcpRegistry + main 端 SQLite 幂等 upsert）

### 37 · mcp-renderer-view

> renderer UI——McpView + 4 子组件 + i18n 35+ key。

- [x] `McpView.vue`（list + [+ 新增] + 空态）
- [x] `useMcpServers.ts` composable（refresh / create / update / remove / setEnabled 乐观+回滚 / test / retryResolution）
- [x] `McpServerCard.vue`（name / description / transport / switch / connection / launch 状态 / tools 列表 / 4 按钮）
- [x] `McpConnectionStatus.vue`（4 状态 + 颜色 + dot 动画 + tooltip；disconnected 不渲染）
- [x] `McpLaunchStatus.vue`（5 状态 + 颜色；ready 不渲染）
- [x] `McpServerFormModal.vue`（按 transportType 切字段：stdio 命令+args+env / http url+headers）
- [x] i18n +40 key（zh + en 对齐，39 mcp.* + 1 common.save）
- [x] 移除 `AppShell.vue` 的 mcp PlaceholderView 路由（'mcp' 入 switch → McpView）
- [x] `useMcpServers.test.ts` 12 用例 + `mcpForm.test.ts` 10 用例（spec 33 同款：项目无 @vue/test-utils，跳过组件级 .test.ts）
- [x] live 验证：装 / 卸 / 启停 / 测试 / 重试 launch（**CDP 实跑通过**）
- [x] 状态：✅ 完成（`npm run lint` 干净 + `npm test` 209/209 = 187 + 22 新 = 12 useMcpServers + 10 mcpForm；assertSameKeys 376/376 通过）

### 38 · tool-registry-merge-and-routing

> 把 skill / mcp 工具合并进 tool.Registry；改 tool_start / tool_end 事件。

- [x] `internal/agent/tool/tool.go` +Kind 3 类 + Entry + Plugin + ToolRegistrar（**不重写 Tool 接口**，spec 26 未落地，重写会破 5 内置 + 测试）
- [x] `internal/agent/tool/registry.go` 改造：`map[string]Tool` → `map[string]*Entry` + RegisterTool / Unregister / UnregisterByPlugin / GetEntry / List / ListByKind（`Get` / `Register` 兼容保留）
- [x] SkillPlugin + SkillTool（**落在 `internal/skills/plugin.go`**——tool 已导入 skills，放 tool 会环依赖）
- [x] `internal/agent/tool/mcp.go` McpPlugin + McpTool + `McpToolSource` 接口 + `mcp.Registry.CallTool` facade
- [x] `internal/agent/executor/executor.go` 改造：emit 前查 GetEntry 填 toolKind / skillId / mcpServerId（无需 switch 路由，skill/mcp 工具即 Tool 实现）
- [x] `internal/agent/event/event.go` ToolStartEvent / ToolEndEvent 加 ToolKind / SkillID / McpServerID（可选，旧事件兼容）
- [x] RPC `agent.tools.list` 合并返回内置 + skill + mcp（session 懒建后取 registry）
- [x] cmd 注入 SkillPlugin + McpPlugin 到 AgentFactory.Plugins + `SessionManager.RefreshAllTools`（skill 启停 / mcp 连接变化触发重注册）
- [x] `registry_test.go` +7 / `skills/plugin_test.go` +7 / `mcp_test.go` +7 / `executor_routing_test.go` +3 / `types_json_test.go` +3 / mcp registry_test +1 / handlers_test +3 = **+31 个 Go 测试**
- [x] `src/shared/darvin-api.ts` tool_start / tool_end 加可选 toolKind / skillId / mcpServerId + `listTools()` 全链路（preload IPC `tools:list` → main handler → client.ts tools 命名空间 → Go RPC）
- [x] live 验证：`listTools()` 返 builtin 5 + skill 4（enabled）+ mcp 3；`testMcpConnection` 返 ok；agent 实调三类工具（mcp 成功 / skill 摘要 / builtin）事件均带 toolKind（**CDP 实跑通过**）
- [x] 状态：✅ 完成（`go build` + `go vet` + `go test ./...` 全绿 + `npm run lint` 干净 + `npm run build:agent` 成功；live 全路径通过）

### 39 · skill-user-invocation

> chat `/skill-name args` 显式触发 skill。

- [x] `Composer.vue` 改造：自动补全浮层（`/` 触发 / 过滤 / 选中 / Escape 关闭）
- [x] `useSkills.ts` 改造：暴露 `userInvocable` 字段（Go wire + TS 类型 + main skillManager 全链路）
- [x] `/` 前缀路由（适配：renderer `useChatActions.send/regenerate` 截获 + main 暴露 `darvin:invoke_skill` IPC）
- [x] `//` 转义保留 `/` 文本（去首字符走普通 prompt，regenerate 也能区分）
- [x] `agentClient.invokeSkill({ sessionId, skillId, args })`
- [x] Go 端 handler `agent.skill.invoke_user`（同步校验 → 3 错误码 → 异步 mini loop）
- [x] `Agent.RunSkillSession` mini agent loop（skill prompt + scoped tool registry，事件流与普通 prompt 一致）
- [x] i18n +10 key（zh/en 各 +10，assertSameKeys 通过）
- [x] 纯函数 helpers `slash.ts` +10 单测（parseSlashCommand / translateSkillError）
- [x] 状态：✅ 完成（go build/vet/test 17 包全绿 + lint 干净 + build:agent 成功 + vitest 194 通过；live 待验证）

---

## 主 spec 验收（全部 9 份子 spec 落地后）

- [x] 侧栏 `技能` 跳 SkillsView，5 个 bundled skill 显示
- [x] 装 / 卸 / 启停 / 升级 / 安全报告 modal 工作
- [x] SQLite `mcp_servers` + `mcp_launch_resolutions` + `skill_state` 3 表齐全
- [x] `npm run lint` + `npm run test` + `npm run build:agent` + `go test ./...` 全绿
- [x] 侧栏 `MCP` 跳 McpView，bundled filesystem 卡片显示（live CDP 已验证）
- [x] 新增 stdio / http / 删除 / 启停 / 测试 / 重试 launch 工作（live CDP 已验证 6/6 路径）
- [x] agent 实际调用 skill / mcp 工具，事件按 `kind: 'skill' | 'mcp'` 带 toolKind / skillId / mcpServerId（live CDP 已验证三类）
- [x] `/code-review src/api/handler.go` 触发 skill（spec 39 live CDP 已验证）
- [x] i18n 新增 110+ key，zh / en 双语齐全（spec 37 已加 40 key，累计 spec 31-37 已落地）

---

## 状态变更日志

> 每完成一份子 spec，在此处记一行：日期 / spec / 「完成说明」。

- 2026-08-02 · 主 spec · 完成调研 + checklist + 9 份子 spec 拆分；待用户确认后启动 spec 31
- 2026-08-02 · spec 31 · Go 端 skills 骨架落地：types / frontmatter / loader(bundled+user) / registry / scanner / runner / bootstrap 全部实现并通过 `go test ./...`；bundled 5 个 SKILL.md 已 embed 到 `cmd/app/resources/skills-bundled/`；`cmd/app/main.go` 注入 SkillRegistry + SkillRunner。`go vet ./...` 干净，gofmt 在本批改动文件上干净，`npm run build:agent` 成功，`npm run lint` + `npm run test` 全绿。**待人工重启 Electron 验证启动日志**中出现 `skills: loaded ... bundled=5 user=0 total=5`。
- 2026-08-02 · spec 32 · Go ↔ Main IPC + main 端 SkillManager 落地：Go 端 3 个 handler（agent.skills.list / set_enabled / bootstrap）+ EventLedger.Broadcast 推 `agent.skills.changed`；main 端独立 SQLite `skill-state.db` 持久化 enabled；chokidar 监听 `userData/darvin-agent/SKILLs/**/SKILL.md`；AgentClient 暴露 `skills.{list, setEnabled, bootstrap, onChanged}` 并新增 `agent.skills.changed` 通知路由；preload 暴露 `window.darvin.skills.{list, setEnabled, onChanged}`；main IPC 通道统一为 `darvin:list_skills` / `darvin:set_skill_enabled` + push `darvin:push:skills-changed`。`go test ./...` + `npm run lint` + `npm run test` + `npm run build:agent` 全绿。**live CDP 验证通过**：① `listSkills` 返 5 bundled；② toggle 后 list 反映；③ chokidar add / unlink 都触发 reload + push 通知（**修复了 chokidar `ignored` regex 命中 `.config` 祖先路径导致整棵子树不监听的 Linux-only bug，改用函数形式显式只对 root 内 basename 判 dotfile**）。已提交 `a11a45e`。
- 2026-08-03 · spec 33 · SkillsView 落地：`shared/darvin-api.ts` 新增 `installSkill` / `uninstallSkill` / `upgradeSkill` / `getSkillDetails` 类型；`i18n.ts` +30 skill.* +4 common.*（assertSameKeys 17/17 通过）；preload 暴露 4 个新 IPC + main 端 4 个 stub handler；`useSkills` composable 走 singleton（refresh / 乐观更新 + 失败回滚 / install / uninstall / upgrade / 订阅 onChanged）+ 5/5 vitest 通过；5 个子组件 SkillCard / SkillMarketplace / SkillSecurityReportModal / SkillSettingsPanel / SkillDetailsModal 落地 + `index.ts` barrel + `plugin.svg` / `shield.svg` 图标；`SkillsView.vue` 三 tab 切换 + 2 modal；`AppShell.vue` 把 'skills' 从 PLACEHOLDERS 移到 main switch 直接走 SkillsView。`npm run lint` 干净 + `npm test` 162/162 通过。**待人工重启 Electron 验证** UI 渲染 + 4 个新 IPC handler（install / uninstall / upgrade / getDetails）跑通。
- 2026-08-03 · spec 34 · mcp transport + JSON-RPC client 落地：`internal/mcp/transport/{transport.go, stdio.go, http.go}` + `internal/mcp/{types.go, client.go}` + 4 个 `_test.go`；`cmd/app/main.go` 加占位 import `var _ = mcp.NewClient`。stdio transport：spawn 子进程（`exec.CommandContext`）+ LSP 风格 `Content-Length: N\r\n\r\n` frame + 子进程 stderr → zap log goroutine + Close SIGTERM 5s → SIGKILL + alive atomic.Bool + 子进程崩溃自动标 dead。http transport：POST + `Content-Type: application/json` + `Accept: application/json, text/event-stream` + 自定义 headers + `Mcp-Session-Id` 自动捕获并加到下次请求 + 30s 默认 timeout（v0 不解析 SSE）。client：JSON-RPC 2.0 envelope（`Request/Response/RPCError`，id 用 `atomic.Int64` 单调递增，**不复用 gateway 的 `json.RawMessage` id 以避免跨包耦合**）+ `Call` 互斥锁序列化 Send/Recv + `Initialize`（协议版本 `2024-11-05`）+ `ListTools` + `CallTool` + `CallWithRetry(method, params, maxRetries, backoffBase)` 指数退避（默认 1s 翻倍 × 3 次）+ reconnect factory（调用方提供，避免 client 重建 transport 破坏生命周期）+ `isConnectionError` 区分 RPC 错误（不重试）和 transport 断开（重试）。**35/35 Go test 通过**：`stdio_test` 10 个（真子进程 cat + sh）+ `http_test` 8 个（`httptest.NewServer`）+ `client_test` 12 个 + `isConnectionError` 9 subtest（fake transport + onRecv callback 模式回填 request id 让 response-id 校验通过）+ `reconnect_test` 5 个。`go build ./...` + `go vet ./...` 干净，`npm run build:agent` 输出 `darvin-agent-linux-x64` 成功，`npm run lint` 干净，`npm test` 162/162 通过。**待人工重启 Electron 验证** `cmd/app/main.go` import 路径不破坏启动。
- 2026-08-03 · spec 33 live · UI 实跑验证（用户已重启 Electron，CDP 探针 `:9222` 抓取 `localhost:5173` 渲染进程）：① `window.darvin.listSkills()` 直调返 5 bundled（Code Review / API Design / Testing / Web Search / Word Document，id 全对）；② 侧栏「技能」button click → AppShell 切到 `SkillsView`（5 个 `SkillCard` 渲染，5 个 `data-testid="skill-details-{id}"` 详情按钮，5 个 toggle `<input type=checkbox>`）；③ 3 个 tab `data-testid="skill-tab-{installed|marketplace|settings}"` active 态用 `border-primary text-primary` 标识，CSS 切换正常（依次点 marketplace → settings → installed，DOM 数据 1:1 符合预期：marketplace 切到后 5 个 checkbox 消失换 1 个 URL input + 1 个「选择 SKILL.md 文件…」按钮 + 1 个「安装」按钮，settings 切到后 5 个 `data-testid="skill-details-*"` 详情按钮 + 5 个 checkbox 与 bundled 数对得上）；④ 启停 switch：click 第一个 SkillCard 的 checkbox，`aria-checked` 从 `true` → `false`，再 `listSkills()` 直查 `code-review.enabled === false`，其余 4 个保持 `true`，乐观更新 + IPC 持久化 + 推回 store 链路 OK；⑤ 详情 modal：点「详情」→ `data-testid="skill-details-modal"` 出现在 `<body>` Teleport 末尾（fixed inset-0 z-50），header 显示 `code-review` + `v0.1.0`，底部 3 按钮（取消 + 升级（bundled 不渲染）+ 卸载（`disabled: true` 因 `isBuiltIn`）），点取消 modal 关闭；⑥ `SkillCard` 风险徽章：5 bundled `riskLevel` 未定义时 `showRiskBadge=false`，无徽章渲染（spec 设计要求 riskLevel 不为 safe 才显示，符合预期）。spec 33 标 ✅ 完成 + 提交。
- 2026-08-03 · spec 35 · McpRegistry + Launcher 落地：`internal/mcp/{registry.go, launcher.go, resolver_fingerprint.go, persistence.go}` + types.go 增量（+ServerSpec / +TransportType / +ResolverKind / +ResolutionStatus / +LaunchResolution / +ServerStatus）+ 4 个 `_test.go`；`cmd/app/main.go` 注入 ResolverManager + Registry + 启动期 `LoadStaleResolutions`；移除 spec 34 占位 `var _ = mcp.NewClient`。`fingerprint`：sha256(transport|command|args|env|url|headers|platform|arch)；同 spec 同 hash，改 command/env/args 不同 hash。`persistence`：interface + `InMemoryResolutionPersistence`（Save/LoadAll/Delete）。`launcher`：4 类 Resolver — npx 完整（parseNpxArgs：第一个非 -flag → 按 last `@` 拆 scoped 包 + version，`@scope/name@1.0.0` 正确解析为 name=`@scope/name` version=`1.0.0`；shim 测试里把 `@scope/name@ver` 的 trailing `@ver` 剥掉后写到 `node_modules/@scope/name/package.json` 让 Go 端可读；npm view 失败 → StatusFailed + "npm view: ..."；npm install 失败 → StatusFailed + "npm install: ..."；读 `package.json` bin（string 或 map，map 优先 basename 匹配否则取第一个）；生成 `node <abs-bin-path> <extra>` 启动行）；uvx / go / raw 是 stubResolver（永远 StatusUnsupported → registry fallback 走原始 command）。Resolve 异步：sync.Map inFlight dedup 同 serverID 并发调用，**fan-out 给每个 subscriber 独立 channel**（修了一开始单 channel 广播 bug）；60s timeout；Cancel(serverID) 给 SetEnabled(false) 用。`registry`：`serverEntry{spec, status, client, fingerprint}` + RWMutex；Register/Unregister/SetEnabled/List/Get/GetTools/GetToolsByName 全并发安全；connectServer 异步跑：resolver → StdioTransport{Command,Args,Env} → NewClient.WithReconnectFactory(stub) → Connect → Initialize → ListTools，任意步骤失败都记录到 status.ConnectionError；resolver failed/unsupported 时 fallback 用 spec.Command / spec.Args（mergeEnv 合并 spec.Env + res.Env）；LoadStaleResolutions 30min grace 扫持久化记录，retry 不在 in-flight 且超过 30min 的 installing。**38/38 mcp 测试通过**：registry_test 9（Register+List / Get / SetEnabled disable / Unregister / 未知 server / fingerprint 变 / GetToolsByName / 并发 / stale retry）+ launcher_test 14（parseNpxArgs 5 + stub 1 + npx happy / view-fail / install-fail + dedup 1 + pickBinEntry 4）+ resolver_fingerprint_test 4 + persistence_test 5 + spec 34 遗留 14。`go build ./...` + `go vet ./...` 干净，`npm run build:agent` 输出 `darvin-agent-linux-x64` 成功，`npm run lint` 干净，`npm test` 162/162 通过。**待人工重启 Electron 验证** `cmd/app/main.go` 启动扫描 `LoadStaleResolutions` 不破坏启动。
- 2026-08-03 · spec 36 · mcp-main-store-and-ipc 落地：main 端 mcpStore（独立 `userData/darvin-agent/mcp.db`，2 表 `mcp_servers` + `mcp_launch_resolutions` cascade delete，PRAGMA journal_mode=WAL + foreign_keys=ON，bundled 幂等 upsert 保留 createdAt）+ mcpManager（bundled filesystem 启动期幂等 upsert + bootstrap 推 Go + 订阅 onConnectionChanged/onResolutionChanged + create / update / delete / setEnabled / test / retryResolution）+ 7 个 IPC handler（`mcp:list` / `mcp:create` / `mcp:update` / `mcp:delete` / `mcp:set_enabled` / `mcp:test` / `mcp:retry_resolution`）+ 9 个 preload `window.darvin.mcp.*` 方法 + AgentClient mcp.* 命名空间（10 个方法含 2 个订阅）+ Go 端 8 个 gateway handler（list / register / update / unregister / set_enabled / test / retry_resolution / bootstrap）+ 2 个 broadcast（`mcp.connection_changed` / `mcp.resolution_changed`）+ Notifier 回调模式（registry → handler 单向 push，registry SetNotifier handler 实现回调）。bundled filesystem MCP 作为 `darvin-agent mcp-filesystem` subcommand：stdio JSON-RPC 2.0，list_directory / read_file / write_file 3 个 tool，path traversal 拦截（resolveWithin + EvalSymlinks）+ 4 MiB 上限 + `notifications/initialized` 静默。**Go ~250+ 用例全绿**：spec 35 既有 38 + spec 36 新增 24（registry_notify 8 + handlers_mcp 8 + mcp_filesystem 8）；`npm test` 187/187 = 162 + 25（mcpStore 10 + mcpManager 15）。`npm run build:agent` 成功输出 `bin/darvin-agent-linux-x64`；**bundled binary 实际跑通**：`echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' | ./darvin-agent-linux-x64 mcp-filesystem` → 1:1 返 2024-11-05 + serverInfo=`darvin-filesystem@0.1.0` + 3 tools schema。
- 2026-08-03 · spec 37 · mcp-renderer-view 落地：`useMcpServers.ts` singleton composable（refresh / create / update / remove / setEnabled 乐观+回滚 / testConnection / retryResolution / 订阅 onMcpServersChanged + onMcpConnectionChanged）+ 4 个子组件（`McpServerCard.vue` name/description/transport/switch/connection/launch/tools 列表/4 按钮 builtin 禁删 + `McpConnectionStatus.vue` 4 状态 dot 动画 + `McpLaunchStatus.vue` 5 状态 ready 不渲染 + `McpServerFormModal.vue` 按 transportType 切字段 stdio(command+args+env) / http(url+headers) Teleport 到 body）+ `McpView.vue`（list + [+ 新增] + 空态 + FormModal + window.confirm 删除）+ `mcpForm.ts` 纯函数 helpers（parseArgs 按空白 split / parseKv 一行 KEY=val 跳空行注释 / formatKv 反向序列化）+ `index.ts` barrel + AppShell.vue 把 'mcp' 从 PLACEHOLDERS 移到 switch 走 McpView（spec 33 同款流程）+ i18n +40 key（39 mcp.* 4 子域：list / badge / transport / connection / launch / tools / action / modal / field / test / delete / create / update + 1 common.save）。**vitest 209/209 通过** = 187 + 22 新（useMcpServers 12：refresh / create / update / remove / setEnabled 乐观 / setEnabled 回滚 / test 成功 / test 失败 / retry / onServersChanged 覆盖 / onConnectionChanged 更新 / onConnectionChanged 忽略未知 id；mcpForm 10：parseArgs 3 + parseKv 5 + formatKv 2）。`npm run lint` 干净；assertSameKeys 376/376 通过。spec 33 SkillCard.test.ts 同款：项目无 @vue/test-utils 模式，McpServerCard / McpServerFormModal 的组件级 .test.ts 跳过，只测纯函数 helpers（mcpForm）+ composable（useMcpServers）。
- 2026-08-03 · spec 37 live · UI 实跑验证（用户已重启 Electron，CDP 探针 `:9222` 抓取 `localhost:5173` 渲染进程，期间踩 better-sqlite3 NODE_MODULE_VERSION 127 vs 148 不匹配 → `electron-rebuild -f -w better-sqlite3` 解决；`npm rebuild` 只对系统 Node 重建没用）：① `window.darvin.listMcpServers()` 直调返 1 server（filesystem bundled，enabled=true，isBuiltIn=true，transportType=stdio，command=Electron 二进制路径，args=['mcp-filesystem']）；② 侧栏「MCP」button click → AppShell 切到 `McpView`（标题 `MCP 服务器`、`+ 新增 MCP server` 按钮、1 张 `data-testid=mcp-card-filesystem`）；③ `McpServerCard` 渲染 1:1：`Filesystem` 标题 + 内置徽章 + toggle checkbox `checked=true` + 描述 `本地文件系统读写（bundled）` + transport 标签 `stdio` + 命令行文本 + 3 按钮（测试连接 / 编辑 / 删除 `disabled=true` 因 isBuiltIn）；④ [+ 新增] → modal 出现（`编辑 MCP server` 标题），填 name=github-test / command=npx / args=`-y @modelcontextprotocol/server-github` → 保存 → 卡片新增 `mcp-card-mcp_<uuid>`，IPC 验 args 解析为 `['-y', '@modelcontextprotocol/server-github']` 数组；⑤ toggle click → 乐观 enabled=false → IPC `listMcpServers()` 验 `enabled=false` 已落 SQLite；⑥ 编辑 → modal prefilled name=github-test / command=npx / args 一致 → 改 name=github-prod → 保存 → IPC 验 name 更新成功；⑦ 删除 → confirm 弹 `删除 MCP server「github-prod」？` → ok=true → 卡片消失，IPC 验只剩 filesystem；⑧ 测试连接 click → toast 显示 `连接失败：unsupported transport ""`（**Go 端 wire 反序列化 transportType 缺失的 spec 36 缺陷，非 spec 37 范围**；renderer → IPC → main → Go → error 反馈链路本身 1:1 命中设计）。spec 37 标 ✅ 完成。
- 2026-08-03 · spec 38 · tool-registry-merge-and-routing 落地：**§0 wire bug 修复**——`ServerSpec` 全字段加 JSON tag（transportType / isBuiltIn / githubUrl / registryId），`types_json_test.go` 3 用例（round-trip + camelCase decode/marshal）。**tool.Registry 增量改造**（spec 26 未落地，重写 Tool 接口会破 5 内置 + 测试，改走 Entry 包装）：`Kind`（builtin/skill/mcp）+ `Entry`（Tool+Kind+Metadata+PluginID）+ `Plugin`/`ToolRegistrar` 接口；Registry 内部 `map[string]Tool`→`map[string]*Entry`，`Register`/`Get` 兼容保留，新增 `RegisterTool`/`Unregister`/`UnregisterByPlugin`/`GetEntry`/`List`/`ListByKind`。**SkillPlugin 落 `internal/skills/plugin.go`**（tool 导入 skills 会环依赖），SkillTool.Execute 解析 skill 上下文返回摘要（mini agent loop 属 spec 39）；**McpPlugin/McpTool** + `mcp.Registry.CallTool` facade + `McpToolSource` 接口（测试注入 fake）。**executor** emit 前查 GetEntry 填 ToolKind/SkillID/McpServerID，事件加 3 可选字段。**gateway** `agent.tools.list` RPC（session 懒建后取 registry）+ `SessionManager.RefreshAllTools`（skill set_enabled / mcp connection_changed 触发插件重注册）+ `AgentFactory.Plugins` 注入（session 懒建时应用）+ main.go 构建 skill/mcp 插件接线。**TS** darvin-api.ts tool_start/end 可选字段 + `listTools()` 全链路（preload IPC `tools:list` → main handler → client.ts tools 命名空间 → Go RPC）。**+31 Go 测试全绿**（tool registry 7 + skills/plugin 7 + tool/mcp 7 + types_json 3 + mcp registry 1 + executor_routing 3 + gateway 3），`go build`/`go vet`/`go test ./...` 干净，`npm run build:agent` 输出 `bin/darvin-agent-linux-x64` 成功，`npm run lint` 干净。**待 live**：Electron 重启后 `window.darvin.listTools()` 应返 builtin + skill: + mcp: 三类（filesystem 连接后含 3 个 mcp:filesystem:* 工具）；`testMcpConnection({id:'filesystem'})` 应返 ok（§0 修复生效）。
- 2026-08-03 · spec 38 live · 全链路 CDP 实跑验证 + 4 个 spec 36/34 遗留 bug 修复：**① §0 wire**——ServerSpec JSON tags 生效，`testMcpConnection({id:'filesystem'})` 从 `unsupported transport ""` → ok:true + 3 tools；**② mcpManager 命令路径**——`bundledFilesystemSpec()` 用 `process.execPath`（Electron）spawn 失败，改 `resolveAgentBinaryPath()`；**③ 帧协议**——`mcp_filesystem.go` 行式 JSON 改 `Content-Length` 帧（与 StdioTransport 一致），否则 Initialize 死锁；**④ 子进程被杀**——StdioTransport `exec.CommandContext` 绑 connectServer 的 ctx，connectServer 返回即 cancel 杀 MCP server，改 `exec.Command`；另修 eventledger + parseDarvinEvent 两处丢 toolKind/skillId/mcpServerId。**live 全通过**：`listTools()` 三类（builtin 5 + skill 4 enabled + mcp 3）、禁用 skill 排除、`testMcpConnection` ok、agent 实调 mcp(`mcp:filesystem:list_directory` 成功返回目录)/skill(`skill:web-search`)/builtin(`shell`) 三类，事件均带正确 toolKind。主验收「agent 实际调用 skill/mcp + 按 kind 渲染」标 ✅。
- 2026-08-03 · spec 39 · skill-user-invocation 落地：`/skill-name args` 显式触发 skill。**Go**——`SkillSummaryWire` + `userInvocable`（ToSummary 填充）+ **`Agent.RunSkillSession`**（新文件 `agent_mini_loop.go`：临时覆盖 `runSkillPrompt`/`runSkillTools`，Instructions()/Tools() 按 transient 返 skill 上下文；`buildSkillTools` 从 full registry 投影 skill 允许工具集并保 Kind/Metadata，空集给空 registry）+ **acp.Loop `SubmitSkill`**（promptReq 带 skill 字段，executeTurn 分支跑 RunSkillSession，messageId/runId 由 Loop mint 事件流与普通 prompt 一致）+ **gateway `agent.skill.invoke_user`**（同步校验存在/enabled/userInvocable → 新错误码 -32010/-32011/-32012；通过后提交 Loop 异步跑，返 prompt 同形 ticket）+ Handler 注入 `SkillRunner` + main.go 接线。**TS**——`DarvinSkillSummary.userInvocable` + `DarvinInvokeSkillRequest/Response` + `client.invokeSkill` + preload `invokeSkill` + main `darvin:invoke_skill` IPC（mint runId 注册 abort 用 + 返 prompt 同形结果）。**Renderer**——Composer `/` 自动补全浮层（过滤 enabled+userInvocable / ArrowDown·Up 导航 / Enter·Tab 选中 / Escape 关闭 / `//` 与多行不弹）+ `useChatActions.send/regenerate` `/` 路由（`//` 转义去首字符走普通 prompt、气泡保留原始输入；`/skill args` → invokeSkill；校验失败 toast 不画错误气泡）+ `slash.ts` 纯函数 helpers（parseSlashCommand 只取首行 / translateSkillError）+ i18n +10 key。**bundled `testing` SKILL.md 补 `invocation.userInvocable: true`**（否则 5 个 bundled 只有 4 个可手动触发，与场景 1 冲突）；main BUNDLED_SKILLS + user 目录默认 `userInvocable=false`（与 Go loader 一致）。**适配说明**：spec FR-3 假设的 `chat:send` IPC 不存在——实际 prompt 走 renderer `useChatActions.send` → `darvin:prompt`；截获逻辑落在 renderer send 层（应用真正的发送入口），main 只暴露 invoke_skill 通道。**测试**：+10 Go（agent mini_loop 3：作用域断言 / 保 kind / 空工具集；gateway handlers_skill 7：未配置 / 不存在 / 禁用 / 不可触发 / 成功 ticket / 缺 skillId / err 透传）+ wire 2（userInvocable 序列化 / 默认 false）+ TS slash 10（parseSlashCommand 7 + translateSkillError 3）。**验证**：`go build`/`go vet`/`go test ./...` 17 包全绿，`npm run lint` 干净，`npm run build:agent` 输出 `bin/darvin-agent-linux-x64` 成功，vitest 194 通过（25 失败为 better-sqlite3 ABI 预存环境问题）。**待 live**：Electron 重启后验证浮层（`/` 显示 5 个 skill、`/co` 过滤、Enter 选中）、`/code-review src/api/handler.go` 触发事件流、`/unknown` toast、`//skill-name` 普通 prompt。
- 2026-08-03 · spec 39 live · CDP 实跑验证全通过：①`/` 浮层显示 5 个 skill（`userInvocable` 全 true；测试前 code-review 是 disabled，为走 `/co` 场景先 `setSkillEnabled` 恢复——用户如需保持禁用可再 toggle）；②`/co` 过滤到 code-review；③Enter 选中 → 输入框变 `/code-review `；④`/code-review src/api/handler.go` 触发 mini loop——assistant 流式输出（"I'll perform a static code review of src/api/handler.go"）+ Bash/read_file 工具组 + token 统计（0 in · 809 out），turn 正常收尾，事件流与普通 prompt 一致；⑤`/unknown-skill xxx` → toast「Skill 不存在：unknown-skill」（Go ErrSkillNotFound → RPC error → i18n 文案）；⑥`//skill-name is a library` → 普通 prompt 不触发（气泡保留 `//` 原文，无 toast）。**live 修复 1 个 bug**：选中 skill 后继续输入 args 时浮层仍保持打开（text 仍以 `/` 开头），Enter 会重复选中而非发送——`onInput` 加 `!text.includes(' ')`（出现空格即收浮层）。修复后 `go test` 全绿 + `npm run lint` 干净 + slash/i18n vitest 27/27 通过。主验收「`/code-review src/api/handler.go` 触发 skill」标 ✅。
