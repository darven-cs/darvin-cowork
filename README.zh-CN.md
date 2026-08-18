<p align="center">
  <img src="docs/assets/darvin-logo.svg" alt="darvin-cowork" width="120"/>
</p>

<h1 align="center">darvin-cowork</h1>

<p align="center">
  <a href="./README.md">English</a>
  &nbsp;·&nbsp;
  <strong>简体中文</strong>
  &nbsp;·&nbsp;
  <a href="./docs/QUICKSTART.md">快速开始</a>
  &nbsp;·&nbsp;
  <a href="./docs/ARCHITECTURE.md">架构</a>
  &nbsp;·&nbsp;
  <a href="./docs/GUIDE.md">使用指南</a>
  &nbsp;·&nbsp;
  <a href="./docs/IM.md">IM 通道</a>
  &nbsp;·&nbsp;
  <a href="./docs/DEVELOPMENT.md">开发</a>
  &nbsp;·&nbsp;
  <a href="./CHANGELOG.md">更新日志</a>
</p>

<p align="center">
  <a href="https://github.com/darven-cs/darvin-cowork/blob/main/LICENSE"><img src="https://img.shields.io/badge/license-MIT-8b949e?style=flat-square&labelColor=161b22" alt="MIT license"/></a>
  <a href="https://www.electronjs.org/"><img src="https://img.shields.io/badge/Electron-43-47848F?style=flat-square&labelColor=161b22&logo=electron&logoColor=white" alt="Electron"/></a>
  <a href="https://vuejs.org/"><img src="https://img.shields.io/badge/Vue-3.5-42b883?style=flat-square&labelColor=161b22&logo=vuedotjs&logoColor=white" alt="Vue"/></a>
  <a href="https://go.dev/"><img src="https://img.shields.io/badge/Go-1.22-00ADD8?style=flat-square&labelColor=161b22&logo=go&logoColor=white" alt="Go"/></a>
  <a href="https://tailwindcss.com/"><img src="https://img.shields.io/badge/Tailwind-v4-38bdf8?style=flat-square&labelColor=161b22&logo=tailwindcss&logoColor=white" alt="Tailwind"/></a>
  <a href="https://modelcontextprotocol.io/"><img src="https://img.shields.io/badge/MCP-compatible-58a6ff?style=flat-square&labelColor=161b22" alt="MCP"/></a>
</p>

<br/>

<p align="center"><strong>MIT &middot; 本地优先 &middot; 一个桌面 App 集成流式对话、工具调用、MCP、skills、定时任务与 IM 通道。</strong></p>
<p align="center">三进程架构 &mdash; Vue 3 渲染层 + Electron 壳 + Go agent 运行时，源码可读、可扩展。业务逻辑全部下沉到 Go，UI 不持 SQLite。</p>

---

## 这是什么

**darvin-cowork** 是一款本地优先的个人桌面 AI 智能助手。对话壳的形态类似 Claude Desktop，但 agent 运行时由独立的 Go 进程承载，源码完全可读、可扩展。当前是工作原型，重点在于清晰的三进程架构、流式对话、结构化工具执行，以及**永不污染主页面 DOM** 的 AI 产物沙箱预览。

三进程，一次对话：

```
Vue3 渲染层  ──Electron IPC──►  Electron 主进程  ──WebSocket JSON-RPC 2.0──►  darvin-agent (Go)
 (UI / 状态)                       (壳 / 编排)                                 (agent 循环 / 工具 / LLM)
```

- **渲染层**：Vue 3 + Tailwind CSS v4，样式全走 `@theme` 设计 token；zh/en 双语，运行时可切换。
- **主进程**：仅负责 Electron 生命周期 + 启动 Go 子进程；业务逻辑刻意全部下放到 Go。
- **Agent**：`darvin-agent` 持有 agent 循环、工具注册表、上下文压缩、MCP 客户端、skills、记忆与持久化（SQLite + GORM）。

## 功能

