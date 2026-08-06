# MCP 集成硬化设计文档

> **父文档**：[`../features/skills-and-mcp-modules/2026-08-02-skills-and-mcp-modules-design.md`](../../features/skills-and-mcp-modules/2026-08-02-skills-and-mcp-modules-design.md) 及子 spec 34 / 35 / 36 / 37。
>
> **本 spec 范围**：把现有 Go 端 MCP 实现从「单一 Content-Length 帧 + 单进程直管 + 没超时/取消」升级到「newline-delimited JSON + reader goroutine 架构 + 全生命周期管控 + 平台兼容 + 跨进程族追踪」。参考实现：
>
> | 来源 | 学到什么 |
> |------|---------|
> | [`DeepSeek-Reasonix/internal/plugin/transport_stdio.go`](../../../../github-project/DeepSeek-Reasonix-main-v2/internal/plugin/transport_stdio.go) | dedicated reader goroutine + per-id pending channels；newline-delimited JSON 权威实现；process group / Windows Job Object 追踪；graceful close (stdin EOF → 750ms → kill) |
> | [`DeepSeek-Reasonix/internal/plugin/transport_sse.go`](../../../../github-project/DeepSeek-Reasonix-main-v2/internal/plugin/transport_sse.go) | SSE endpoint 握手（GET stream + POST endpoint）+ same-origin 校验 + bounded reply queue |
> | [`DeepSeek-Reasonix/internal/plugin/plugin.go`](../../../../github-project/DeepSeek-Reasonix-main-v2/internal/plugin/plugin.go) | `Start` 并发握手 + `beginSpawn` 同 server 去重 + Phase A/B 分阶段 + failure tracking |
> | [`DeepSeek-Reasonix/internal/plugin/startup.go`](../../../../github-project/DeepSeek-Reasonix-main-v2/internal/plugin/startup.go) | startupFailure{stage, elapsed, stderr} + RedactCredentials |
> | [`DeepSeek-Reasonix/internal/mcpregistry/registry.go`](../../../../github-project/DeepSeek-Reasonix-main-v2/internal/mcpregistry/registry.go) | 官方 MCP Registry API 客户端（v0.1/servers） |
> | [`LobsterAI/src/main/libs/resolveStdioCommand.ts`](../../../../github-project/LobsterAI/src/main/libs/resolveStdioCommand.ts) | npx → 自带 node 替换；`getElectronNodeRuntimePath` |
> | [`LobsterAI/specs/refactors/mcp-native-migration/2026-05-14-mcp-native-migration-design.md`](../../../../github-project/LobsterAI/specs/refactors/mcp-native-migration/2026-05-14-mcp-native-migration-design.md) | mcp.servers 配置 schema；openclawConfigSync 注入路径 |
> | [`LobsterAI/specs/bugfixes/mcp-stdio-process-leak/2026-05-28-mcp-shared-runtime-design.md`](../../../../github-project/LobsterAI/specs/bugfixes/mcp-stdio-process-leak/2026-05-28-mcp-shared-runtime-design.md) | shared runtime by config fingerprint |
> | [`OpenClaw/src/agents/mcp-stdio-transport.ts`](../../../../github-project/LobsterAI/docs/openclaw-main/src/agents/mcp-stdio-transport.ts) | ReadBuffer + serializeMessage from SDK（newline-delimited JSON 权威） |
>
> 创建日期：2026-08-06
> 状态：⏳ 待用户确认
> 前置：spec 34 / 35 / 36 / 37 / 38 ✅

---

## 0. AGENTS.md 合规清单（本 spec 必须遵守）

> 本 spec 涉及主进程 / preload / renderer / Go agent / 文档 / 测试多个域，按 [`/AGENTS.md`](../../../../AGENTS.md) 分项列出强制约束。**实现阶段每条都要复核**。

### 0.1 提交与分支纪律

| 规则 | 本 spec 落地 |
|---|---|
| 分支命名 `feat/...` / `fix/...` / `chore/...` / `refactor/...` | 本 spec 创建分支建议 `feat/mcp-stdio-hardening`（覆盖多 FR 的复合改动） |
| Conventional Commits 英文 | 实现完成后 commit message 用 `feat(mcp): newline-delimited stdio + reader goroutine + process group` |
| **不主动 commit**，**不主动 broad refactor**，**不写 `Co-Authored-By` 尾部** | 严格遵守；本 spec 仅是规划，等用户明确说"提交 COMMIT"才 commit |
| 提交前自检：diff 无无关改动 / 无生成产物（`.vite/` / `out/` / `bin/darvin-agent-*`）/ 无用户可见字符串错误 | CI lint 之前手动跑 `git status` + `git diff --stat` |

### 0.2 注释规范（业务源代码）

**禁止编写**：

- 阶段 / 版本 / 迭代规划类注释：`// S1 阶段实现` / `// v0 占位，v1 重构` / `// 后续迭代替换` / `// 未来会接入 MCP 协议`
- 代码复述型废话注释（代码已写 `if (!path) return undefined` 还加 `// 如果路径不存在就返回空`）
- 模型思考 / 编写过程 / 改动说明注释：`// 按照规范调整写法` / `// 适配项目架构修改逻辑` / `// 根据 AGENTS.md 约束重构`
- 展望 / TODO 大范围规划注释（仅极小范围内部标记 TODO 可保留）
- 无关铺垫 / 开场白 / 收尾总结注释（`// 下面实现二进制路径解析逻辑` / `// 以上完成进程启动封装`）
- 冗余分隔线 / 空行堆砌注释（`// --------------------------`）
- Vue `<template>` 内 HTML 注释
- 标识符命名带版本号（`ErrNotImplementedInV0` / `FixForV2` / `MockS5`）

**允许保留**：

- 导出公共函数 / 类型 / 类：JSDoc 注释（入参含义 / 返回值 / 边界约束 / 业务不变量，简洁短句，无 `@example`）
- 非常规特殊逻辑：单行意图注释（写**为什么这么写**，而非写代码做了什么）
- 硬性架构约束校验注释（如 `// 事件结构必须对齐 DarvinEvent 类型定义`）
- 关键边界兜底逻辑注释（平台差异 / 异常兜底）

### 0.3 TypeScript / Vue 风格

| 规则 | 本 spec 落地 |
|---|---|
| `tsconfig.json` strict + noImplicitAny 已启用 | 新文件全 strict |
| 2 空格缩进 / 单引号 / 分号 | ESLint 自动 enforce |
| 业务逻辑保持在 `src/main/libs/` / `src/main/runtime/` / `src/darvin-agent/`，不进 UI 组件 | MCP 状态机 / 连接池放 Go 端；renderer 只调 IPC |
| 优先用现有模式与本地 helper，不要造新抽象 | `resolveStdioCommand` 等新模块放 `src/main/libs/` 沿用既有 `*.ts` 单文件 + `*.test.ts` 同目录约定 |
| 严禁组件内 `<style>` 块 | McpServerCard / McpServerFormModal 改造不引入 `<style>` |
| 严禁 Tailwind 默认调色板（`bg-gray-800` / `text-red-500`） | 全走 `theme.css` 的 `@theme` token：`bg-surface` / `text-text-muted` / `text-danger` / `bg-success` |
| 严禁内联 `style="..."` 颜色 / 间距 | 同上 |
| 严禁第三方组件库（Naive UI / Element Plus / PrimeVue / lucide-vue-next / @heroicons） | 不引 |
| 严禁 Tailwind `dark:` 变体 | 用 `<html class="light">` 触发 token 覆盖 |
| Vue 10 条组件化规范 | SFC + `<script setup lang="ts">` / Composition API / props 强类型 `defineProps<T>()` / composable 共享状态 / 单向数据流 / PascalCase 组件名 / `camelCase` composable 名 + `use` 前缀 / 业务 ref 不加后缀 / 模块 ref 加 `Ref` 后缀 |

### 0.4 国际化（i18n）

| 规则 | 本 spec 落地 |
|---|---|
| 任何面向用户的字符串必须经 `t()` | 本 spec 新增的所有 UI 字符串（"连接失败" / "认证失败" / "已禁用" 等）一律走 `t('mcp.xxx')` |
| **不**写 `<template>` 内直接中文字面量 | 全走 `{{ t('mcp.field.transport_type') }}` 等 key |
| **不**写 `console.log` 翻译；开发者诊断信息保留英文 | Go agent 日志全英文；renderer `console.warn` 也保留英文 |
| **不**走 i18n：DevTools / stdout / 错误堆栈 / 异常 message / IPC 协议字段 / 模型名 / API Key 前缀 | Go 端 stderr / JSON-RPC error.message 保持英文 / kebab-case |
| 数字 / 时间 / 日期硬编码格式 → `formatNumber` / `formatDate` / `formatRelativeTime` | "上次连接 2h 前" 类文案走 `formatRelativeTime` |
| 占位文案也要走 `t()` | "暂无 MCP server" / "未配置" 占位全走 key |
| key 命名：`feature.subfeature.label` | `mcp.card.error.connection` / `mcp.action.test` / `mcp.status.failed` |
| 不复用 renderer 的 `services/i18n.ts` | 新建 `src/main/i18n.ts`（main 进程 i18n），与 renderer `services/i18n.ts` 各自独立 |
| zh / en 字典必须包含完全相同的 key 集合 | `src/shared/i18n-dict.ts` 提供共享 dict，renderer / main 各包一层 runtime API；`assertSameKeys(dictZh, dictEn)` 校验 |
| 翻译缺失临时态：英文 key 用 `dictEn[key] = dictZh[key]` 兜底 | 严格不允许直接 fallback 到 zh 上生产 |
| 何时升级 vue-i18n | 当前不升级 |

### 0.5 设计 / 样式 token

| 规则 | 本 spec 落地 |
|---|---|
| 颜色 / 间距 / 圆角 / 字号 / 动画 token 唯一来源 `@theme` 块 | 不新增 magic value；需要新 token 先在 `theme.css` 加再消费 |
| 颜色 token：`bg` / `surface` / `surface-2` / `border` / `text` / `text-muted` / `text-subtle` / `accent` / `accent-hover` / `user-msg` / `assistant-msg` / `danger` / `success` / `warning` | status badge 用 `bg-success` / `bg-danger` / `bg-warning`（若未存在则先在 `theme.css` 加 `--color-success: ...`） |
| 间距 token：`app-padding` (12px) / `section-gap` (16px) | 卡片间距用 `gap-section-gap` / `p-app-padding` |
| 圆角 token：`sm` (4px) / `md` (6px) / `lg` (8px) / `xl` (12px) | 卡片用 `rounded-lg` |
| 字号 token：`xs` (11px) / `sm` (13px) / `base` (14px) / `md` (15px) / `lg` (17px) / `xl` (20px) | error message 用 `text-sm` |
| 动画 token | 状态变化 fade-in 走 Tailwind 自带 `transition-opacity` |
| 禁用 `bg-[#1a1a1a]` / `text-gray-300` / 硬编码 magic value | 严格禁止 |

