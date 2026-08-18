<a href="./ARCHITECTURE.md">English</a>
&nbsp;·&nbsp;
<strong>简体中文</strong>
&nbsp;·&nbsp;
<a href="../README.zh-CN.md">README</a>

# 架构

> 三进程，一次对话：Vue 3 渲染层、Electron 壳、Go agent 运行时。

## 概览

darvin-cowork 拆成三个进程，通过清晰的边界相互通信。渲染层是唯一与用户打交道的进程。主进程只负责拉起 Go 子进程、转发 JSON-RPC 流量。Go agent 是所有业务状态的所在地。

```
┌────────────────────────┐    Electron IPC（ipcMain.handle / ipcRenderer.invoke）
│   渲染层（Vue 3）      │ ◄───────────────────────────────────────────────────┐
│   src/renderer/        │                                                     │
│   src/preload/         │ ─── contextBridge 暴露 window.darvin（强类型） ──► │
└────────────────────────┘                                                     │
                                                                                ▼
┌───────────────────────────────────────────────────────────────────────────────┐
│   主进程（Electron）                                                          │
│   src/main/                                                                   │
│   - BrowserWindow + DevTools + remote-debugging-port=9222（仅 dev）           │
│   - RuntimeMgr：spawn darvin-agent-<平台>-<架构>，从 stdout 抓 <port>         │
│     （5s 超时）                                                               │
│   - AgentClient：WebSocket JSON-RPC 2.0 客户端                                │
│   - EventRouter：agent.event → webContents.send（纯转发）                     │
│   - ~70 个 ipcMain.handle 通道（session / message / skill / MCP / workspace   │
│     / LLM / prefs / locale / runtime status / attachment / permission / ...） │
└───────────────────────────────────────────────────────────────────────────────┘
                                                                                │
                                                                                ▼  WebSocket
                                                                       ws://localhost:<port>/ws
                                                                                │
┌───────────────────────────────────────────────────────────────────────────────┐
│   darvin-agent（Go）                                                          │
│   src/darvin-agent/                                                           │
│   - Gateway：WS JSON-RPC 服务端、per-session handler 分发                     │
│   - SessionRuntime：per-session agent 循环、生命周期容器                      │
│   - Agents：dispatcher / executor / agent loop / ctx engine / permissions     │
│   - LLM：anthropic / openai / gemini providers、流式协议                      │
│   - Tools：内置工具（fs / shell / search / web_fetch / code_index / sandbox   │
│     / todo / subagent / notebook_edit / mcp bridge）                          │
│   - MCP：client（http / sse / stdio）+ launcher + registry                    │
│   - Skills：scanner / loader / frontmatter / registry / runner                │
│   - IM：QQ / 企业微信 / 个人微信连接器 + Prober（一次性连通性探测）           │
│   - ScheduledTask / SubAgent / Memory / Todos                                │
│   - 持久化：GORM + SQLite（sessions.db、MCP、skills、定时任务、IM 通道、记忆）│
└───────────────────────────────────────────────────────────────────────────────┘
```

## 为什么是三进程

- **渲染层保持简单**。只持有 UI 状态，不持 SQLite、不碰工作区外文件系统、不持有密钥。
- **主进程保持薄**。~70 个 `ipcMain.handle` 通道把强类型 JSON-RPC 代理到 Go。加一个新 IPC 通道，只需在 `src/shared/darvin-api.ts` 里改两处。
- **Go 持有所有关键逻辑**。Agent 循环、工具注册表、上下文压缩、MCP 客户端、skills、记忆、持久化全在一个进程里——能挂调试器、能跑 `go test`、能 grep。`CGO_ENABLED=0` 的单静态二进制让桌面端跨平台毫无负担。

## Go 运行时布局

`src/darvin-agent/` 是一个 Go 模块（`backend`），单装配入口（`cmd/app/main.go` → `internal/runtime`），15+ 个 `internal/` 包。

