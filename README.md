# darvin-cowork

个人桌面智能助手原型：Electron + Vue3 + Tailwind v4 前端，Go 编写的 `darvin-agent` 作为主进程子进程承载 agent 循环。

- 架构说明：[`docs/系统架构.md`](docs/系统架构.md)
- 开发约定（lint / i18n / commit 规范）：[`AGENTS.md`](AGENTS.md)
- 设计文档与进度：[`specs/`](specs/)

## First Run

环境要求：Node 22+、Go 1.22+。

### 1. 安装依赖

```bash
npm install
```

### 2. 配置 Anthropic API key（二选一）

**（推荐）在 UI 里配置**：先跳到第 3、4 步把应用跑起来，然后

> Settings → Models → 填入 API Key（可选 Base URL）→ 保存

保存后主进程会把配置写到用户级 `config.yaml` 并**自动重启 Go 子进程**，无需手动重开应用。

**或用环境变量**：

```bash
export LLM_API_KEY=sk-ant-...
npm start
```

仓库内 `src/darvin-agent/config.yaml` 里的 `llm.api_key` 默认留空，**不要**把真实 key 提交进去。配置优先级：

```
环境变量 LLM_API_KEY  >  用户级 config.yaml  >  仓库内 config.yaml
```

用户级文件路径（Go 侧 `config.UserConfigPath()` 与 Electron 侧 `app.getPath('userData')` 保持一致）：

| 平台 | 路径 |
|------|------|
| Linux | `~/.config/darvin-cowork/config.yaml` |
| macOS | `~/Library/Application Support/darvin-cowork/config.yaml` |
| Windows | `%APPDATA%\darvin-cowork\config.yaml` |

### 3. 构建 Go 二进制

```bash
npm run build:agent
```

产出 `bin/darvin-agent-<platform>-<arch>`。`npm start` / `npm run make` 都挂了 `pre` 钩子会自动跑这一步，单独执行只是为了先确认 Go 侧能编过。

### 4. 启动 Electron

```bash
npm start
```

主窗口打开后，聊天区顶栏的运行时状态徽标（`RuntimeStatusBadge`）应显示为已连接；
主进程 stdout 会打印 Go 子进程监听的端口。

### 5. 发一条 prompt

在输入框里输入内容 → 回车 / 点发送。预期看到流式 token 逐字上屏，结束时 `done` 事件带回本轮 token usage。

也可以在 DevTools console 里直接调协议层：

```js
await window.darvin.prompt({ content: 'ping', sessionId: 'default' });
await window.darvin.listSessions();
await window.darvin.getMessages('default');
```

## 验证

### Headless smoke（不需要 API key，秒级）

```bash
npm run smoke
```

脚本会编译二进制、拉起子进程、解析端口、用 WebSocket 跑一遍 JSON-RPC 协议栈
（`subscribe_events` / `list_sessions` / `get_messages` / `prompt` / 等 `agent_end`），
最后 SIGTERM 并断言 5s 内退出。退出码 0 即通过。

因为用的是占位 key，第 4 步的 prompt 会走到 LLM 报错分支——这是预期行为，
smoke 校验的是协议链路与落盘钩子（prompt 之后 `list_sessions` 必须能看到该会话）。

### Playwright e2e

```bash
npm run e2e          # 全量；@real-llm 用例在没 key 时自动 skip
npm run e2e:headed   # 同上，带界面
npx playwright test --project=core   # 只跑不依赖 LLM 的用例
```

带 `@real-llm` 标签的用例需要真实 key，设置后才会实际执行：

```bash
export ANTHROPIC_API_KEY=sk-ant-...
npm run e2e
```

### 其他

```bash
npm run lint                                  # 前端 eslint
cd src/darvin-agent && go test -race ./...     # Go 单测
```

## Troubleshooting

**改错了 API key，想清空重来**

删掉用户级配置即可（下次启动回落到仓库内 `config.yaml` / 环境变量）：

```bash
rm ~/.config/darvin-cowork/config.yaml                                  # Linux
rm ~/Library/Application\ Support/darvin-cowork/config.yaml             # macOS
del %APPDATA%\darvin-cowork\config.yaml                                 # Windows
```

**想清空历史会话**

会话与消息存在 SQLite（`sessions.db`，默认在工作目录）。停掉应用后删除该文件，
下次启动会自动 AutoMigrate 重建空库。

**`npm start` 报找不到 darvin-agent 二进制**

`npm run build:agent` 单独跑一次，看 Go 编译错误。二进制名带平台后缀，
交叉平台复制过来的产物不会被识别。

**smoke 卡在等端口行**

看 `.smoke.log`：子进程若在打印端口前就退出，通常是配置加载失败
（例如用户级 `config.yaml` 语法坏了）。删掉该文件重试。