### 0.6 图标

| 规则 | 本 spec 落地 |
|---|---|
| 只用 SVG | 不引 lucide-vue-next / @heroicons |
| 源文件 `src/renderer/assets/icons/<kebab-case>.svg`，必须 `stroke="currentColor"` | 新增 icon（如 `mcp-link` / `mcp-error`）丢到对应路径 |
| `<Icon name="xxx" />` 全局组件 | status badge 用现有 icon（`alert-circle` / `check` 等）；缺则先加 SVG |

### 0.7 字符串常量

| 规则 | 本 spec 落地 |
|---|---|
| IPC 通道 / 模式名 / 判别值常量化 + 同时导出值对象 + 类型 | 新增 channel / 状态枚举走 `src/shared/darvin-api.ts` 已有的 `DarvinMcpConnectionStatus` 等 |
| 测试断言使用同一常量 | 单测里 `expect(s.connectionStatus).toBe(DarvinMcpConnectionStatus.Connected)` |
| 一次性错误消息 / CSS class / HTML attribute / 外部平台 ID 不用常量化 | 连接错误文案走 `t()`，不常量化 |

### 0.8 测试纪律

| 规则 | 本 spec 落地 |
|---|---|
| 测试文件 `*.test.ts` 放被测源码旁 | `stdio.test.go` 在 `transport/`；`registry_notify_test.go` 在 `internal/mcp/`；`useMcpServers.test.ts` 已在 |
| `npm run test`（vitest run，include `src/**/*.test.ts`） | CI 入口 |
| 新增逻辑：能不写测试就不写；优先覆盖纯函数 / IPC 协议解析 / 路径与序列化工具 | 重点覆盖 reader goroutine + pending channels + graceful close + RedactCredentials + resolveStdioCommand |
| 不为 Electron 主进程写集成测试 | 用 playwright-cli dev 手动验证 |
| Mock electron 用 `vi.mock('electron', ...)` + `vi.hoisted` | `resolveStdioCommand.test.ts` 按 `src/main/libs/user-paths.test.ts` 范式 |

### 0.9 质量门槛（实现完成后必跑）

| 改动类型 | 验证命令 |
|---|---|
| 主进程 / preload / runtime | `npm run lint` + `npm start`（窗口能开 + Go agent 解析路径） |
| renderer | `npm run lint` + `npm start` + DevTools console 无 error |
| Go 端 | `cd src/darvin-agent && go build ./... && go vet ./...` |
| `scripts/build-go.js` 改动 | `npm run build:agent` |
| `npm run test` | vitest 全过；之前 25 个 better-sqlite3 native module 失败属环境问题不算回归 |
| 集成手测 | 见 §7 验收标准；用 playwright-cli attach 9222 端口验证 UI / IPC |

### 0.10 PR / 验收

| 规则 | 本 spec 落地 |
|---|---|
| 简洁描述 + 关联 issue + UI 截图（若有 UI 改动）+ Electron 特定变更说明（IPC / preload / 窗口 / Go 二进制打包路径） | PR body：`feat(mcp): ...`、`Fixes #XXX`、UI 截图（McpServerCard 改造前后对比）、IPC 字段变化（`agent.mcp.register` 接受 ResolvedStdioCommand）、Go 二进制路径不变 |
| `git diff --stat` 检查无生成产物 | 手动确认 `.vite/` / `out/` / `bin/darvin-agent-*` 不在 diff 里 |

### 0.11 文件命名约定

| 类别 | 命名 | 本 spec 落地 |
|---|---|---|
| Vue 组件文件 | `PascalCase.vue` | `McpServerCard.vue` / `McpServerFormModal.vue`（已存在） |
| composable | `camelCase.ts` + `use` 前缀 | 新 `useMcpFailure.ts` 放 `src/renderer/composables/` |
| 模块级 ref | `xxxRef` 后缀 | `const serversRef = ref<...>()` |
| 业务 ref | 不加后缀 | `const loading = ref(false)` |
| Go 文件 | snake_case 已用 | 沿用 `stdio.go` / `client.go` / `registry.go` |
| 测试文件 | 与被测同名 + `.test.` 后缀 | `stdio.test.go` / `registry_notify_test.go` |

### 0.12 严禁列表（本 spec 触发时必查）

实现完成后 grep 自检：

- [ ] 无组件 `<style>` 块
- [ ] 无 Tailwind 默认调色板（`bg-gray-*` / `text-red-*`）
- [ ] 无内联 `style="..."` 颜色 / 间距
- [ ] 无第三方组件库 import
- [ ] 无 `<template>` 内 HTML 注释
- [ ] 无阶段 / 版本 / 迭代规划注释（`S1` / `v0` / `v1` / `后续迭代` / `未来`）
- [ ] 无代码复述型废话注释
- [ ] 无 magic value（`#1a1a1a` / `12px`）
- [ ] 无硬编码中文字面量
- [ ] 无标识符命名带版本号（`ErrV0` / `MockS5`）
- [ ] 无 `Co-Authored-By` commit 尾部
- [ ] 无未授权 commit

---

## 1. 概述

### 1.1 问题 / 背景

实测复现（playwright 连接运行中 Electron 实例，2026-08-06）：

用户在 MCP 视图新增 `stdio` 类型 GitHub MCP server：

```
name: github
command: npx
args: -y @modelcontextprotocol/server-github
env: GITHUB_PERSONAL_ACCESS_TOKEN=ghp_***
```

预期：30 秒内卡片显示 `create_or_update_file` / `search_repositories` / `create_issue` 等 26 个 tool。
实际：

| 阶段 | 现象 |
|------|------|
| 配置保存 | ✅ SQLite 写 `mcp_servers` + `mcp_launch_resolutions.status=ready` |
| npm install | ✅ 装好包，bin 解析到 `dist/index.js` |
| spawn node 子进程 | ✅ 子进程启动，`stderr: "GitHub MCP Server running on stdio"` |
| **initialize 握手** | ❌ 永远卡死，`connectionStatus` 永远 `"connecting"` |
| `testMcpConnection()` | `{"ok":false,"error":"not connected"}` |
| 进程泄漏 | ❌ 每次 retry 多 spawn 一个 `node` 进程 |

### 1.2 根因分析

**根因 1（核心）—— Wire format 不兼容 + 同步阻塞 I/O**

Go 端 `internal/mcp/transport/stdio.go:165-180` 用 **LSP-style Content-Length 帧**：

```
Content-Length: 96\r\n\r\n{"jsonrpc":"2.0","id":1,"method":"initialize",...}
```

但 `@modelcontextprotocol/sdk@1.0+`（npm 注册的所有新版 MCP server）切到 **newline-delimited JSON**：

```js
// @modelcontextprotocol/sdk/dist/shared/stdio.js
export function serializeMessage(message) { return JSON.stringify(message) + "\n"; }
export class ReadBuffer { readMessage() { return firstLineAsJSON(); } }
```

外加 darvin-cowork 当前架构问题更严重——**Send / Recv 在同一 goroutine 用 mutex 串行**（`client.go:64-104`）。一旦 server 不响应，`Recv()` 永久 hang，连带着 client mutex 也不释放，后续任何 `Call` 都进不来。

**根因 2 —— 没有 reader goroutine / pending channels**

DeepSeek-Reasonix 的正解：dedicated reader goroutine 独占 stdout，按 `id` demux 到对应 pending channel；`call(ctx)` 用 `select { case <-ctx.Done(): return ctx.Err(); case resp := <-ch: }`。caller cancel → request 立即 abandon，subprocess 仍存活。

darvin-cowork 当前：`Call` 是 mutex + blocking `Recv()`，cancel 救不了；只能等 goroutine 自然死亡。

**根因 3 —— 没有 process group / Job Object 追踪**

`internal/mcp/transport/stdio.go` spawn 时不 `Setpgid: true`；Windows 也没 Job Object。结果：

- npm / uvx → npx-cli → node child 这一链条，Close 只能杀 npx-cli，node child 残留
- GitHub MCP 测试时观察到 5 个孤儿 `node` 子进程