- **三进程 + 单二进制 agent**。Go 运行时是静态二进制，由 Electron 拉起并通过 WebSocket JSON-RPC 2.0 通信；UI 永不接触数据库。
- **流式对话 + 真工具调用**。`text_delta` / `thinking_delta`、多轮 tool loop、per-session 并发、可中止、后台完成发系统通知；todo 带证据签收。
- **22 个内置工具**。文件（`read_file` / `write_file` / `edit_file` / `multi_edit` / `move_file` / `list_dir` / `glob` / `grep` / `notebook_edit`）、沙箱化 `shell`（命令白名单）、`web_fetch`、代码搜索与索引（`search` / `code_index` / `delete_symbol`），以及 `todo_write` / `complete_step` / `subagent` / MCP 桥接。
- **MCP 开箱即用**。服务器注册 / 连接 / 测试，传输 `http` / `sse` / `stdio`，fingerprint 解析，按服务器开关。
- **Skills、sub-agents、定时任务**。扫描 / 安装 / 开关 skills；委派 / 并行 / 中止 sub-agents（自带侧栏面板）；cron 风格的后台定时任务。
- **IM 通道子系统**。QQ / 企业微信 / 个人微信连接器，带**真实一次性连通性探测**（结构化检查报告）+实例管理 + 个人微信扫码登录。
- **沙箱化的产物预览**。10 个渲染器（Code / Document / Html / Image / LocalService / Markdown / Mermaid / Svg / Text / Video），全部跑在 `sandbox` iframe 里——AI 原始产物**永不进入主页面 DOM**。
- **Workspaces、专家套件、搜索**。按 workspace 文件隔离、专家套件预设、全文会话搜索。
- **主题 + 国际化**。浅 / 深色主题 + 3 种 accent 色；zh/en 双语字典（约 800 key）运行时可切换；设计 token 走 Tailwind v4 `@theme`。
- **本地优先的持久化**。会话、消息、MCP、skills、定时任务、IM 通道、记忆全部在 Go 侧 SQLite（GORM）——无需联网。

## 安装

### 环境要求

- Node.js **>= 20**（`electron 43.2.0` 兼容性要求）
- Go **>= 1.22**（构建 agent）
- 平台：Windows / macOS / Linux

### 路径 A：从源码构建

```sh
git clone https://github.com/darven-cs/darvin-cowork.git
cd darvin-cowork
npm install
npm start                 # prestart 自动编译 Go agent + 重建 better-sqlite3，然后拉起 Electron
```

在应用内配置 API key：`Settings → Models → 填入 Key（可选 Base URL）→ 保存`。保存后主进程会自动重启 Go 子进程。

或用环境变量：

```sh
export LLM_API_KEY=sk-ant-...
npm start
```

