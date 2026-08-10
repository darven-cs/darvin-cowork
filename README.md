# darvin-cowork

> A local desktop AI assistant prototype. Electron + Vue 3 front-end, Go-written `darvin-agent` running as a child process as the agent runtime.
> 一个本地桌面 AI 智能助手原型:Electron + Vue3 前端,Go 编写的 `darvin-agent` 作为主进程子进程承载完整的 agent 循环(LLM 调用 / 工具执行 / 上下文压缩 / 持久化)。

[English](#english) · [中文](#中文) · [Configuration](#configuration) · [Troubleshooting](#troubleshooting) · [License](#license)

---

## English

### What is it

**darvin-cowork** is a personal, local-first desktop AI assistant — a chat shell in the spirit of Claude Desktop, but with the agent runtime owned by a Go process you can read and extend. It is a prototype, not a finished product: the focus is a clean three-process architecture, streaming conversations, structured tool execution, and AI-artifact previews that never touch the main page DOM.

Three processes, one conversation:

```
Vue 3 renderer  ──Electron IPC──►  Electron main  ──WebSocket JSON-RPC 2.0──►  darvin-agent (Go)
   (UI / state)                      (shell / orchestration)                  (agent loop / tools / LLM)
```

- **Renderer**: Vue 3 + Tailwind CSS v4, all styles via `@theme` design tokens, zh/en i18n.
- **Main**: Electron lifecycle + spawning the Go child process; business logic is deliberately pushed down to Go.
- **Agent**: `darvin-agent` owns the agent loop, tool registry, context compaction, MCP clients, skills, memory and persistence (SQLite via GORM).

### Features (as-built)

| Domain | Capability |
|---|---|
| Sessions | Create / switch / rename / delete / search, lazy-create draft mode |
| Conversation | Streaming `text_delta` / `thinking_delta`, multi-turn tool loop, abort, **per-session concurrency**, background notifications |
| Context | Token-budget estimation, summary compaction, tool-result truncation |
| Built-in tools (21) | `read_file` / `write_file` / `edit_file` / `multi_edit` / `move_file` / `list_dir` / `glob` / `grep` / `notebook_edit`, sandboxed `shell` (command whitelist), `web_fetch`, code search & indexing (`search` / `code_index` / `delete_symbol`) |
| Task tracking | `todo_write` (two-level checklist) + `complete_step` (evidence-backed sign-off) + a **TodoPanel** in the artifact side panel |
| Sub-agents | Delegate / parallel / abort / list / read-result + a sub-agent panel (run list & details) |
| MCP | Server register / connect / test, transports `http` / `sse` / `stdio`, fingerprint resolution, per-server enable |
| Skills | Scan / install / toggle / plugins |
| Memory | Lightweight memory manager |
| Artifact preview | 10 sandboxed renderers (Code / Document / Html / Image / LocalService / Markdown / Mermaid / Svg / Text / Video) via `sandbox` iframes — AI output never enters the main page DOM |
| UI | 8 views (Home / Chat / Expert Suite / Settings / Skills / MCP / Search / Placeholder), light/dark theme + 3 accent colors, model picker |
| i18n | zh / en dictionaries (~800 keys), runtime switchable |
| Persistence | Go-side SQLite (GORM); user-level `config.yaml` |

### Requirements

- Node.js >= 20
- Go >= 1.22
- Platform: Windows / macOS / Linux (Electron Forge makers: squirrel / zip / deb / rpm)

### Quick start

```bash
npm install
npm start            # prestart automatically builds the Go agent, then launches Electron
```

Configure an API key **inside the app**: `Settings → Models → paste key (optional Base URL) → save`. Saving restarts the Go child process automatically.

Or use an environment variable:

```bash
export LLM_API_KEY=sk-ant-...
npm start
```

> Do **not** commit a real key into `src/darvin-agent/config.yaml` — it ships with an empty `llm.api_key` on purpose. Precedence: `LLM_API_KEY` env var > user-level `config.yaml` > repo `config.yaml`. See [Configuration](#configuration).

### Build & package

```bash
npm run build:agent   # compile Go agent → bin/darvin-agent-<platform>-<arch>
npm run package       # unpacked app (auto-builds Go first)
npm run make          # installers (deb / rpm / squirrel / zip)
```

### Development & tests

```bash
npm run lint                        # ESLint over src/*.ts/.vue
npm test                            # Vitest unit tests
npm run smoke                       # headless smoke test: spawns the binary, exercises the JSON-RPC protocol (no API key needed)
cd src/darvin-agent && go test ./...   # Go unit tests
```

See `src/darvin-agent/Makefile` for Go `fmt` / `vet` / `lint` / `check` targets.

### Project layout

```
src/
├─ main/                  Electron main process (IPC handlers, runtime manager, event router)
├─ preload/               contextBridge → window.darvin
├─ renderer/              Vue 3 UI (components / composables / services / styles / views)
├─ shared/darvin-api.ts   single source of truth for IPC channels, events & message types
└─ darvin-agent/          Go agent (gateway / agents / tools / llm / mcp / skills / memory / ...)
bin/                      built Go binaries (git-ignored)
scripts/                  build-go.js · smoke.sh
specs/                    design docs (one dir per feature)
docs/                     系统架构.md · 源码现状.md
```

### More docs

- Architecture (in Chinese): [`docs/系统架构.md`](docs/系统架构.md)
- Legacy as-built snapshot (dated 2026-08-01, partially outdated): [`docs/源码现状.md`](docs/源码现状.md)
- Feature design specs: [`specs/`](specs/)

### Security

- `.env`, `*.db`, `*.log` and built binaries are git-ignored. A real API key in a local `src/darvin-agent/.env` will **not** be committed — but never `git add -f` it.
- Consider a pre-commit secret scanner (e.g. [gitleaks](https://github.com/gitleaks/gitleaks)) before pushing.
- To report a security issue, open an issue (or contact the author directly); do not post secrets in a public issue.

---

## 中文

### 这是什么

**darvin-cowork** 是一款**本地优先**的个人桌面 AI 智能助手——对话壳形态类似 Claude Desktop,但 agent 运行时由独立的 Go 进程承载,源码完全可读、可扩展。它是一个原型而非成品,重点在于清晰的三进程架构、流式对话、结构化工具执行,以及**永不污染主页面 DOM** 的 AI 产物沙箱预览。

三进程,一次对话:

```
Vue3 渲染层  ──Electron IPC──►  Electron 主进程  ──WebSocket JSON-RPC 2.0──►  darvin-agent (Go)
 (UI / 状态)                       (壳 / 编排)                                 (agent 循环 / 工具 / LLM)
```

- **渲染层**:Vue3 + Tailwind CSS v4,样式全走 `@theme` 设计 token,zh/en 双语。
- **主进程**:仅负责 Electron 生命周期 + 启动 Go 子进程;业务逻辑刻意全部下放到 Go。
- **Agent**:`darvin-agent` 持有 agent 循环、工具注册表、上下文压缩、MCP 客户端、skills、记忆与持久化(SQLite + GORM)。

### 已实现功能(以源码为准)

| 域 | 能力 |
|---|---|
| 会话 | 创建 / 切换 / 重命名 / 删除 / 搜索,懒创建 draftMode |
| 对话 | 流式 `text_delta` / `thinking_delta`、多轮 tool loop、abort、**per-session 并发**,后台会话完成发系统通知 |
| 上下文 | token 预算估算、摘要压缩、工具结果截断 |
| 内置工具(21 个) | `read_file` / `write_file` / `edit_file` / `multi_edit` / `move_file` / `list_dir` / `glob` / `grep` / `notebook_edit`、沙箱 `shell`(命令白名单)、`web_fetch`、代码搜索与索引(`search` / `code_index` / `delete_symbol`) |
| 任务清单 | `todo_write`(两级清单)+ `complete_step`(带证据签收)+ artifact 侧栏的 **TodoPanel** |
| 子代理 | 委派 / 并行 / 中止 / 列表 / 读取结果 + 子代理面板(运行列表与详情) |
| MCP | 服务器注册 / 连接 / 测试,传输 `http` / `sse` / `stdio`,fingerprint 解析,按服务器开关 |
| Skills | 扫描 / 安装 / 开关 / 插件 |
| 记忆 | 轻量记忆管理器 |
| 产物预览 | 10 个沙箱渲染器(Code / Document / Html / Image / LocalService / Markdown / Mermaid / Svg / Text / Video),经 `sandbox` iframe 渲染——AI 原始产物**不进主页面 DOM** |
| UI | 8 个视图(首页 / 对话 / 专家套件 / 设置 / Skills / MCP / 搜索 / 占位),light/dark 主题 + 3 种 accent,模型选择器 |
| i18n | zh / en 双语字典(约 800 key),运行时可切换 |
| 持久化 | Go 侧 SQLite(GORM);用户级 `config.yaml` |

### 环境要求

- Node.js >= 20
- Go >= 1.22
- 平台:Windows / macOS / Linux(Electron Forge makers:squirrel / zip / deb / rpm)

### 快速开始

```bash
npm install
npm start            # prestart 自动编译 Go agent,然后拉起 Electron
```

在应用内配置 API key:`Settings → Models → 填入 Key(可选 Base URL)→ 保存`。保存后主进程会自动重启 Go 子进程。

或用环境变量:

```bash
export LLM_API_KEY=sk-ant-...
npm start
```

> 仓库内 `src/darvin-agent/config.yaml` 的 `llm.api_key` 刻意留空,**不要**提交真实 key。配置优先级:`LLM_API_KEY` 环境变量 > 用户级 `config.yaml` > 仓库内 `config.yaml`。详见下方[配置](#配置)。

### 构建与打包

```bash
npm run build:agent   # 编译 Go agent → bin/darvin-agent-<platform>-<arch>
npm run package       # 生成 unpacked 应用(自动先编 Go)
npm run make          # 打安装包(deb / rpm / squirrel / zip)
```

### 开发与测试

```bash
npm run lint                            # ESLint 检查 src/*.ts/.vue
npm test                                # Vitest 单元测试
npm run smoke                           # 无头 smoke:spawn 二进制,跑 JSON-RPC 协议栈(不需要 API key)
cd src/darvin-agent && go test ./...    # Go 单元测试
```

Go 侧 `fmt` / `vet` / `lint` / `check` 目标见 `src/darvin-agent/Makefile`。

### 目录结构

```
src/
├─ main/                  Electron 主进程(IPC handler、运行时管理、事件路由)
├─ preload/               contextBridge → window.darvin
├─ renderer/              Vue3 UI(components / composables / services / styles / views)
├─ shared/darvin-api.ts   IPC 通道、事件、消息类型的单一事实源
└─ darvin-agent/          Go agent(gateway / agents / tools / llm / mcp / skills / memory / ...)
bin/                      构建出的 Go 二进制(不入库)
scripts/                  build-go.js · smoke.sh
specs/                    设计文档(每个 feature 一个目录)
docs/                     系统架构.md · 源码现状.md
```

### 更多文档

- 架构设计:[`docs/系统架构.md`](docs/系统架构.md)
- 历史 as-built 快照(2026-08-01,部分内容已过时):[`docs/源码现状.md`](docs/源码现状.md)
- Feature 设计文档:[`specs/`](specs/)

### 安全说明

- `.env`、`*.db`、`*.log`、构建产物均已 git-ignore。本地 `src/darvin-agent/.env` 里的真实 API key **不会**被提交——但切记不要 `git add -f` 强加。
- push 前建议装 pre-commit 密钥扫描(如 [gitleaks](https://github.com/gitleaks/gitleaks))。
- 发现安全问题请开 issue(或直接联系作者);**不要**在公开 issue 里贴密钥。

---

## Configuration

用户级配置文件路径(Go 侧 `config.UserConfigPath()` 与 Electron 侧 `app.getPath('userData')` 保持一致):

| 平台 | 路径 |
|------|------|
| Linux | `~/.config/darvin-cowork/config.yaml` |
| macOS | `~/Library/Application Support/darvin-cowork/config.yaml` |
| Windows | `%APPDATA%\darvin-cowork\config.yaml` |

清除 API key 重来:删除上面的用户级配置文件即可(下次启动回落到仓库内 `config.yaml` / 环境变量)。

## Troubleshooting

**常见问题**

**配置错了 key,想清空重来**
删除用户级 `config.yaml`(见上表),下次启动自动回落。

**想清空历史会话**
会话与消息存在 Go 侧 SQLite(`sessions.db`)。停掉应用后删除该文件,下次启动自动 AutoMigrate 重建空库。

**`npm start` 报找不到 darvin-agent 二进制**
单独跑一次 `npm run build:agent` 看 Go 编译错误。二进制名带平台后缀,跨平台复制过来的产物不会被识别。

**smoke 卡在等端口行**
看 `.smoke.log`:子进程若在打印 `<port>` 前就退出,通常是配置加载失败(例如用户级 `config.yaml` 语法坏了)。删掉该文件重试。

---

## License

[MIT](LICENSE) © 2026 [darven](https://github.com/darven-cs)