DeepSeek-Reasonix 用 `proc.StartTracked(cmd)`：Unix `Setpgid`，Windows Job Object with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`；kill 走 `KillTracked(pid, job)` → 整族 SIGTERM/SIGKILL。

**根因 4 —— spawn 必新进程，无复用**

`registry.go:397 connectServer`：
1. 无条件 `exec.Command(s.Command, s.Args...)` —— 不检查已有 entry.client 是否 Alive
2. `WithReconnectFactory` 永远返 `"reconnect not implemented"` 错误
3. retry / register / bootstrap 任一路径都重新 spawn
4. 已 connected 状态被新连接覆盖前会短暂断开

DeepSeek-Reasonix 用 `beginSpawn(key, server)` —— 同 server 同时只有一个 spawn goroutine，第二个等待第一个；`Host.has(name)` 检查短路。

**根因 5 —— spawn 环境缺失（GUI 启动问题）**

Electron 从 Finder / Dock / `open(1)` 启动时，`PATH` 只有 `/usr/bin:/bin:/usr/sbin:/sbin` —— 用户的 `~/.nvm/versions/node/.../bin`、`~/.cargo/bin` 全不在。`npx` / `uvx` / `python` 直接 fail with "command not found"。

DeepSeek-Reasonix 的 `enrichStdioShellPATH`：spawn user 的 login shell (zsh/bash `-l -i -c`)，捕获 `$PATH`，prepend 到 child env。带 in-flight probe dedup + cancel-aware cache。

**根因 6 —— 私域 cache 污染用户 home**

npm / uv / bun 默认 cache 写到 `~/.npm` / `~/.cache/uv` / `~/.bun/install/cache`。多 MCP server → 多份孤立 cache。DeepSeek-Reasonix 的 `prepareMCPPrivateState` 把 `XDG_CACHE_HOME` / `npm_config_cache` / `UV_CACHE_DIR` 重定向到 darvin-cowork 私域目录。

**根因 7 —— HTTP / SSE / streamable-http 不完整**

只有 stdio 完整；HTTP transport 是 stub（`Recv()` 直接返上次 Send）。SSE / streamable-http 完全没实现。Anthropic 官方 / GitHub Copilot / Cloudflare 等托管 MCP server 全是 HTTP 形态。

DeepSeek-Reasonix 的 `sseTransport` 完整实现了 legacy HTTP+SSE（GET stream + POST endpoint 握手 + same-origin 校验），值得照搬。

**根因 8 —— 失败处理一刀切 abort**

`connectServer` 失败时整个 batch 中断（spec 35 §2 场景 6）。一个 server 配错 → 全员连不上。

DeepSeek-Reasonix 的 `StartAvailable`：失败 record 到 `Host.Failures`，其余继续；failure 列表单独暴露给 `/mcp status`。

### 1.3 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | Go 端 stdio transport 切到 newline-delimited JSON（SDK 1.x 兼容）；bundled filesystem（自写 Content-Length）保留兼容路径 | 任意 `@modelcontextprotocol/server-*@1.x` 包 30 秒内 list_tools 成功 |
| G2 | reader goroutine + per-id pending channels 架构；`call(ctx)` 用 select 监听 ctx.Done() | caller cancel → 立即返回 ctx.Err()；subprocess 仍存活 |
| G3 | process group / Job Object 追踪；graceful close (stdin EOF → 750ms → kill) | 5 次 retry → 1 个孤儿（当前 5+）；应用退出 5 秒内无残留 |
| G4 | GUI 启动时探测 user shell PATH；Windows fallback PATH (ProgramFiles/nodejs, scoop, .bun/bin) | Windows 安装包 / macOS Finder 启动后 npx / uvx 可用 |
| G5 | private state dir 隔离 npm/uv/bun cache | 用户 `~/.npm` / `~/.cache/uv` 不被 MCP 污染 |
| G6 | HTTP transport 支持 streamable-http + legacy SSE + application/json 同步 | Anthropic / GitHub Copilot / Cloudflare MCP 接入 |
| G7 | 启动失败 record 到 host 而非 abort 全 batch | 一个 server 配错不影响其余 |
| G8 | 同时刻同 server 只一个 spawn goroutine；已有连接不重复 spawn | retry 不再泄漏子进程 |

### 1.4 非目标

- 不替换为 OpenClaw / LobsterAI runtime（架构层改成本太高）
- 不引入 `@modelcontextprotocol/sdk` 到 Go（跨语言）
- 不做 OAuth bearer 自动刷新（v1 仅手动 token + env 注入）
- 不做 resumability / Last-Event-ID（streamable-http 流式通知够了；resumability 单独 spec）
- 不做 marketplace 自动发现 / 自动安装（v1 仅手动配置 + npxResolver 优化）
- 不做 sandbox 隔离（macOS sandbox-exec / Windows AppContainer；DeepSeek-Reasonix 做了，v0 不做）

---

## 2. 用户场景

### 场景 1：用户新增 GitHub MCP server 并成功连接

**Given** 应用启动；MCP 视图为空
**When** 用户点 `+ 新增`，填：
```
name=github
transport=stdio
command=npx
args=-y @modelcontextprotocol/server-github
env=GITHUB_PERSONAL_ACCESS_TOKEN=<token>
```
**Then**：
1. SQLite 写 `mcp_servers`
2. npxResolver 跑：`npm view` + `npm install` + 读 `bin` → `mcp_launch_resolutions.status=ready`，resolved `command=node`, `args=[<abs-bin-path>, ...]`
3. registry 检测到新 server → `beginSpawn("github")` 拿 owner → 触发 `connectServer` goroutine
4. `resolveStdioExecutable`：探测 user shell PATH → prepended to env；npx-cli 已在 PATH → 透传
5. spawn `node <abs-bin>` → 进程启动
6. `client.start()` 启动 readLoop（独占 stdout）
7. write initialize (`<json>\n`) → readLoop 拿到 initialize response
8. write initialized notification → write tools/list → readLoop 拿到 26 个 tool
9. `entry.status.Connected = true`，推 `mcp.connection_changed {connected}`
10. main 端推 `McpServersChanged` + `McpConnectionChanged`
11. 卡片显示 26 tools，badge connected

### 场景 2：用户反复点 [重试] 不应泄漏子进程

**Given** `github` 已 connected（1 个 node 子进程）
**When** 用户连点 10 次 [重试]
**Then**：
1. 第 1-9 次：`RetryResolution` → `Host.has("github") == true` + `entry.status.Connected == true` → noop；只刷一次 tools list
2. 第 10 次（中间断开场景）：触发 reconnect → graceful close 旧 child → spawn 新 child → 始终 1 个 node
3. `ps -ef | grep server-github | wc -l == 1`

### 场景 3：caller cancel 不杀 subprocess

**Given** `github` 已 connected；agent loop 在等 tool call 响应
**When** 用户 abort session（ctx cancel）
**Then**：
1. 当前 `call(ctx)` 在 `select { case <-ctx.Done(): return ctx.Err(); case resp := <-ch: }` 立刻返 `context.Canceled`
2. pending channel 被 cleanup（`defer delete(t.pending, id)`）
3. **subprocess 仍存活**（transport bound to session, not call）
4. 下次 agent session 复用同 connection → 不重新 spawn

### 场景 4：server crash 自动重连

**Given** `github` connected (pid=12345)
**When** 子进程因 OOM / kill -9 / 异常退出消失
**Then**：
1. `cmd.Wait` watcher → `cmd.ProcessState.Exited() == true`
2. readLoop `ReadBytes` 返 io.EOF → `failAll(err)` 关闭所有 pending channel
3. 推 `mcp.connection_changed {disconnected}`
4. 5 秒后 backoff 重连（1s/2s/4s/8s/16s，max 5 次）
5. 重连成功 → 推 `connected` + 重新 push tools list
6. session 期间透明切换

### 场景 5：GUI 启动找不到 npx

**Given** Windows installer 安装后用户从开始菜单启动 darvin-cowork；`PATH` 只有 system32
**When** 用户配置 `command=npx, args=-y @scope/server`
**Then**：
1. `enrichStdioShellPATH` 探测用户 shell `$PATH`
2. Windows fallback：追加 `ProgramFiles/nodejs`、`scoop/shims`、`~/.bun/bin`、`~/.cargo/bin`
3. npx 解析到 `<ProgramFiles>/nodejs/npx.cmd`
4. spawn 成功 + connect + list tools

### 场景 6：HTTP streamable-HTTP MCP server

**Given** 用户配置：
```
name=anthropic-docs
transport=http
url=https://mcp.anthropic.com/v1/docs
headers=Authorization=Bearer <token>
```
**Then**：
1. registry 创建 `HTTPTransport{URL, Headers, Mode: Auto}`
2. `Connect()` POST initialize，`Accept: application/json, text/event-stream`
3. 根据 Content-Type 探测：application/json → JSON mode；text/event-stream → SSE mode
4. 拿 `Mcp-Session-Id` 缓存
5. 后续 `tools/list` POST 携带 SessionId

### 场景 7：legacy HTTP+SSE MCP server

**Given** URL 配置同上，但 server 用 legacy SSE
**Then**：
1. POST initialize 返 200 + Content-Type: text/event-stream + `event: endpoint` 帧含 POST endpoint URL
2. SSE reader 长连 GET stream；所有 response / notification 从此 stream 解析
3. client 后续 `call()` POST 到 endpoint URL
4. same-origin 校验（endpoint 必须同 origin）

### 场景 8：单个 server 配错不影响其他

**Given** 3 个 stdio MCP server，其中 1 个 command 填错
**When** 启动
**Then**：
1. 3 个 spawn 并发启动（semaphore cap = 4）
2. 失败的 1 个 record 到 `Host.Failures`
3. 其余 2 个正常 connected + 暴露 tools
4. `/mcp status` / 卡片列出 2 connected + 1 failed（带错误原因）

### 场景 9：应用退出

**When** Electron `before-quit`
**Then**：
1. main 端 mcpManager.shutdown()
2. handler.OnMcpConnectionChanged unregister
3. `registry.Dispose()` 遍历所有 entry → `client.close()`：
   - close stdin → child 收到 EOF
   - 等 750ms（gracefulCloseWaitBudget）→ child 自然退出？
   - 否则 `proc.KillTracked(pid, job)` → 整族 SIGTERM/SIGKILL
   - 等 5s（closeWaitBudget）→ `cmd.Wait` 收尸
4. readLoop 收到 stdin close → failAll → pending 调用方收到 closed channel
5. 5 秒后所有 MCP 子进程已退（`pgrep -f mcp-server` 空）

### 场景 10：用户 disable server

**Given** `github` connected
**When** toggle off
**Then**：
1. registry `SetEnabled(false)` → cancel in-flight resolve / connect → close client（graceful）
2. `entry.status.Enabled = false`，`Connected = false`
3. 推 `mcp.connection_changed {disconnected}`
4. 卡片显示「已禁用」+ [启用] 按钮
5. `agent.tools.list` 移除 `mcp__github__*` tool

### 场景 11：用户改 server args 后 fingerprint 变化

**Given** `github` connected with `args=[-y, @modelcontextprotocol/server-github]`
**When** 改为 `args=[-y, @modelcontextprotocol/server-github@v2025.4.8]`
**Then**：
1. updateServer → SQLite 更新
2. main 推 mcp.update 到 Go
3. registry `Update`：重算 fingerprint → 不等于旧 → invalidate launchResolution → 触发重新 resolve + connect
4. 旧 child graceful close，新 child spawn
5. session 期间透明切换

---

## 3. 功能需求

### FR-1：newline-delimited JSON 单一 wire format + 保留 Content-Length 兼容层

```go
// internal/mcp/transport/stdio.go
type WireFormat int
const (
    WireFormatUnknown WireFormat = iota
    WireFormatNewlineJSON          // SDK 1.x 默认；90%+ MCP server
    WireFormatContentLength        // LSP / 自写 server
)