| 包 | 职责 |
|---|---|
| `cmd/app` | 15 行入口：`os.Exit(runApp(os.Args[1:]))`；`runApp` 是 var，测试可替换 |
| `internal/runtime` | `Build(ctx, Options) (*Runtime, error)` 加载 config + DB + LLM provider、装配 agent factory、bootstrap skills / MCP、起 gateway、bootstrap active session；`Run(args)` 接 SIGINT / SIGTERM；`Shutdown(ctx)` 关闭 server / harness / SQLite |
| `internal/gateway` | WebSocket 服务端、JSON-RPC 封装、handler 分发、per-session manager、per-session event ledger |
| `internal/sessionruntime` | per-session agent 运行时。`AgentFactory` 装配 Agent + Harness + Loop + DeltaHook + Subagents；`Loop` 持有按 session 串行的 turn 队列；`SessionRuntime` 是生命周期容器（Close 链 Subagents → DeltaHook → Loop） |
| `internal/agents` | `Agent.Prompt / Run / Abort / Subscribe`；dispatcher enqueue + `runMsgID`；子包 `queue / session / store / executor / perm / ctxengine / msgid / protocol / runtime / usage` |
| `internal/llm` | 流式协议 + `anthropic` / `openai` / `gemini` providers + model registry；events 与 errors 单独文件 |
| `internal/tools` | 内置工具 + permission registry + MCP 桥接；exclusions 文件白名单 |
| `internal/skills` | scanner / loader / frontmatter / registry / plugin / runner / wire；安装走 `skillInstall`（main 端）+ `wire`（Go 端） |
| `internal/mcp` | client / launcher / registry / transport（http + sse + stdio）/ resolver fingerprint / persistence |
| `internal/scheduledtask` | cron 风格定时任务引擎，含每任务的运行历史 |
| `internal/subagent` | 子代理编排（委派 / 并行 / 中止 / 列表 / 读取结果） |
| `internal/memory` | 轻量记忆管理器 |
| `internal/database` | GORM + `glebarez/sqlite` 单例 `globalDB`；`internal/agents/store/` 持有 session / message / app_state / imported_file / memory |
| `internal/config` | viper 配置加载 |
| `internal/logger` | zap + lumberjack 日志轮转 |
| `internal/harness` | 前端 / CLI / 嵌入运行器、能力注册 |
| `internal/jsonschema` | schema 规范化与校验 |

跨包规则：

- `agents/` 不可 import 能力包（`llm / tools / skills / mcp`）；由 `Makefile` 的 `lint-agents-boundaries` 强制。
- `internal/im/` 的接口放在消费侧契约包（`contract.go`），连接器实现放在 `internal/im/<channel>/`。

## IPC 契约——单一事实源

所有 IPC 通道、推送事件、流事件、消息类型都集中在 `src/shared/darvin-api.ts`：

- `DarvinApi`——请求/响应接口（~70 个方法；session / message / skill / MCP / workspace / LLM / locale / prefs / attachment / permission / artifact）。
- `DarvinPushEvent`——推送事件常量（`SessionsChanged / ActiveSessionChanged / SessionEvent / WorkspaceChanged / SkillsChanged / McpServersChanged / McpConnectionChanged`）。
- `DarvinEvent`——discriminated union（`text_delta / thinking_delta / tool_start / tool_end / done / error / agent_end / compaction / context_usage / permission_request / artifact`）。
- `DarvinMessage`——discriminated union（`user / assistant / tool_use / tool_result / system`）。
- 帮助：`parseDarvinEvent`、`assertNever`。
- 各域记录 shape（`DarvinIMInstance`、`DarvinIMStatus` 等）。

约定：

- 一源三处。main / preload / renderer 都从 `darvin-api.ts` 导入。组件内禁止 `any`。
- 新增 IPC 通道三处改：`darvin-api.ts` 里 `DarvinApi` → main 里 `ipcMain.handle` → preload 里 `window.darvin` 方法。
- wire 投影类型用 `<Domain>Wire` 后缀，区分内部业务类型与 IPC 协议类型。

## 主进程

`src/main/index.ts`（~67 KB）是唯一承载业务知识的主进程文件：

- 处理 `electron-squirrel-startup` 短路（Windows 安装）。
- `createWindow()` 构造 `BrowserWindow`；preload 指向 Vite 产物。
- 开发态：`loadURL(MAIN_WINDOW_VITE_DEV_SERVER_URL)`、默认开 DevTools、`remote-debugging-port=9222` + `remote-allow-origins=*`（方便 `electron-cdp` 驱动窗口做 E2E）。
- ~70 个 `ipcMain.handle` 通道（完整列表见 `src/shared/darvin-api.ts`）。
- `RuntimeMgr`（子进程生命周期）+ `AgentClient`（WS JSON-RPC 客户端）+ `EventRouter`（纯转发）。
- `window-all-closed` 非 macOS 退出；`activate` 时若无窗口则重建。
- merge-databases 重构后，main 端不再持有 SQLite；Go 离线时，渲染层有最近视图的进程内 in-memory 缓存兜底。