> 仓库内 `src/darvin-agent/config.yaml` 的 `llm.api_key` 刻意留空，**不要**提交真实 key。配置优先级：`LLM_API_KEY` 环境变量 > 用户级 `config.yaml` > 仓库内 `config.yaml`。详见[快速开始 → 配置](./docs/QUICKSTART.md#配置)。

### 路径 B：打包安装包

```sh
npm run build:agent       # 编译 Go agent → bin/darvin-agent-<平台>-<架构>
npm run package           # 生成 unpacked 应用（自动先编 Go）
npm run make              # 打安装包：deb / rpm（Linux）· squirrel / zip（Windows）· zip（macOS）
```

`extraResources` 过滤 `bin/` 仅保留**当前平台**的 Go 二进制——其他平台产物不会被打入安装包。

## 开发与测试

```sh
npm run lint                          # ESLint（src/*.ts/.vue）
npm test                              # Vitest 单元测试
npm run smoke                         # 无头 smoke：spawn 二进制、跑 JSON-RPC 协议栈（不需要 API key）
cd src/darvin-agent && go test ./...  # Go 单元测试
```

dev 流程、Go `fmt` / `vet` / `lint`、本仓库工程规范见 [`docs/DEVELOPMENT.md`](./docs/DEVELOPMENT.md) 与 `CLAUDE.md`。

## 目录结构

```
darvin-cowork/
├─ src/
│  ├─ main/                 Electron 主进程（IPC handler、运行时管理、事件路由）
│  ├─ preload/              contextBridge → window.darvin
│  ├─ renderer/             Vue 3 UI（components / composables / services / styles / views）
│  ├─ shared/darvin-api.ts  IPC 通道、事件、消息类型的单一事实源
│  └─ darvin-agent/         Go agent（gateway / agents / tools / llm / mcp / skills / memory / ...）
├─ bin/                     构建出的 Go 二进制（不入库，仅当前平台）
├─ docs/                    ARCHITECTURE · QUICKSTART · GUIDE · IM · DEVELOPMENT + pkg-document/
├─ specs/                   设计文档（每个 feature 一个目录）
├─ scripts/                 build-go.js · smoke.sh
├─ forge.config.ts          Electron Forge makers（squirrel / zip / deb / rpm）+ Vite 插件 + Fuses
├─ package.json
└─ README.md
```

Go 运行时布局（`internal/` 各包一句话）：

| 包 | 职责 |
|---|---|
| `agents` | per-session 控制器、agent 循环、dispatcher、executor、store、上下文引擎、权限 |
| `config` | viper 配置加载 |
| `database` | GORM + SQLite、schema 迁移 |
| `gateway` | WebSocket JSON-RPC 服务端、per-session handler |
| `harness` | agent harness 抽象、能力注册 |
| `im` | QQ / 企业微信 / 个人微信连接器，按实例生命周期 |
| `llm` | providers（`anthropic` / `openai` / `gemini`）、model registry、streaming protocol |
| `logger` | zap + lumberjack 日志轮转 |
| `mcp` | MCP 客户端（registry / launcher / http / sse / stdio 传输） |
| `memory` | 轻量记忆管理器 |
| `runtime` | 组装 gateway + harness + providers + workspace bootstrap |
| `scheduledtask` | cron 风格定时任务引擎 |
| `sessionruntime` | per-session agent 运行时、生命周期容器 |
| `skills` | scanner、loader、frontmatter、registry、runner |
| `subagent` | 子代理编排 |
| `todos` | todo 存储 |
| `tools` | 内置工具集（fs / shell / search / web_fetch / code_index / sandbox / todo / subagent / notebook_edit / mcp 桥接） |

## 文档

- [快速开始](./docs/QUICKSTART.md) — 安装、配置、构建、排障。
- [架构](./docs/ARCHITECTURE.md) — 三进程架构、Go 数据所有权、IPC 契约。
- [使用指南](./docs/GUIDE.md) — 会话、工具、todo、sub-agents、MCP、skills、memory、artifact 沙箱、专家套件、定时任务、IM 总览、设置。
- [IM 通道](./docs/IM.md) — QQ / 企业微信 / 个人微信连接器、真实连通性探测、实例管理。
- [开发](./docs/DEVELOPMENT.md) — dev 流程、lint / test / smoke、Go `fmt` / `vet` / `lint`、代码规范指针。
- [更新日志](./CHANGELOG.md) — 版本发布说明。
- [`docs/pkg-document/`](./docs/pkg-document/) — 第三方库参考（viper · zap · 企业微信 AI Bot 协议 · 个人微信 iLink 协议）。

Feature 设计 spec 在 [`specs/`](./specs/)，每个 feature 一个目录。

## 安全说明

- `.env`、`*.db`、`*.log`、构建产物均已 git-ignore。本地 `src/darvin-agent/.env` 里的真实 API key **不会**被提交——但切记不要 `git add -f` 强加。
- push 前建议装 pre-commit 密钥扫描（如 [gitleaks](https://github.com/gitleaks/gitleaks)）。
- IM 通道使用各自协议专有鉴权（QQ app access token、企业微信 botId+secret、个人微信 iLink bot token）。一次性 `imTest` 探测对 UI 暴露是安全的，不会持久化任何会话状态。
- 发现安全问题请开 [GitHub issue](https://github.com/darven-cs/darvin-cowork/issues)（或直接联系作者）；**不要**在公开 issue 里贴密钥。

## License

[MIT](LICENSE) &copy; 2026 [darven](https://github.com/darven-cs)