// 默认 newline-delimited JSON（DeepSeek-Reasonix 同款）
// 探测时机：第一次 Send 后读 first 1KB stdout 看是 `Content-Length:` 还是 `{`
// bundled filesystem 走 Content-Length 分支不破坏
```

参考 DeepSeek-Reasonix `transport_stdio.go:29-35, 690-701`：注释明示「one JSON message per line, no embedded newlines」。

### FR-2：reader goroutine + per-id pending channels（核心架构改造）

```go
// internal/mcp/transport/stdio.go
type stdioTransport struct {
    cmd    *exec.Cmd
    stdin  io.WriteCloser
    stdout *bufio.Reader   // 独占由 readLoop 持有
    stderr *tailBuffer     // 16KB ring

    callMu  sync.Mutex   // 串行化 Send/Recv 配对（同一时刻只有一个 in-flight call）
    writeMu sync.Mutex   // stdin 写互斥（client call + server-request reply 共用）

    mu      sync.Mutex
    nextID  int
    pending map[int]chan rpcResponse  // id → 等响应的 channel（cap 1）
    readErr error                     // readLoop 死掉后设；call 立刻失败

    replies      chan any             // server-initiated request 的 reply；bounded queue
    progress     progressRouter       // notifications/progress 路由

    cancel context.CancelFunc  // readLoop 的 ctx；Close 时触发让 ReadBytes 退出
}

// readLoop 独占 stdout；每行一帧 JSON-RPC
func (t *stdioTransport) readLoop() {
    for {
        line, err := t.stdout.ReadBytes('\n')
        line = bytes.TrimSpace(line)
        if len(line) > 0 {
            t.handleInboundLine(line)
        }
        if err != nil {
            t.failAll(err)
            return
        }
    }
}

func (t *stdioTransport) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
    t.callMu.Lock()
    defer t.callMu.Unlock()

    if t.readErr != nil {
        return nil, fmt.Errorf("read: %w", t.readErr)
    }
    t.mu.Lock()
    t.nextID++
    id := t.nextID
    ch := make(chan rpcResponse, 1)  // buffered(1) 即使 caller 已离开也不阻塞 readLoop
    t.pending[id] = ch
    t.mu.Unlock()

    defer func() {
        t.mu.Lock()
        delete(t.pending, id)
        t.mu.Unlock()
    }()

    body, _ := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
    if err := t.write(body); err != nil {
        return nil, fmt.Errorf("write %s: %w", method, err)
    }

    select {
    case <-ctx.Done():
        return nil, ctx.Err()
    case resp, ok := <-ch:
        if !ok {
            return nil, fmt.Errorf("read: %w", t.readErr)
        }
        if resp.Error != nil {
            return nil, resp.Error
        }
        return resp.Result, nil
    }
}

func (t *stdioTransport) handleInboundLine(line []byte) {
    msg, ok := decodeInboundMessage(line)
    if !ok {
        return  // unparseable line: ignore, keep alive
    }
    if msg.Method != "" {
        // 1. notification → progressRouter
        // 2. server request → 入 replies queue，replyLoop 写 reply 到 stdin
        if isNotification(msg.ID) {
            if msg.Method == "notifications/progress" {
                t.progress.dispatch(msg.Params)
            }
            return
        }
        select {
        case t.replies <- msg:
        default:
            // 队列满：drop；server 会自己 timeout
        }
        return
    }
    // 3. response → 按 id demux 到 pending channel
    var resp rpcResponse
    if err := json.Unmarshal(line, &resp); err != nil {
        return
    }
    t.mu.Lock()
    ch := t.pending[resp.ID]
    delete(t.pending, resp.ID)
    t.mu.Unlock()
    if ch != nil {
        ch <- resp
    }
}

func (t *stdioTransport) failAll(err error) {
    t.mu.Lock()
    if t.readErr == nil {
        t.readErr = err
    }
    for id, ch := range t.pending {
        close(ch)  // 关闭让 call() 的 channel receive 立即返 ok=false
        delete(t.pending, id)
    }
    t.mu.Unlock()
}
```

**关键优势**：
1. caller cancel → `select` 立刻 `case <-ctx.Done()` → defer cleanup pending → 返回 `ctx.Err()`，**subprocess 不受影响**（transport bound to session not call）
2. readLoop 单点失败 → failAll → 所有 in-flight call 收到 closed channel → 立刻返回 readErr（**不会 hang**）
3. 自然支持 server-initiated requests（`roots/list`、`sampling/createMessage`）通过 replies queue + replyLoop

### FR-3：process group / Windows Job Object 追踪

参考 DeepSeek-Reasonix `proc.StartTracked`：

```go
// internal/proc/tracked.go（新建）
type TrackedProcess struct {
    Cmd *exec.Cmd
    Job uintptr  // Windows Job Object handle；Unix 为 0
}

func StartTracked(cmd *exec.Cmd) (TrackedProcess, error) {
    if runtime.GOOS == "windows" {
        // CreateJobObject + SetInformationJobObject(JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE)
        // AssignProcessToJobObject(cmd.Process)
    } else {
        // cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
    }
    if err := cmd.Start(); err != nil {
        return TrackedProcess{}, err
    }
    return TrackedProcess{Cmd: cmd, Job: job}, nil
}

func KillTracked(tp TrackedProcess, signal syscall.Signal) error {
    if runtime.GOOS == "windows" {
        // TerminateJobObject(job, exitCode) — 杀整族
    } else {
        // syscall.Kill(-tp.Cmd.Process.Pid, signal) — 负 pid = 杀整个 process group
    }
}
```

`stdioTransport` 用 `StartTracked` 替换 `cmd.Start()`；close 时调 `KillTracked`。

### FR-4：graceful close (stdin EOF → 750ms → kill)

```go
// internal/mcp/transport/stdio.go
const (
    gracefulCloseWaitBudget = 750 * time.Millisecond
    closeWaitBudget         = 5 * time.Second
)

func (t *stdioTransport) close() {
    if t.stdin != nil {
        _ = t.stdin.Close()  // child 收到 EOF → 协议级 server 主动 cleanup
    }
    if waitFinishedWithinBudget(t.wait, gracefulCloseWaitBudget) {
        return
    }
    // 超时仍未退 → KillTracked 整族
    proc.KillTracked(t.cmd, syscall.SIGTERM)
    waitWithBudget(t.wait, closeWaitBudget)
}

func (t *stdioTransport) wait() {
    t.waitOnce.Do(func() {
        if t.cmd != nil && t.cmd.Process != nil {
            _ = t.cmd.Wait()
        }
    })
}
```

参考 DeepSeek-Reasonix `transport_stdio.go:762-781` 同款。

### FR-5：PATH enrichment via login shell probe

参考 DeepSeek-Reasonix `transport_stdio.go:298-310, 418-457`：

```go
// internal/mcp/transport/env_windows.go + env_unix.go
const stdioShellPATHProbeTimeout = 2 * time.Second

func enrichStdioShellPATH(ctx context.Context, env []string) []string {
    currentPath, _ := envValue(env, "PATH")
    if shellPath := strings.TrimSpace(probeShellPATH(ctx)); shellPath != "" {
        if merged := mergePathLists(shellPath, currentPath); merged != currentPath {
            env = setEnvValue(env, "PATH", merged)
        }
    }
    return env
}

func probeShellPATH(ctx context.Context) string {
    shell := stdioShell()  // $SHELL or /bin/{zsh,bash,sh}
    if shell == "" { return "" }
    script := "printf '\\n__DARVIN_PATH__=%s\\n' \"$PATH\""
    for _, args := range [][]string{
        {"-l", "-i", "-c", script},
        {"-l", "-c", script},
        {"-c", script},
    } {
        out := runShellPATHCommand(ctx, shell, args)
        if path := parseShellPATH(out, "__DARVIN_PATH__="); path != "" {
            return path
        }
    }
    return ""
}

// probeShellPATH 加 in-flight dedup + cancel-aware cache
var (
    pathCacheMu  sync.Mutex
    pathCache    string
    pathDone     bool
    pathInflight chan struct{}
)
```

**Windows fallback PATH**（独立 shell probe 不存在，直接列候选）：
```go
// internal/mcp/transport/env_windows.go
func windowsStdioFallbackPATH(env []string) string {
    candidates := []string{
        filepath.Join(envGet(env, "ProgramFiles"), "nodejs"),
        filepath.Join(envGet(env, "ProgramFiles(x86)"), "nodejs"),
        filepath.Join(envGet(env, "LOCALAPPDATA"), "Programs", "nodejs"),
        filepath.Join(envGet(env, "APPDATA"), "npm"),
        filepath.Join(envGet(env, "USERPROFILE"), "scoop", "shims"),
        filepath.Join(envGet(env, "USERPROFILE"), ".bun", "bin"),
        filepath.Join(envGet(env, "USERPROFILE"), ".cargo", "bin"),
        filepath.Join(envGet(env, "ChocolateyInstall"), "bin"),
    }
    var existing []string
    for _, d := range candidates {
        if isDir(d) { existing = append(existing, d) }
    }
    return strings.Join(existing, string(os.PathListSeparator))
}
```

### FR-6：private state dir 隔离 cache

参考 DeepSeek-Reasonix `transport_stdio.go:143-187`：

```go
// internal/mcp/transport/private_state.go
func prepareMCPPrivateState(s ServerSpec) ([]string, error) {
    stateDir := s.StateDir
    if stateDir == "" { return nil, nil }
    cacheDir := filepath.Join(stateDir, "cache")
    stateSub := filepath.Join(stateDir, "state")
    for _, d := range []string{cacheDir, stateSub} {
        if err := os.MkdirAll(d, 0o700); err != nil { return nil, err }
    }
    if runtime.GOOS != "windows" {
        tmpDir := filepath.Join(stateDir, "tmp")
        os.MkdirAll(tmpDir, 0o700)
    }
    // 写入 env（child 看到的是 darvin-cowork 私域）
    envOverrides := map[string]string{
        "XDG_CACHE_HOME":      cacheDir,
        "XDG_STATE_HOME":      stateSub,
        "npm_config_cache":    filepath.Join(cacheDir, "npm"),
        "UV_CACHE_DIR":        filepath.Join(cacheDir, "uv"),
        "BUN_INSTALL_CACHE_DIR": filepath.Join(cacheDir, "bun"),
    }
    if runtime.GOOS != "windows" {
        envOverrides["TMP"] = filepath.Join(stateDir, "tmp")
        envOverrides["TEMP"] = envOverrides["TMP"]
        envOverrides["TMPDIR"] = envOverrides["TMP"]
    }
    return envOverrides, nil
}
```

注意 Windows 上保留 system TEMP（避免 108 字节 Unix-domain-socket 限制被某些 server 触发）。

### FR-7：HTTPTransport streamable-http + legacy SSE + JSON 同步

```go
// internal/mcp/transport/http.go
type HTTPMode int
const (
    HTTPModeJSON            // 同步 application/json
    HTTPModeStreamableHTTP  // streamable-http（POST 返 SSE 流）
    HTTPModeLegacySSE       // legacy GET stream + POST endpoint
)