`src/main/runtime/manager.ts`：

- `resolveAgentBinaryPath()`——按 `app.isPackaged` 选 `process.resourcesPath/bin/...` 或 `__dirname` 回溯三级的开发路径；缺失时打 warning 不抛错。
- `start(workspaceRoot?)`——`spawn(bin)`，从 stdout 抓 `<port>…</port>`（5 s 超时），SIGTERM + 4 s 宽限期停止。
- 暴露 `pid() / port() / isResolved() / resolveAgentConfigPath()`（仅 dev）。

`src/main/runtime/client.ts`：

- `class AgentClient`——WebSocket JSON-RPC 2.0 客户端，连 `ws://localhost:{port}/ws`。
- 完整方法面：`connect / disconnect / request / prompt / abort / invokeSkill / subscribeEvents / listSessions / getMessages` + 命名空间 `skills.{list,setEnabled,bootstrap,onChanged}` / `mcp.{list,register,update,unregister,setEnabled,test,retryResolution,bootstrap,onConnectionChanged,onResolutionChanged}` / `tools.list`。
- 帮助：`parseDarvinEvent`、`BACKEND_DEFAULT_SESSION_ID`。

## preload

`src/preload/index.ts` 通过 `contextBridge.exposeInMainWorld('darvin', api)` 暴露强类型 API。渲染层从不 import `electron`；一切通过 `window.darvin`。

## 渲染层

Vue 3 + Vite（`root: 'src/renderer'`，`base: './'` 用于生产相对路径）。要点：

- **Vue 3 SFC + `<script setup lang="ts">` + Composition API**。禁止 mixins、class-based 组件、Options API。
- **Tailwind CSS v4** via `@tailwindcss/vite`。设计 token 在 `src/renderer/styles/theme.css` 的 `@theme` 块；组件用 utility class（`bg-surface` / `text-text-muted` / `rounded-md`）；禁 `<style>` 块、magic value。
- **图标系统**——约 70 个 SVG 自动 glob（`src/renderer/assets/icons/`）。`Icon` 组件接 `name` + `:size`，全部 `stroke="currentColor"`。
- **i18n**——平铺 `dictZh` / `dictEn`，`assertSameKeys` 强制 key 对齐。仅 renderer；main 端保持英文。
- **Artifact 渲染器**——`src/renderer/services/artifact-renderer/` 按产物类型在 `sandbox` iframe 里渲染（Code / Document / Html / Image / LocalService / Markdown / Mermaid / Svg / Text / Video）。AI 原始产物**永不**进入主页面 DOM。

## IM 子系统（摘要）

`src/darvin-agent/internal/im/`：

- `qq/`——QQ 官方机器人，app access token + Discord 风格 WS 网关。
- `wecom/`——企业微信 AI Bot，WS 到 `wss://openws.work.weixin.qq.com`，`aibot_subscribe` 鉴权。
- `weixin/`——个人微信 iLink bot，HTTP 网关，扫码登录 + 长轮询 `getupdates`。
- `manager.go`——统一生命周期，把入站消息派发到绑定的 darvin session，把出站回给 peer。
- `Prober`——各连接器实现 `Probe(ctx) ([]Check, error)`，`imTest` RPC 返回结构化检查报告而非虚假 ok。

完整设计见 [`docs/IM.md`](./IM.md)。

## 构建、打包、跨平台

- `scripts/build-go.js`——输出 `<repo>/bin/darvin-agent-<平台>-<架构><.exe?>`，`CGO_ENABLED=0`。
- `npm run build:agent`——只编 Go。
- `npm run package`——生成 unpacked Electron 应用（自动先 `build:agent`）。
- `npm run make`——打安装包：`squirrel`（Windows）/ `zip`（macOS）/ `deb`（Linux）/ `rpm`（Linux）。
- `forge.config.ts`——`extraResources.filter` 仅保留**当前平台**的 Go 二进制，避免开发机 `bin/` 里跨平台产物打进去。

## 故意不在主进程的事

- **SQLite**——属于 Go（`internal/database` 的 `globalDB`）。main 只是代理。
- **LLM keys**——由 Go viper 从 `LLM_API_KEY` env / 用户 `config.yaml` / 仓库 `config.yaml` 读取；main 端永远不见明文 key。
- **工具执行**——所有工具调用都在 Go 进程跑，渲染层无法绕过权限 / 沙箱。
- **IM 会话**——各连接器在 Go 端，渲染层只通过 `window.darvin.im*` IPC 访问。