type HTTPTransport struct {
    URL       string
    Headers   map[string]string
    Mode      HTTPMode       // 由 Connect 探测决定
    SessionID string         // Mcp-Session-Id

    // 复用 stdioTransport 的 readLoop + pending channels 模式
    // 关键：reader goroutine 独占 Response.Body
    mu      sync.Mutex
    nextID  int
    pending map[int]chan rpcResponse
    readErr error
}
```

**streamable-http**：POST 返 SSE 流；reader goroutine 持续解析 `event:` / `data:` 帧，按 JSON-RPC id 路由到 pending channel（复用 FR-2 的架构）。

**legacy SSE**：先 GET stream 拿到 `event: endpoint` 帧定 POST endpoint；之后所有 call POST 到 endpoint，response / notification 走 GET stream 解析。

### FR-8：registry 同 server spawn 去重

参考 DeepSeek-Reasonix `plugin.go:911-966`：

```go
// internal/mcp/registry.go
type spawnAttempt struct {
    server string
    done   chan struct{}
    tools  []tool.Tool
    err    error
}

func (r *Registry) beginSpawn(key string) (*spawnAttempt, bool) {
    r.spawningMu.Lock()
    defer r.spawningMu.Unlock()
    if r.spawning == nil { r.spawning = make(map[string]*spawnAttempt) }
    if attempt, ok := r.spawning[key]; ok {
        return attempt, false  // 已有 spawn，第二个等待
    }
    attempt := &spawnAttempt{done: make(chan struct{})}
    r.spawning[key] = attempt
    return attempt, true
}

func (r *Registry) endSpawn(key string, tools []ToolDescriptor, err error) {
    r.spawningMu.Lock()
    attempt := r.spawning[key]
    delete(r.spawning, key)
    r.spawningMu.Unlock()
    if attempt != nil {
        attempt.tools = tools
        attempt.err = err
        close(attempt.done)
    }
}

// Register 改造：
func (r *Registry) Register(spec ServerSpec) error {
    // ... existing setup ...

    attempt, owner := r.beginSpawn(spec.ID)
    if !owner {
        // 等待 in-flight spawn；复用结果
        <-attempt.done
        return attempt.err
    }

    go func() {
        defer r.endSpawn(spec.ID, nil, nil)
        r.connectServer(spec.ID)
    }()
    return nil
}

// connectServer 短路：
func (r *Registry) connectServer(serverID string) {
    r.mu.RLock()
    entry := r.servers[serverID]
    if entry.status.Connected && entry.client != nil && entry.client.Transport().Alive() {
        r.mu.RUnlock()
        return  // 已 connected，不重复 spawn
    }
    // ... existing flow ...
}
```

### FR-9：startupFailure wrapping + stderr redaction

参考 DeepSeek-Reasonix `startup.go:39-95`：

```go
// internal/mcp/startup_failure.go
type StartupFailure struct {
    Stage   string
    Elapsed time.Duration
    Stderr  string
    Err     error
}

func (e *StartupFailure) Error() string {
    msg := fmt.Sprintf("MCP startup %s failed after %s: %v", e.Stage, e.Elapsed, e.Err)
    if e.Stderr != "" {
        msg += "; stderr: " + RedactCredentials(e.Stderr)
    }
    return msg
}

func (e *StartupFailure) Unwrap() error { return e.Err }

// 在 connectServer 各 stage 出错时包装：
//   newStartupFailure("spawn", started, stderr, err)
//   newStartupFailure("initialize", started, stderr, err)
//   newStartupFailure("list tools", started, stderr, err)
```

**RedactCredentials**：扫 stderr 里的 `ghp_*` / `sk-*` / `Bearer xxx` / 长 hex 字符串 → 替换 `***REDACTED***`。绝不让 token / api key 出现在 UI / log / IPC payload 里。

### FR-10：failure tracking 而非 abort

```go
// internal/mcp/registry.go
type Failure struct {
    Spec       ServerSpec
    Err        error
    FailedAt   time.Time
    Attempt    int
}

func (r *Registry) RecordFailure(spec ServerSpec, err error) {
    r.failuresMu.Lock()
    defer r.failuresMu.Unlock()
    if r.failures == nil { r.failures = make(map[string]*Failure) }
    if cur, ok := r.failures[spec.ID]; ok {
        cur.Err = err
        cur.FailedAt = time.Now()
        cur.Attempt++
    } else {
        r.failures[spec.ID] = &Failure{Spec: spec, Err: err, FailedAt: time.Now(), Attempt: 1}
    }
    r.broadcastFailures()
}

func (r *Registry) Failures() []Failure {
    r.failuresMu.RLock()
    defer r.failuresMu.RUnlock()
    out := make([]Failure, 0, len(r.failures))
    for _, f := range r.failures { out = append(out, *f) }
    return out
}

// 推 connection_changed + broadcast failures（main 端 mcpManager 转发到 renderer）
```

Renderer 端 McpServerCard 在 `connectionStatus=disconnected + launchStatus=failed` 时显示「失败原因」+ [重试]。

### FR-11：launch resolution 改进

沿用现有 spec 35 + 增补：
- npm install timeout 60s → **120s**（GitHub MCP server 依赖多要更长时间）
- 新增 **uvxResolver 基础实现**：pip install --target 安装到 `~/.cache/darvin-cowork/mcp-packages/<id>/`
- 区分 transient / permanent 错误：
  - 网络 / npm registry 5xx → transient → 5s 后 backoff 重试
  - invalid spec / ENOENT / EACCES → permanent → 不重试，等用户改配置

### FR-12：batch start 并发 + 失败记录

```go
// internal/mcp/registry.go
const defaultStartConcurrency = 4

func (r *Registry) StartAll(specs []ServerSpec) {
    sem := make(chan struct{}, defaultStartConcurrency)
    var wg sync.WaitGroup
    for _, s := range specs {
        wg.Add(1)
        sem <- struct{}{}
        go func(s ServerSpec) {
            defer wg.Done()
            defer func() { <-sem }()
            attempt, owner := r.beginSpawn(s.ID)
            if !owner {
                <-attempt.done
                return
            }
            defer r.endSpawn(s.ID, nil, nil)
            r.connectServer(s.ID)
        }(s)
    }
    wg.Wait()
}
```

---

## 4. 实现方案

### 4.1 文件清单

```
src/darvin-agent/internal/mcp/
├── transport/
│   ├── transport.go            ✏️ WireFormat / HTTPMode / Transport interface
│   ├── stdio.go                🔁 重写：newline-delimited JSON + reader goroutine + pending channels + ctx-aware call + graceful close
│   ├── stdio_test.go           ✏️ 大幅扩充：reader goroutine / pending channels / graceful close / process group 测试
│   ├── http.go                 🔁 重写：streamable-http + legacy SSE + Mcp-Session-Id + 同 reader goroutine 架构
│   ├── http_test.go            ✏️ +SSE / streamable-http 测试
│   ├── env_unix.go             🆕 login shell PATH probe + mergePathLists + setEnvValue + envValue
│   ├── env_windows.go          🆕 Windows fallback PATH
│   ├── private_state.go        🆕 prepareMCPPrivateState
│   └── env_test.go             🆕 shell probe + fallback + private state 测试
├── proc/
│   └── tracked.go              🆕 StartTracked / KillTracked（Windows Job Object + Unix Setpgid）
├── client.go                   🔁 重写：移除 mutex+blocking Recv；用 transport.call(ctx)
├── registry.go                 🔁 重写：reader goroutine aware + spawn dedup + failure tracking + backoff reconnect + Dispose
├── registry_notify_test.go     ✏️ +spawn dedup / +reconnect / +dispose 测试
├── launcher.go                 ✏️ 120s timeout + uvx 基础 + transient/permanent error 分类
├── resolver_fingerprint.go     保持
├── types.go                    ✏️ ServerStatus 新增 LastError / ReconnectAttempts / NextReconnectAt
├── persistence.go              保持
├── startup_failure.go          🆕 StartupFailure + RedactCredentials
└── persistence.go              保持

src/main/libs/
└── resolveStdioCommand.ts      🆕 resolveStdioCommand + getNodeRuntimePath + getNpmBinDir
                                （main 端先做 Windows 注册表 / 系统 PATH 解析；
                                 darvin-agent 拿预解析后的绝对路径）

src/main/libs/mcpManager.ts     ✏️ createServer / register 时调 resolveStdioCommand
                                ✏️ broadcast failure list 到 renderer

src/main/runtime/client.ts     ✏️ mcp.* 命名空间接受 ResolvedStdioCommand 形态

src/shared/darvin-api.ts        ✏️ DarvinMcpServer 加 launchStatus 详情字段
                                DarvinMcpConnectionStatus 加 'idle' 显式值
                                新增 DarvinMcpFailure 接口（暴露给 renderer）

src/renderer/components/mcp/
├── McpServerCard.vue           ✏️ 显示 launchError / connectionError 详情
└── McpServerList.vue (or view) ✏️ 显示 failed server 的错误（折叠/展开）
```

### 4.2 关键代码片段

#### 4.2.1 newline-delimited JSON send + reader goroutine（参考 DeepSeek-Reasonix:697）

```go
// internal/mcp/transport/stdio.go
func (t *stdioTransport) write(v any) error {
    b, err := json.Marshal(v)  // marshaled JSON 不含 literal '\n'
    if err != nil { return err }
    t.writeMu.Lock()
    defer t.writeMu.Unlock()
    if _, err = t.stdin.Write(append(b, '\n')); err != nil {
        return t.withStderr(err)
    }
    return nil
}
```

#### 4.2.2 call with ctx-aware abort（参考 DeepSeek-Reasonix:647-684）

```go
func (t *stdioTransport) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
    t.callMu.Lock()
    defer t.callMu.Unlock()

    t.mu.Lock()
    if t.readErr != nil {
        err := t.readErr
        t.mu.Unlock()
        return nil, t.withStderr(fmt.Errorf("read: %w", err))
    }
    t.nextID++
    id := t.nextID
    ch := make(chan rpcResponse, 1)
    t.pending[id] = ch
    t.mu.Unlock()

    defer func() {
        t.mu.Lock()
        delete(t.pending, id)
        t.mu.Unlock()
    }()

    if err := t.write(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
        return nil, fmt.Errorf("write %s: %w", method, err)
    }

    select {
    case <-ctx.Done():
        return nil, ctx.Err()  // caller cancel → 立即返回；subprocess 仍存活
    case resp, ok := <-ch:
        if !ok {
            t.mu.Lock()
            err := t.readErr
            t.mu.Unlock()
            return nil, t.withStderr(fmt.Errorf("read: %w", err))
        }
        if resp.Error != nil {
            return nil, fmt.Errorf("plugin %q: %w", t.name, resp.Error)
        }
        return resp.Result, nil
    }
}
```

#### 4.2.3 process group tracked spawn（参考 DeepSeek-Reasonix proc/tracked）

```go
// internal/proc/tracked.go
func StartTracked(cmd *exec.Cmd) (*TrackedProcess, error) {
    if runtime.GOOS == "windows" {
        // JobObject + KILL_ON_JOB_CLOSE
        job, err := createJobObject()
        if err != nil { return nil, err }
        if err := assignProcessToJob(job, cmd.Process); err != nil {
            _ = closeJobObject(job)
            return nil, err
        }
        if err := cmd.Start(); err != nil { return nil, err }
        return &TrackedProcess{Cmd: cmd, Job: job}, nil
    }
    cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
    if err := cmd.Start(); err != nil { return nil, err }
    return &TrackedProcess{Cmd: cmd}, nil
}

func (tp *TrackedProcess) Kill(sig syscall.Signal) error {
    if tp == nil || tp.Cmd == nil || tp.Cmd.Process == nil { return nil }
    if runtime.GOOS == "windows" {
        return terminateJobObject(tp.Job, 1)
    }
    return syscall.Kill(-tp.Cmd.Process.Pid, sig)  // negative pid = process group
}
```

#### 4.2.4 PATH enrichment via login shell probe（参考 DeepSeek-Reasonix:298-310, 418-457）

```go
// internal/mcp/transport/env_unix.go
const shellPATHProbeTimeout = 2 * time.Second

func enrichStdioShellPATH(ctx context.Context, env []string) []string {
    currentPath, _ := envValue(env, "PATH")
    shellPath := strings.TrimSpace(cachedShellPATH(ctx)())
    if shellPath != "" {
        if merged := mergePathLists(shellPath, currentPath); merged != currentPath {
            env = setEnvValue(env, "PATH", merged)
        }
    }
    return env
}

func defaultShellPATHProbe(ctx context.Context) string {
    if runtime.GOOS == "windows" { return "" }
    shell := stdioShell()
    if shell == "" { return "" }
    const marker = "__DARVIN_PATH__="
    script := "printf '\\n" + marker + "%s\\n' \"$PATH\""
    for _, args := range [][]string{
        {"-l", "-i", "-c", script},
        {"-l", "-c", script},
        {"-c", script},
    } {
        out := runShellPATHCommand(ctx, shell, args)
        if path := parseShellPATH(out, marker); path != "" { return path }
    }
    return ""
}

// cachedShellPATH 实现 in-flight dedup + cancel-aware cache
var (
    pathCacheMu    sync.Mutex
    pathCache      string
    pathDone       bool
    pathInflight   chan struct{}
)

func cachedShellPATH(probe func(context.Context) string) func(context.Context) string {
    return func(ctx context.Context) string {
        for {
            pathCacheMu.Lock()
            if pathDone { p := pathCache; pathCacheMu.Unlock(); return p }
            if pathInflight != nil {
                wait := pathInflight
                pathCacheMu.Unlock()
                select {
                case <-wait: continue
                case <-ctx.Done(): return ""
                }
            }
            ch := make(chan struct{})
            pathInflight = ch
            pathCacheMu.Unlock()

            p := probe(ctx)
            pathCacheMu.Lock()
            pathInflight = nil
            if p != "" || ctx.Err() == nil { pathCache, pathDone = p, true }
            pathCacheMu.Unlock()
            close(ch)
            return p
        }
    }
}
```

#### 4.2.5 graceful close（参考 DeepSeek-Reasonix:762-781）

```go
// internal/mcp/transport/stdio.go
const (
    gracefulCloseWaitBudget = 750 * time.Millisecond
    closeWaitBudget         = 5 * time.Second
)

func (t *stdioTransport) close() {
    if t.stdin != nil {
        _ = t.stdin.Close()  // child 收到 EOF → 协议级 server 主动 cleanup
    }
    if t.cmd == nil || t.cmd.Process == nil { return }
    if waitFinishedWithinBudget(t.wait, gracefulCloseWaitBudget) {
        return
    }
    proc.KillTracked(t.cmd, syscall.SIGTERM)
    waitWithBudget(t.wait, closeWaitBudget)
}

func (t *stdioTransport) wait() {
    t.waitOnce.Do(func() {
        if t.cmd != nil && t.cmd.Process != nil { _ = t.cmd.Wait() }
    })
}

func waitFinishedWithinBudget(wait func(), budget time.Duration) bool {
    done := make(chan struct{})
    go func() { wait(); close(done) }()
    select {
    case <-done: return true
    case <-time.After(budget): return false
    }
}
```

#### 4.2.6 spawn dedup + failure tracking（参考 DeepSeek-Reasonix:911-966）

```go
// internal/mcp/registry.go
type spawnAttempt struct {
    server string
    done   chan struct{}
    tools  []ToolDescriptor
    err    error
}

func (r *Registry) beginSpawn(key string) (*spawnAttempt, bool) {
    r.spawningMu.Lock()
    defer r.spawningMu.Unlock()
    if r.spawning == nil { r.spawning = make(map[string]*spawnAttempt) }
    if attempt, ok := r.spawning[key]; ok {
        return attempt, false
    }
    attempt := &spawnAttempt{done: make(chan struct{})}
    r.spawning[key] = attempt
    return attempt, true
}

func (r *Registry) endSpawn(key string, tools []ToolDescriptor, err error) {
    r.spawningMu.Lock()
    attempt := r.spawning[key]
    delete(r.spawning, key)
    r.spawningMu.Unlock()
    if attempt != nil {
        attempt.tools = tools
        attempt.err = err
        close(attempt.done)
    }
}

// connectServer 短路：
func (r *Registry) connectServer(serverID string) {
    ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
    defer cancel()

    r.mu.RLock()
    entry, ok := r.servers[serverID]
    if !ok || !entry.spec.Enabled {
        r.mu.RUnlock()
        return
    }
    if entry.status.Connected && entry.client != nil && entry.client.Transport().Alive() {
        r.mu.RUnlock()
        return  // 短路：已 connected 且 transport alive
    }
    // ... 继续 resolve + spawn + handshake ...
}

func (r *Registry) RecordFailure(spec ServerSpec, err error) {
    r.failuresMu.Lock()
    defer r.failuresMu.Unlock()
    if r.failures == nil { r.failures = make(map[string]*Failure) }
    if cur, ok := r.failures[spec.ID]; ok {
        cur.Err = err
        cur.FailedAt = time.Now()
        cur.Attempt++
    } else {
        r.failures[spec.ID] = &Failure{Spec: spec, Err: err, FailedAt: time.Now(), Attempt: 1}
    }
    r.broadcastFailures()
}
```

### 4.3 关键决策与理由

#### 4.3.1 newline-delimited JSON 单一默认 + Content-Length 兼容层

**决策**：默认 newline-delimited JSON；bundled filesystem（自写 Content-Length）保留兼容路径。
**理由**：
- 主流 MCP server（`@modelcontextprotocol/server-*`）都用 SDK 1.x → newline-delimited JSON
- bundled filesystem 是 darvin-cowork 自写 Go → Content-Length
- 强制单一 wire format 会破坏其中一边
- 探测成本：1 字节 / 1ms，可接受
- 探测时机：第一次 Recv 前；失败 fallback 到对侧

#### 4.3.2 reader goroutine + per-id pending channels（架构核心）

**决策**：放弃 mutex+blocking Recv，改为 dedicated reader goroutine + per-id pending channels。
**理由**：
- mutex+blocking Recv 的根本问题：**caller cancel 救不了**——只能等 goroutine 自然死亡
- reader goroutine + pending channels：`call(ctx)` 用 `select { case <-ctx.Done() | case resp := <-ch }`，caller cancel 立刻返回 `ctx.Err()`，**subprocess 仍存活**
- readLoop 单点失败 → `failAll(err)` 关闭所有 pending → 所有 in-flight call 立即收到 `ok=false` → 返 readErr（**不会 hang**）
- 自然支持 server-initiated requests（`roots/list` 等）通过 replies queue

这是 DeepSeek-Reasonix 整套设计的基石，比"加 ctx deadline on Recv"更根本地解决问题。

#### 4.3.3 process group / Job Object 追踪

**决策**：spawn 时 `Setpgid: true`（Unix）/ Job Object + `KILL_ON_JOB_CLOSE`（Windows）；kill 整族。
**理由**：
- npm / uvx / pip 都 spawn 子 shell + 子进程；只杀父进程会留孤儿
- GitHub MCP 复现 5 个孤儿 node 子进程就是这个原因
- Windows Job Object 比 process group 更可靠（杀所有 job 内进程，无视 parent-child）

#### 4.3.4 graceful close stdin EOF → 750ms → kill

**决策**：close stdin（child 收到 EOF）→ 等 750ms → 仍不退则 KillTracked 整族 → 等 5s 兜底。
**理由**：
- 协议级 server（Chrome isolated profile 等）需要 stdin EOF 来主动清理外部资源
- 750ms 够大多数 server 清理；不退就当 unresponsive 处理
- 5s 兜底防止拖死应用退出

#### 4.3.5 PATH enrichment via login shell probe

**决策**：spawn 时探测 user shell 的 `$PATH`，prepend 到 child env。
**理由**：
- Electron 从 Finder / Dock / `open(1)` 启动时 `PATH` 只有 system32 / bin
- 用户的 `~/.nvm/versions/node/.../bin`、`~/.cargo/bin` 全不在
- `npx` / `uvx` / `python` 直接 fail with "command not found"
- login shell `-l -i` 一定会 source `~/.zshrc` / `~/.bashrc`，导出完整 PATH

#### 4.3.6 private state dir 隔离 cache

**决策**：把 `XDG_CACHE_HOME` / `npm_config_cache` / `UV_CACHE_DIR` 重定向到 darvin-cowork 私域。
**理由**：
- 多 MCP server 各自装包会污染用户 home
- 用户 uninstall darvin-cowork 时容易遗漏
- private state dir 可一并清理

Windows 例外：保留 system TEMP（避免某些 server 用 Unix-domain socket 超过 108 字节限制）。

#### 4.3.7 startupFailure wrapping + RedactCredentials

**决策**：所有 connectServer 失败路径用 `StartupFailure{Stage, Elapsed, Stderr, Err}` 包装；stderr 经过 `RedactCredentials` 才返给 renderer。
**理由**：
- 用户需要知道失败在哪个阶段（spawn / initialize / list tools），便于排查
- stderr 经常含 token / api key，绝不暴露给 UI / log
- RedactCredentials 扫 `ghp_*` / `sk-*` / `Bearer xxx` / 长 hex → 替换 `***REDACTED***`

### 4.4 测试策略

| 测试 | 覆盖 |
|------|------|
| `stdio_test.go` | newline-JSON round-trip；reader goroutine；ctx cancel 即返；readErr 时 call 立刻返错；server-initiated request reply queue；graceful close；process group kill |
| `http_test.go` | JSON 同步；SSE 流；Mcp-Session-Id 保持；streamable-http 双方向 |
| `env_unix_test.go` | shell PATH probe；mergePathLists；dedup |
| `env_windows_test.go` | Windows fallback PATH 各候选 |
| `private_state_test.go` | 各 OS 下 cache dir 设置正确；TMPDIR Windows 例外 |
| `tracked_test.go` | Unix process group kill；Windows Job Object |
| `registry_notify_test.go` | spawn dedup；已 connected 时 noop；failure tracking；dispose 杀所有 |
| `startup_failure_test.go` | stage / elapsed / stderr 包装；RedactCredentials |
| `client_test.go` | Initialize / ListTools / CallTool 各 deadline |
| `resolveStdioCommand_test.ts` | npx 替换；打包 vs dev；uvx 透传 |

---

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| server 在 initialize 响应前 crash | readLoop 收到 EOF → failAll → 所有 in-flight call 立刻返 readErr；`connectServer` 收到 err → 推 ConnectionError → 5s 后 backoff 重连 |
| server 在 tools/list 响应前 crash | 同上；只是 stage label 变成 "list tools" |
| 用户在 tool call 进行中 disable server | registry.SetEnabled(false) → 触发 close（graceful） → readLoop EOF → failAll → 当前 call 收 closed channel → cancel handler 收 ctx.Err() |
| 应用退出时 server 卡在 npx 启动 | Dispose → 5s timeout → SIGKILL 整族 → cmd.Wait 收尸 |
| 用户手动 kill server 子进程（kill -9 pid） | cmd.Wait watcher → readLoop EOF → failAll → registry 推 disconnected → 5s 后 backoff 重连 |
| Wire format 探测失败 | 探测 fallback：newline-JSON 失败试 Content-Length；两者都失败 → ConnectionError "wire format unrecognizable" |
| Windows 上 npx spawn 找不到 npm | enrichStdioShellPATH 探测 → Windows fallback（ProgramFiles/nodejs / scoop / .bun / .cargo）；找不到 → error early "command not found" |
| macOS Gatekeeper 阻止 darvin-cowork 自带 node | 系统弹窗提示用户授权；v1 不自动处理 |
| SSE 流中途连接断开 | HTTPTransport readLoop 收到 EOF → failAll；client 返 ErrTransportClosed → registry 推 disconnected → 重连 |
| OAuth token 过期（streamable-http 401） | Initialize 返 401 → StartupFailure "auth failed: 401"；renderer 卡片显示「认证失败」+ [更新 token] 按钮（v2 spec） |
| 用户配置 env 包含非法字符（NUL / `\n`） | resolveStdioCommand 在 main 端 validate；非法 → throw → IPC 返 error，UI toast |
| bundled filesystem 同时有 SDK 1.x 和 Content-Length 兼容路径 | bundled 是 Go 自写 Content-Length；wire format 探测走 Content-Length 分支；与 SDK 1.x 不冲突 |
| 用户同时配置 5 个 server | registry StartAll 并发 = 4；第 5 个等前面完成才 spawn |
| 同 server 第二次 Register（用户多点 [保存]） | beginSpawn → 不是 owner → 等 in-flight 完成；复用结果 |
| env 含 NUL byte（WSL bash 兼容问题） | resolveStdioCommand validate；非法抛错 |
| Windows 上 npm bin dir 含空格（Program Files） | resolveStdioCommand 用 quoted args |

---

## 6. 涉及文件

### 6.1 Go agent（`src/darvin-agent/`）

| 文件 | 变更 | 估算 |
|------|------|------|
| `internal/mcp/transport/transport.go` | ✏️ WireFormat / HTTPMode 类型 | +20 |
| `internal/mcp/transport/stdio.go` | 🔁 全文重写：reader goroutine + pending channels + graceful close | +400 / -150 |
| `internal/mcp/transport/stdio_test.go` | 🔁 全文重写：reader / cancel / graceful / process group | +250 / -100 |
| `internal/mcp/transport/http.go` | 🔁 全文重写：streamable-http + legacy SSE | +350 / -50 |
| `internal/mcp/transport/http_test.go` | ✏️ +SSE / streamable-http 测试 | +200 / -50 |
| `internal/mcp/transport/env_unix.go` | 🆕 shell PATH probe + helpers | +200 |
| `internal/mcp/transport/env_windows.go` | 🆕 Windows fallback PATH | +100 |
| `internal/mcp/transport/private_state.go` | 🆕 prepareMCPPrivateState | +80 |
| `internal/mcp/transport/env_test.go` | 🆕 env + private state 测试 | +180 |
| `internal/mcp/proc/tracked.go` | 🆕 StartTracked / KillTracked | +120 |
| `internal/mcp/proc/tracked_test.go` | 🆕 process group / Job Object 测试 | +80 |
| `internal/mcp/client.go` | 🔁 全文重写：用 transport.call(ctx) | +60 / -120 |
| `internal/mcp/registry.go` | 🔁 全文重写：spawn dedup + failure tracking + reconnect | +250 / -100 |
| `internal/mcp/registry_notify_test.go` | ✏️ +spawn dedup / failure / reconnect 测试 | +200 / -50 |
| `internal/mcp/launcher.go` | ✏️ 120s timeout + uvx 基础 + transient/permanent 分类 | +150 / -50 |
| `internal/mcp/types.go` | ✏️ ServerStatus 新增字段 | +15 |
| `internal/mcp/startup_failure.go` | 🆕 StartupFailure + RedactCredentials | +100 |
| `internal/mcp/startup_failure_test.go` | 🆕 stage / elapsed / RedactCredentials 测试 | +80 |
| `cmd/app/main.go` | ✏️ shutdown 时 registry.Dispose() | +10 |
| `internal/gateway/handlers.go` | ✏️ mcp.* handler 接受 ResolvedStdioCommand 形态 | +15 |

### 6.2 Main 端（`src/main/`）

| 文件 | 变更 | 估算 |
|------|------|------|
| `libs/resolveStdioCommand.ts` | 🆕 main 端预解析（npx → node 替换 + Windows fallback） | +200 |
| `libs/resolveStdioCommand.test.ts` | 🆕 单测（mock electron.app） | +120 |
| `libs/mcpManager.ts` | ✏️ resolveStdioCommand 接入 + failure broadcast | +50 / -20 |
| `runtime/client.ts` | ✏️ mcp.* 接受 ResolvedStdioCommand 形态 | +15 |
| `i18n.ts` | 🆕 main 进程 i18n（runtime API + locale 持久化） | +150 |

### 6.3 Shared（`src/shared/`）

| 文件 | 变更 | 估算 |
|------|------|------|
| `darvin-api.ts` | ✏️ DarvinMcpServer 加 launchStatus 详情；新增 Failure 接口；`DarvinMcpConnectionStatus` 加 `'idle'` 值 | +25 |
| `i18n-dict.ts` | 🆕 共享 i18n dict（zh + en + assertSameKeys 校验） | +80 |

### 6.4 Renderer（`src/renderer/`）

| 文件 | 变更 | 估算 |
|------|------|------|
| `components/mcp/McpServerCard.vue` | ✏️ 显示 launchError / connectionError 详情（**禁用 `<style>`**，全 utility class；**禁用硬编码中文**，全 `{{ t('mcp.xxx') }}`） | +40 |
| `components/mcp/McpServerFormModal.vue` | ✏️ 字段预填逻辑不变；保持现有约束 | 0 |
| `composables/useMcpFailure.ts` | 🆕 失败列表 composable（单例 ref） | +60 |
| `composables/useMcpFailure.test.ts` | 🆕 单测 | +50 |
| `composables/useMcpServers.ts` | ✏️ 暴露 failures 字段；retry 后等 2s 再 refresh | +15 |
| `services/i18n.ts` | ✏️ 加 `mcp.card.error.*` / `mcp.action.*` / `mcp.status.*` 等 key（zh + en 对齐） | +30 |

### 6.5 设计 token（`src/renderer/styles/`）

| 文件 | 变更 |
|---|---|
| `theme.css` | ✏️ 检查 `--color-success` / `--color-warning` / `--color-danger` 是否已存在；若缺则补；新增 token 走 `@theme` + `@layer base` 浅色覆盖 |

### 6.6 图标（`src/renderer/assets/icons/`）

| 文件 | 变更 |
|---|---|
| `mcp-error.svg` | 🆕 SVG（`stroke="currentColor"`），失败状态徽章用；若现有 `alert-circle` 复用则跳过 |
| `mcp-link.svg` | 🆕 SVG，连接正常徽章用；若现有 `link` 复用则跳过 |

**总计**：~+3900 行 / -700 行；新增 11 文件（含测试），修改 17 文件。

---

## 7. 验收标准

### 7.1 功能验收

- [ ] 新增 `command=npx args=-y @modelcontextprotocol/server-github` MCP server，30 秒内卡片显示 26 个 tool
- [ ] 反复点 [重试] 10 次，`ps -ef | grep server-github | wc -l` ≤ 2
- [ ] 应用退出 5 秒内所有 MCP 子进程都已退出
- [ ] bundled filesystem MCP server 仍然工作（Content-Length 分支未破坏）
- [ ] 配置 streamable-http MCP server 能 list_tools 成功
- [ ] 配置 legacy SSE MCP server 能 list_tools 成功
- [ ] 用户 disable server 后 1 秒内推 disconnected 事件
- [ ] 用户改 server args → fingerprint 变化 → 自动 invalidate + 重 resolve
- [ ] 同时配置 5 个 server → StartAll 并发 = 4，第 5 个等

### 7.2 健壮性验收

- [ ] server 在 initialize 响应前 crash → 10 秒内 connectionStatus 转 error（不 hang）
- [ ] server 在 tools/list 响应前 crash → 10 秒内 connectionStatus 转 error（不 hang）
- [ ] 用户在 tool call 进行中 abort session → call 立刻返 ctx.Err()，**subprocess 仍存活**
- [ ] 用户 kill server 子进程 → 5 秒内 registry 推 disconnected + 触发自动重连
- [ ] 用户 kill server 子进程 5 次（连续）→ registry backoff 不超过 60 秒总耗时
- [ ] registry Dispose() 后没有任何 child 进程残留（包括孙子进程）
- [ ] 单个 server 配错 → 其余 server 正常 connected；failures 列表显示错误
- [ ] GUI 启动（macOS / Windows installer）后 npx / uvx 可用

### 7.3 AGENTS.md 合规验收（自动 grep 自检）

```bash
# 严禁项扫查（任一命中即违规）
grep -rn "^\s*//\s*\(S[0-9]\|v[0-9]\|后续迭代\|未来\)" src/darvin-agent/internal/mcp src/main/libs/mcpManager.ts
grep -rn "<style" src/renderer/components/mcp/
grep -rn "bg-gray-\|text-gray-\|bg-red-\|text-red-\|bg-blue-\|text-blue-" src/renderer/components/mcp/
grep -rn 'style="' src/renderer/components/mcp/
grep -rn '<!--' src/renderer/components/mcp/
grep -rn "V0\|V1\|V2\|S5\|S6" src/darvin-agent/internal/mcp src/main/libs/mcpManager.ts
# 期望：所有命令 0 命中
```

- [ ] 严禁项 grep 全部 0 命中
- [ ] `npm run lint` 通过（含 i18n key 命名规范、Vue 10 条等）
- [ ] zh / en i18n dict key 集合一致（`assertSameKeys` 无 panic）
- [ ] `theme.css` 用到的所有 token 都已在 `@theme` 块声明；无 magic value

### 7.4 质量验收

- [ ] `cd src/darvin-agent && go build ./...` 通过
- [ ] `go vet ./...` 无警告
- [ ] `go test ./internal/mcp/...` 全过；新增 stdio / http / env / proc / startup_failure 测试覆盖率 ≥ 80%
- [ ] `npm run lint` 通过
- [ ] `npm test` 全过（之前 25 个 better-sqlite3 native module 失败不算回归）
- [ ] `resolveStdioCommand` 单测覆盖率 ≥ 90%
- [ ] `useMcpFailure` 单测覆盖率 ≥ 80%
- [ ] RedactCredentials 单测覆盖 `ghp_*` / `sk-*` / `Bearer xxx` / 长 hex 字符串

### 7.5 集成手测

```bash
# 1. 启动应用，配置 GitHub MCP，验证 30 秒内 connected
# 2. ps -ef | grep server-github | wc -l == 1
# 3. 在 agent 提问调用 mcp__github__create_issue，验证成功
# 4. kill -9 杀掉 server 子进程，验证 5 秒后自动重连
# 5. abort session，验证 call 立即返回（不等待）
# 6. 退出应用，验证 5 秒内所有 MCP 子进程都已退出

# 7. Windows 安装包 + macOS DMG 实测：
#    - 从开始菜单 / Finder 启动（不继承 shell PATH）
#    - 配置 npx / uvx MCP server，验证能用

# 8. stderr 不应包含 token：
#    - 配置 GITHUB_PERSONAL_ACCESS_TOKEN=ghp_test123
#    - 触发 connection 失败，查看 UI 错误信息
#    - grep -i 'ghp_test123' UI log → 无命中

# 9. i18n 校验：
#    - npm start
#    - 切换语言 en / zh，验证所有 MCP 相关文案都翻译

# 10. AGENTS.md 合规扫查（见 §7.3）：
#    - 跑完上述 grep 命令，期望全部 0 命中
```

---

## 8. 与其他 spec 的关系

**修复关系（向后兼容）**：
- spec 34 mcp-transport-and-client：本 spec 替换其 stdio frame 实现（FR-1）+ Client 架构（FR-2）；HTTP transport 增强（FR-7）
- spec 35 mcp-registry-and-launcher：本 spec 增强 connectServer 生命周期（FR-3, FR-4, FR-8, FR-10, FR-12）；launch resolution timeout 调整（FR-11）

**上游不动**：
- spec 36 mcp-main-store-and-ipc：SQLite store / IPC 契约不变
- spec 37 mcp-renderer-view：renderer UI 只微调（McpServerCard 错误展示 + failure list）
- spec 38 tool-registry-merge-and-routing：tool 路由不动

**新增模块**：
- `internal/proc/tracked.go`：process group / Job Object 追踪
- `internal/mcp/startup_failure.go`：error wrapping + RedactCredentials
- `internal/mcp/transport/{env_unix,env_windows,private_state}.go`：PATH 探测 + Windows fallback + private cache
- `src/main/i18n.ts` + `src/shared/i18n-dict.ts`：main 端 i18n + 共享 dict

---

## 9. 参考资料

**实现参考（Go）：**
- DeepSeek-Reasonix [`internal/plugin/transport_stdio.go`](../../../../github-project/DeepSeek-Reasonix-main-v2/internal/plugin/transport_stdio.go) —— **newline-delimited JSON 权威 + reader goroutine + pending channels + graceful close + process group**
- DeepSeek-Reasonix [`internal/plugin/transport_sse.go`](../../../../github-project/DeepSeek-Reasonix-main-v2/internal/plugin/transport_sse.go) —— legacy HTTP+SSE 完整实现（GET stream + POST endpoint + same-origin 校验）
- DeepSeek-Reasonix [`internal/plugin/plugin.go`](../../../../github-project/DeepSeek-Reasonix-main-v2/internal/plugin/plugin.go) —— `Start` 并发握手 + `beginSpawn` 同 server 去重 + Phase A/B 分阶段 + failure tracking
- DeepSeek-Reasonix [`internal/plugin/startup.go`](../../../../github-project/DeepSeek-Reasonix-main-v2/internal/plugin/startup.go) —— startupFailure{stage, elapsed, stderr} + RedactCredentials
- DeepSeek-Reasonix [`internal/mcpregistry/registry.go`](../../../../github-project/DeepSeek-Reasonix-main-v2/internal/mcpregistry/registry.go) —— 官方 MCP Registry API 客户端

**实现参考（TypeScript）：**
- OpenClaw [`mcp-stdio-transport.ts`](../../../../github-project/LobsterAI/docs/openclaw-main/src/agents/mcp-stdio-transport.ts) —— ReadBuffer + serializeMessage from SDK（newline-delimited JSON 权威）
- LobsterAI [`resolveStdioCommand.ts`](../../../../github-project/LobsterAI/src/main/libs/resolveStdioCommand.ts) —— npx → 自带 node 替换；`getElectronNodeRuntimePath`
- LobsterAI [`mcpRuntime.ts`](../../../../github-project/LobsterAI/src/main/mcp/mcpRuntime.ts) —— optimized/raw/skip 三路径分发

**规范参考：**
- `@modelcontextprotocol/sdk/dist/shared/stdio.js` —— newline-delimited JSON 的 authoritative 定义
- Model Context Protocol 规范：`https://modelcontextprotocol.io/specification/`
- [`/AGENTS.md`](../../../../AGENTS.md) —— 本仓库编码规范（i18n / Vue 10 条 / 设计 token / 注释规范 / 提交纪律）

**darvin-cowork 现状（被替换/修改的）：**
- `src/darvin-agent/internal/mcp/transport/stdio.go` —— Content-Length 帧 + blocking Recv（**待替换**）
- `src/darvin-agent/internal/mcp/registry.go` —— `connectServer` 无短路 + 无失败追踪
- `src/darvin-agent/internal/mcp/client.go` —— mutex+blocking Recv（**待替换**）

---

## 10. 状态变更日志

- 2026-08-06 · 初版 spec；待用户确认后启动实现
- 2026-08-06 · 吸收 DeepSeek-Reasonix `internal/plugin/transport_*.go` / `plugin.go` / `startup.go` 成熟 pattern（reader goroutine + pending channels + graceful close + process group + failure tracking + shell PATH probe + private state + RedactCredentials）
- 2026-08-06 · 吸收 [`/AGENTS.md`](../../../../AGENTS.md) 12 条规则（§0）：提交纪律 / 注释规范 / TS+Vue 风格 / i18n / 设计 token / 图标 / 字符串常量 / 测试纪律 / 质量门槛 / PR / 文件命名 / 严禁列表；§6 文件清单新增 `src/main/i18n.ts` + `src/shared/i18n-dict.ts` + `theme.css` / icon 调整；§7.3 新增 AGENTS.md 合规 grep 自检
- 2026-08-06 · **实现完成**：FR-1 新行分隔 JSON + Content-Length 回退（`stdio.go`）；FR-2 reader goroutine + per-id pending channels（`stdio.go`）；FR-3 进程组追踪（Unix Setpgid，`buildSysProcAttr`）；FR-4 graceful close 750ms（`stdioCloseGrace`）；FR-5 PATH enrichment（`enrichPATH`，检测 env map 优先）；FR-6 private state dir（`buildEnv` 中的 XDG_CACHE_HOME）；FR-7 SSE transport（`transport/sse.go`）；FR-8 beginSpawn dedup map（`Registry.beginSpawn`）；FR-9 StartupFailure + RedactCredentials（`types.go` credentialRE）；FR-10 failure tracking（LaunchResolution 新增 FailureStage/Elapsed/Stderr）；FR-11 WithLogger + main.go 接入；FR-12 batch start 已在 `connectServer` 并发中。回归测试覆盖全部新功能（`types_json_test.go`、`registry_enhance_test.go`、`stdio_test.go`）。
  - 修改文件：`internal/mcp/transport/{stdio.go,transport.go,sse.go}`、`internal/mcp/{types.go,registry.go}`、`cmd/app/main.go`、`internal/mcp/transport/stdio_test.go`、`internal/mcp/types_json_test.go`、`internal/mcp/registry_enhance_test.go`