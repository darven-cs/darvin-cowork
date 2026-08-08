# CLAUDE.md

> Claude / Claude Code 等 AI agent 进项目先读这个。

## 先读

1. 当前所在子目录的 `AGENTS.md`（如果有）
2. `### darvin-agent Go 代码规范`(若改动 `src/darvin-agent/` 下的 Go 代码)

## 三句话总结

1. 这是一个 Electron + Vue3 + Go 桌面应用原型
2. 不主动 commit，不主动 broad refactor，不写 `Co-Authored-By`

## 详细规则

本文件为编码代理提供该仓库（`darvin-cowork`）的当前工作模型。当此处内容过时时，以 `package.json`、源代码为准。

## 指令范围

本文件 `CLAUDE.md` 是仓库级指南。子目录里目前没有更具体的覆盖文件，未来如果新增子级 `AGENTS.md` / `AGENTS.override.md`，更具体的指令覆盖更宽泛的指令。

不要把生成的运行时产物（`.vite/build/`、`out/`、`node_modules/`、`.bak/` 里备份的旧 webpack 配置）当作权威的项目指令。仅用作历史上下文，并对照当前源代码进行验证。

## 项目概览

`darvin-cowork` 是一款个人桌面智能助手。架构与近期演进方向如下（见 `docs/系统架构.md`）：

- **桌面壳**：Electron（`@electron-forge` + `@electron-forge/plugin-vite`）。
- **渲染层**：Vue3 + Tailwind CSS v4（`@tailwindcss/vite` 插件接入 Vite）；样式统一走 utility class，组件里不写裸 CSS。
- **AI 产物预览 (Artifact 渲染器)**：`src/renderer/components/side-panel/renderers/` + `side-panel/ArtifactPanel.vue` + `side-panel/ArtifactRenderer.vue`，按产物类型分派渲染器（`Code` / `Document` / `Html` / `Image` / `LocalService` / `Markdown` / `Mermaid` / `Svg` / `Text` / `Video`），沙箱渲染避免污染主页面 DOM；不要把 AI 原始产物直接 `innerHTML` 注入主页面。
- **Agent runtime**：Go 编写的 `darvin-agent`，作为 Electron 主进程的子进程运行（`src/main/runtime/manager.ts` → spawn `bin/darvin-agent-<platform>-<arch>`）。
- **主进程**：仅负责 Electron 生命周期 + 启动 Go 子进程；业务逻辑（agent 循环、工具调用、记忆、skills、MCP、上下文压缩、模型切换等）全部下放到 Go 运行时。
- **构建产物打包**：通过 `extraResources` 把 `bin/` 下当前平台的 Go 二进制随 `out/` 资源一起分发，置于 `Resources/bin/`（不进 asar，因为 spawn 需要可执行权限），由 `forge.config.ts` 的 filter 只打当前平台。

### 架构图

```
Electron 主进程 (src/main/index.ts)
    ├─ Electron 生命周期、BrowserWindow
    ├─ RuntimeMgr (src/main/runtime/manager.ts)   ← EventEmitter, spawn + 抓 <port>
    ├─ AgentClient (src/main/runtime/client.ts)   ← WebSocket JSON-RPC 2.0 客户端
    ├─ EventRouter (src/main/store/EventRouter.ts)← 纯转发 agent.event → webContents.send
    └─ ~70 ipcMain.handle（session/message/skill/MCP/workspace/LLM/prefs/...）
         │
         ▼
         darvin-agent (Go, src/darvin-agent/)   ← 子进程，IPC 协议见 src/shared/darvin-api.ts

preload (src/preload/index.ts)   ← contextBridge 暴露 window.darvin

renderer (src/renderer/)
    ├─ Vue3 应用 (createApp().mount('#app'))
    ├─ Tailwind CSS v4 (@tailwindcss/vite)     ← 样式统一走 utility class
    └─ Artifact 渲染器 (components/side-panel/renderers/)  ← 按产物类型分派渲染器
```

## 命令

```bash
# 开发：启动 electron-forge（Vite 端口 + Electron 窗口）
npm start

# 单独编译当前平台 Go agent（输出 bin/darvin-agent-<platform>-<arch>）
npm run build:agent

# 打安装包（自动先跑 build:agent，再调 electron-forge make）
npm run make

# 仅生成 unpacked 产物（不打包成安装包）
npm run package

# 发布（electron-forge publish；通常 CI 才会用，本地需配置 GH_TOKEN 等）
npm run publish

# ESLint：src 全部 .ts/.tsx/.vue
npm run lint

# 单元测试：vitest run（见 vitest.config.ts；覆盖 src/**/*.test.ts）
npm run test

# 烟雾测试：scripts/smoke.sh（手动跑，验证打包产物可启动）
npm run smoke
```

要求：
- Node.js `>=20`（具体看 `@types/node ^20.11.0` 与 `electron 43.2.0` 兼容性矩阵）。
- Go agent 构建需要本机 `go` 工具链；`scripts/build-go.js` 会设置 `CGO_ENABLED=0` 强制静态。
- Windows 下若安装时涉及 shortcuts 由 `electron-squirrel-startup` 处理；当前主进程已包含该 guard。

Go agent 相关：
- `src/darvin-agent/` 下放置 Go agent 源码（含 `go.mod` / `main.go`）。`scripts/build-go.js` 在该目录不存在时会打印警告并 `exit 0`，不会阻塞 Electron 启动。
- Go 模块名见 `src/darvin-agent/go.mod`。
- 主进程侧的 IPC 客户端 (`src/main/runtime/client.ts`)：`class AgentClient`（WebSocket JSON-RPC 2.0 客户端），通过 `RuntimeMgr` 解析出的 `<port>` 拨号；具体协议见 `src/shared/darvin-api.ts` 的 `DarvinApi` / `DarvinEvent` / `DarvinMessage` 定义。

## 测试

单元测试运行器已落地 Vitest：

- 脚本：`package.json` 的 `pretest`（rebuild `better-sqlite3` native binding + 清理 forge-meta）+ `test`（`vitest run`）+ `smoke`（`bash scripts/smoke.sh`，spawn 二进制、等 `<port>` 行、跑 `ws-smoke-client.js`、超时 SIGKILL 兜底）；devDependencies 含 `vitest` 与 `@vitest/coverage-v8`。
- 配置：仓库根 `vitest.config.ts`（`environment: 'node'`，`include: ['src/**/*.test.ts']`，排除 `node_modules/` / `out/` / `.vite/build/` / `.git/`）。
- 约定：测试文件放在被测源码旁，命名 `*.test.ts`（与被测模块同名，例如 `src/main/libs/user-paths.ts` ↔ `user-paths.test.ts`）。
- API：从 `vitest` 导入 `describe` / `it` / `expect` / `vi` / `beforeAll` / `beforeEach`；需要 mock `electron` 时用 `vi.mock('electron', ...)` + `vi.hoisted` 维护 mock 状态（参考 `src/main/libs/user-paths.test.ts`）。
- CI 入口包含 `npm run lint` + `npm run test`。
- 新增逻辑：能不写测试就不写（避免覆盖债堆积）；覆盖时优先覆盖纯函数 / IPC 协议解析 / 路径与序列化工具，避免给 Electron 主进程写集成测试。

UI / Electron 行为验证走 `playwright-cli`（不入 CI，dev 手动跑）。主进程在 `!app.isPackaged` 时已自动开 `remote-debugging-port=9222` + `remote-allow-origins=*`（见 `src/main/index.ts`），跑 `npm start` 拉起 Electron 后端口即可用。

前置环境检查（每次接活时跑一次）：

1. CLI 是否已装：
   ```bash
   playwright-cli --help
   ```
   没装就全局装：
   ```bash
   npm install -g @playwright/cli@latest
   ```
2. 项目级 skills 是否已装：
   ```bash
   playwright-cli install --skills
   ```
   没装就跑这条装上；已装就跳过，之后直接用 skills 驱动 Electron 窗口。

验证流程：先 `npm start` 起 Electron，再在另一终端按需调用 `playwright-cli` / skills 操作。

## 质量门槛

ESLint 配置在 `.eslintrc.json`：基于 `eslint:recommended` + `@typescript-eslint/recommended` + `import/recommended` + `import/electron` + `import/typescript`。

`.vue` 走 `overrides`：`vue-eslint-parser` 拆包（`<template>` 出 Vue AST，`<script>` 转交 `@typescript-eslint/parser`）+ `plugin:vue/vue3-recommended`。
其中纯排版类规则（`max-attributes-per-line` / `singleline-html-element-content-newline` / `html-self-closing` / `attributes-order`）与本仓库紧凑写法冲突，已关闭；
`multi-word-component-names` 因组件全部显式 import、不存在全局注册冲突，也已关闭。保留的是错误预防类规则。

```bash
# 全量 lint（src 内全部 .ts/.tsx/.vue）
npm run lint

# 仅 lint 受影响的文件（与 CI 一致）
npx eslint --ext .ts,.tsx,.vue <files>
```

验证期望：
- **文档 / 配置变更**：不需要跑 lint；说明"仅改文档 / 配置"即可。
- **主进程 / preload / runtime 改动**：跑 `npm run lint`，并用 `npm start` 手动起一次确认窗口能开、Go agent 解析路径符合预期。
- **renderer 改动**：跑 `npm run lint`，并通过 `npm start` 在 DevTools 里观察 console。
- **Go 端改动**：在 `src/darvin-agent/` 下用 `go build` / `go vet` 本地验证；若改了 `scripts/build-go.js`，同步跑 `npm run build:agent`。
- **forge.config.ts / vite.* 改动**：必须先 `npm start` 验证 HMR / 构建流程没坏，再考虑打 `npm run package`。
- **交接前**：检查 diff 里有没有无关改动、风险性大的重构、生成的产物（`.vite/`、`out/`、`bin/darvin-agent-*`）以及用户可见的字符串错误。

## 架构要点

### 主进程

`src/main/index.ts`：
- 处理 `electron-squirrel-startup` 短路；
- `createWindow()` 创建 `BrowserWindow`，`webPreferences.preload` 指向 Vite 产物路径；
- 开发态 `loadURL(MAIN_WINDOW_VITE_DEV_SERVER_URL)`，生产 `loadFile(...)`；
- 持有 ~70 个 `ipcMain.handle` channel（session / message / skill / MCP / workspace / LLM / prefs / locale / runtime status / attachment / permission 等）；所有数据所有权归 Go（merge-databases refactor 后 main 端不再持有业务 SQLite，Go 离线时 main 端用进程内 in-memory 缓存兜底，保证最近一次视图可见）；
- 维护 `RuntimeMgr`（subprocess 生命周期）+ `AgentClient`（WS JSON-RPC 客户端）+ `EventRouter`（`agent.event` → `webContents.send` 纯转发）+ `installAppMenu()` + `notifyIfHidden`（窗口隐藏时 OS 通知）；
- DevTools 开发态默认打开 + `remote-debugging-port=9222` + `remote-allow-origins=*`；
- `window-all-closed` 在 macOS 外退出；`activate` 时若无窗口则重建。

`src/main/runtime/manager.ts`：
- `class RuntimeMgr extends EventEmitter`；
- `resolveAgentBinaryPath()`：根据 `app.isPackaged` 选 `process.resourcesPath/bin/...` 或 `__dirname` 回溯三级的开发路径；缺失时打 warning 不抛错；
- `start(workspaceRoot?)`：`spawn(bin)`，从 stdout 抓 `<port>…</port>`（5 s 超时），SIGTERM + 4 s 宽限期停止；
- 暴露 `pid() / port() / isResolved() / resolveAgentConfigPath()`（dev only）。

`src/main/runtime/client.ts`：
- `class AgentClient`：WebSocket JSON-RPC 2.0 客户端，连 `ws://localhost:{port}/ws`；
- 完整方法面：`connect / disconnect / request / prompt / abort / invokeSkill / subscribeEvents / listSessions / getMessages` + 命名空间 `skills.{list,setEnabled,bootstrap,onChanged}` / `mcp.{list,register,update,unregister,setEnabled,test,retryResolution,bootstrap,onConnectionChanged,onResolutionChanged}` / `tools.list` + `onEvent / isConnected`；
- 提供 `parseDarvinEvent` 给 union 类型判别；导出 `BACKEND_DEFAULT_SESSION_ID` 常量。

### preload

`src/preload/index.ts` 通过 `contextBridge.exposeInMainWorld('darvin', api)` 把 Go agent 客户端的方法以受限 API 暴露给 renderer；API 形状见 `src/shared/darvin-api.ts`。

### renderer

`src/renderer/` 是 Vite root（`root: 'src/renderer'`，`base: './'` 用于生产相对路径）。当前栈：

- **Vue3**：`index.ts` 已 `createApp(App).mount('#app')`，挂载点 `<div id="app">` 在 `index.html`，`<html lang="zh-CN">`。
- **Tailwind CSS v4**：通过 `@tailwindcss/vite` 插件接入 `vite.renderer.config.mts`；`index.css` 顶部 `@import "./styles/theme.css"; @import "./styles/reset.css";`，设计 token 统一走 `styles/theme.css` 的 `@theme` 块。样式优先用 utility class；仅在跨组件复用的设计 token（颜色 / 间距 / 字号）才用 `@theme` 抽象。
- **Artifact 渲染器**：见下文"编码风格"小节关于 `components/side-panel/renderers/` 的约束（按产物类型分派 Code / Document / Html / Image / LocalService / Markdown / Mermaid / Svg / Text / Video）。

### Go agent

`src/darvin-agent/` 是 Go 后端实现（`go.mod` module 名 `darvin-cowork/backend`，Go 1.22）。`scripts/build-go.js`：

- 输出 `<repo>/bin/darvin-agent-<platform>-<arch><.exe?>`；
- 设置 `CGO_ENABLED=0`、`GOOS=process.platform`、`GOARCH=process.arch`；
- `cwd: src/darvin-agent`，`go build -ldflags="-s -w" -o ${out} ./cmd/app`；
- 失败非零退出，被 `npm run make` 的 `premake` 钩子传播。

模块布局（15 个 `internal/` 包）：

- `cmd/app/main.go`：15 行，仅 `os.Exit(runApp(os.Args[1:]))`；`runApp` 是 var，测试可替换入口。
- `internal/runtime/`：single assembly entry；`Build(ctx, Options) (*Runtime, error)` 加载 config + DB + LLM provider + 装配 agent factory + bootstrap skills / MCP + 启动 gateway + bootstrap active session；`Run(args)` 接 SIGINT / SIGTERM；`Shutdown(ctx)` 关闭 server / harness / SQLite。
- `internal/gateway/`：WS server（端口由 main 端从 stdout 解析）+ JSON-RPC framing + handler dispatch + per-session manager + per-session event ledger。
- `internal/agentloop/`：`Loop` 单一所有者，按 session 串行 turn 队列；`Submit` / `Steer`（cancel in-flight + steerQueue + wake）/ `Close`；`agent.Prompt + agent.Run` 走 `harness.BuiltinEmbeddedHarness`。
- `internal/agents/`：`Agent.Prompt / Run / Abort / Subscribe`；`dispatcher` enqueue + runMsgID；subpackage `queue / session / store / executor / perm / ctxengine / msgid / protocol / runtime / usage`。
- `internal/llm/`：streaming protocol + `anthropic/` 子包 + 兼容层 + registry；events 与 errors 单独文件。
- `internal/tools/`：built-in shell / fs / sandbox + permission + registry + MCP bridge；exclusions 文件白名单。
- `internal/skills/`：scanner / loader / frontmatter / registry / plugin / runner / wire；安装走 `skillInstall`（main 端）+ `wire`（Go 端）。
- `internal/mcp/`：client / launcher / registry / transport (http+sse+stdio) / resolver_fingerprint / persistence。
- `internal/database/`：GORM + `glebarez/sqlite` 单例 `globalDB`；`internal/agents/store/` 持有 session / message / app_state / imported_file / memory。
- `internal/config / logger / harness / jsonschema`：viper 配置 + zap + lumberjack 日志 + harness 抽象 + JSON Schema 校验。

`forge.config.ts` 的 `extraResources.filter` 仅保留 `darvin-agent-<platform>-<arch>(.exe)` 与 `.gitkeep`，避免 dev 机器把 darwin+linux+win 全打进去。

Go 端测试：每个 internal 包同目录 `*_test.go`（vitest 只跑 TS；Go 测试走 `cd src/darvin-agent && go test ./...`）。`Makefile` 含 `lint-agents-boundaries` target，禁止 `agents/` 引入 capability 包（`llm / tools / skills / mcp`）。

## 字符串常量

IPC 通道、推送事件、流事件、消息类型已统一在 `src/shared/darvin-api.ts`：

- `DarvinApi`：~70 个 request 方法的接口（session / message / skill / MCP / workspace / LLM / locale / prefs / attachment / permission / artifact 等），所有请求/响应类型同源导出；
- `DarvinPushEvent`：常量 (`SessionsChanged / ActiveSessionChanged / SessionEvent / WorkspaceChanged / SkillsChanged / McpServersChanged / McpConnectionChanged`)；
- `DarvinEvent`：discriminated union（`text_delta / thinking_delta / tool_start / tool_end / done / error / agent_end / compaction / context_usage / permission_request / artifact`）；
- `DarvinMessage`：discriminated union（`user / assistant / tool_use / tool_result / system`）；
- 提供 `parseDarvinEvent / assertNever` 帮助判别；另有 `darvinMessageRole / darvinMessageContent` 旧 shape 归一化。

约定：

- 模块一源；同时导出值对象 + 类型；测试断言使用同一常量。
- main / preload / renderer 都从 `darvin-api.ts` 导入，禁止在组件内 `any`。
- 一次性错误消息、CSS class、HTML attribute、外部平台 ID 不用常量化。

## 国际化

renderer 侧 i18n 已落地（`src/renderer/services/i18n.ts`，平铺 `dictZh` / `dictEn` 双语字典，`assertSameKeys` 强制 key 对齐）；主进程侧 i18n（托盘 / 菜单 / 窗口标题 / 通知）尚未建。任何面向用户的字符串必须经 `t()`，不要直接写死在 template / script。

### 字典结构

- 文件：`src/renderer/services/i18n.ts` 内的 `dictZh` / `dictEn`（同一文件，导出严格一致的 key 集合；**不要**新建第三套运行时 i18n 库）。
- key 命名：`feature.subfeature.label`，按 feature 子域分段：
  - `app.*` 应用级（标题、菜单入口、状态）
  - `sidebar.*` 侧栏
  - `chat.*` 主聊天区
  - `sidepanel.*` 右侧工具面板
  - `home.*` 首屏
  - `settings.*` 设置面板（按子面板再分 `settings.about.*` / `settings.models.*`）
  - `model.*` / `expert.*` / `quick.*` / `plus.*` 跨域模块
- value 用原文（中英混排可接受，模型名 / API 名 / 协议字段保持英文：`'app.runtime.ready': 'Runtime: ready'`）。

### API 表面

- `t(key: string, params?: Record<string, string | number>)`：命中返回译文并做 `{name}` 插值；未命中**直接返回 key**（便于 dev 期发现遗漏；生产期不要凭 key 直接展示给用户——必须先把字典补齐）。缺 key 时 dev 期 `console.warn` 一次。
- `setLang(lang: 'zh' | 'en')` / `getLang(): 'zh' | 'en'`：全局语言切换，**响应式**——`currentLang` 是 `ref`，模板内 `{{ t('xxx') }}` 在 render 期读取它，`setLang` 后整树自动 re-render。纪律：不要在 `<script setup>` 顶层缓存 `t()` 结果（缓存会断响应式链）。
- `formatNumber(n, opts?)` / `formatDate(ts, opts?)` / `formatRelativeTime(ts)`：数字 / 日期 / 紧凑相对时间，按当前语言格式化。
- 组件消费：**直接 import**（`import { t } from '../../services/i18n'`），不引入 vue-i18n / i18next 等第三方库，不写 composable 包装层（按 YAGNI，重复 `import { t }` 比多一层抽象便宜）。

### 写入规则

- ✅ 写完整句子作 value：`'chat.placeholder': 'Send a message...'`
- ❌ 拆分句子跨 key：`'chat.greet' + 'chat.body'` 拼接（语序因语言而变，会破坏翻译）
- ❌ 模板字符串拼用户输入：`t('chat.greet') + userName`（变量位置不可控）；插值一律写进 value 占位并 `t('chat.greet', { name })`，调用点不要手拼 `.replace('{x}', v)`
- ❌ 数字 / 时间 / 日期硬编码格式：一律走 `formatNumber` / `formatDate` / `formatRelativeTime`（按当前 locale 取）；不要写 `12 个任务` 这类硬拼接。
- ❌ `<template>` 内直接写中文字面量：必须 `{{ t('xxx') }}` 或 `:aria-label="t('xxx')"`（哪怕是过渡期占位也得走 key，方便后续翻译）。
- ❌ 同一字符串在多处出现却只在一个 key 里登记：先抽 key 再用——重复字符串就是隐性漏译。
- ✅ 占位文案也要走 `t()`：写 `'sidebar.placeholder.warn': '此功能尚未实现'`，不要直接 `<div>此功能尚未实现</div>`。

### 不走 i18n 的内容

- DevTools / stdout / 日志（`console.log` / `console.error` 等）：开发者诊断信息，保留英文或源码语言，不要花精力翻译。
- 错误堆栈、异常 message、IPC 协议字段：技术输出，不走 i18n。
- 用户可见但纯英文的 token：模型名（`claude-sonnet-4-5`）、API Key 前缀提示（`sk-ant-...`）、技术标识符。

### zh / en 字典同步

`en` 字典落地时强制做 key 一致性：

- 两份字典**必须**包含完全相同的 key 集合（多 key / 少 key 都视为 bug）。
- 校验方式：在 `src/renderer/services/i18n.ts` 顶部写一段 `assertSameKeys(dictZh, dictEn)`，开发期跑一次（`process.env.NODE_ENV !== 'production'` 守卫）。
- 翻译缺失临时态：英文 key 暂时用 `dictEn[key] = dictZh[key]`（机器/拼音回退也行）兜底，**不允许**直接 fallback 到 zh 后悄悄上生产。

### 主进程侧（暂不在范围内）

i18n 目前只在 renderer 落地，主进程侧字符串（托盘 / 菜单 / 窗口标题 / 通知）保持英文。需要扩展时：

- 文件位置：`src/main/i18n.ts`，**不要**复用 renderer 的 `services/i18n.ts`（主进程不能直接 `import` renderer 路径，会跨进程边界拉拽依赖）。
- 字典可内联在 main 端或抽到 `src/shared/i18n-dict.ts`（与 renderer 同 key 集合）；当前未抽离，renderer / main 各自维护。
- locale 持久化：跟随用户设置（`src/main/libs/user-settings.ts` 的 `locale` 字段），不要单独存一份；首次启动回落到 `app.getLocale()` 探测的 `zh-CN` / `en-US`。

### 何时升级到 vue-i18n

当前手写 `t()` 满足平铺字符串 + zh/en 二语 + `{name}` 插值 + 响应式切换。**遇到任意一条**即应停下评估引入 vue-i18n：

- 出现复数形式（`{count, plural, one {} other {}}`）。
- 语言 ≥ 3 种或需要按 namespace 懒加载字典。

不要为了「将来可能需要」提前换；先在现有字典里把缺失能力显式列出，等真正落地时再迁。

## 编码风格

- TypeScript 是默认语言。`tsconfig.json` 已 `strict + noImplicitAny`。
- React / Vue 组件：函数组件 + Composition API，优先使用 `<script setup>`。
- 2 空格缩进、单引号、分号；ESLint 规则按现有 `.eslintrc.json` 为准。
- 文件命名沿用 Vite 默认（保留现有 `index.ts`），新模块按职责拆 `PascalCase` 组件 / `camelCase` 工具函数。
- 业务逻辑保持在 `src/main/libs/`、`src/main/runtime/`、`src/darvin-agent/` 等模块中，不要塞进 UI 组件。
- 优先使用现有模式与本地 helper，不要为了新加一个功能造新抽象。
- **样式 (Tailwind CSS v4)**：组件内优先用 utility class，**不要写裸 `<style>` 或组件级 CSS 文件**（除 `index.css` 的 `@import "tailwindcss"` + 极少量全局 reset / 字体外）。跨组件复用的设计 token 走 `@theme` 块，不要散落成 magic value（`#1a1a1a` / `text-gray-300` 等禁止）。新增交互态/动效用 Tailwind 自带的 `hover:` / `focus:` / `dark:` 等变体，不要为单个 hover 写新 CSS。组件**不用** `style="..."` 内联属性；颜色 / 间距 / 字号一律走 `bg-bg` / `text-text-muted` / `rounded-md` / `text-sm` 等 utility（值来源 `@theme`）。
- **Artifact 渲染器 (`src/renderer/services/artifact-renderer/`)**：AI 生成的产物（HTML / SVG / Mermaid / React / 代码块）**一律走 sandboxed iframe**，iframe 必须 `sandbox="allow-scripts"` 起跳、按需 `allow-same-origin`（仅在需要 DOM API 时打开），不与主页面共享 DOM。渲染器对外只暴露受控 API（`mount(artifact, container)` / `update(payload)` / `destroy()`），不把 iframe `contentWindow` 直接透出。产物源（来自 Go agent 的 IPC payload）须按类型分派（html / react / mermaid / svg / code），不要 `innerHTML` 注入主页面。

### Vue 3 组件化规范（renderer 锁定）

所有 Vue 组件 **必须** 遵守以下 10 条（违反任意一条 PR review 拒绝）：

1. **SFC + `<script setup lang="ts">`**；不允许 Options API。`<script setup>` 顶层只放 props / emits / 组合逻辑，不放业务。
2. **Composition API**：`ref` / `reactive` / `computed` / `watch` 自由组合；不引入 mixins / class-based 组件。
3. **props 强类型**：`defineProps<T>()`；emits 用 `defineEmits<T>()`；不写 `defineProps(['xxx'])` 字符串数组。
4. **跨组件状态走 composables**（`src/renderer/composables/useXxx.ts`），不直接共享 `ref` / 不用 EventBus。
5. **单向数据流**：父 → 子 props；子 → 父 emits；不双向 `v-model` 除非表单组件（textarea / input / select）。
6. **命名**：
   - 组件文件 `PascalCase`：`SidebarHeader.vue` / `MessageItem.vue`
   - composable `camelCase` + `use` 前缀：`useTheme.ts` / `useMessages.ts`
   - 模块级 `ref` 用 `xxxRef` 后缀（避免与局部变量混淆）：`const listRef = ref<HTMLDivElement | null>(null);`
   - 业务 `ref` 不加后缀：`const busy = ref(false);`
   - **目录约定**（`src/renderer/` 下）：
     - `layout/` — 页面级布局壳（与 `components/` 平级），如 `AppShell.vue`
     - `views/` — 顶层路由页面（`HomeView` / `ChatView` / `SettingsView` / `SkillsView` / `McpView` / `ExpertSuiteView` / `SearchView` / `PlaceholderView`）
     - `components/` — 可复用功能组件，按 feature 子目录拆分（`chat/` / `chat/tools/` / `sidebar/` / `side-panel/` / `side-panel/renderers/` / `home/` / `settings/` / `skills/` / `mcp/` / `expert/` / `runtime/`）
     - `components/common/` — 跨 feature 通用组件（`Icon` / `IconButton` / `Dropdown` / `ToastHost`）
     - `composables/` — 所有 `useXxx.ts` 单例状态（`useAppearance` / `useMessages` / `useChatActions` / `useSidebar` / `useSidePanel` / `useArtifacts` / ...）
     - `services/` — 纯函数 / IPC 客户端 / mock 数据（`i18n` / `markdown` / `highlight` / `tokenFormat` / `toolDisplay` / `toast` / `artifactHtml` / `mock-data`）
     - `styles/` — 全局 CSS（`theme.css` + `reset.css`）
     - `assets/icons/` — SVG 图标源（自动 glob 加载）
     - `assets/agent-avatars/` — agent 头像 SVG（30 个）
7. **样式约束**：组件 `<template>` 内只用 utility class（`bg-surface` / `text-text-muted` 等），颜色全走 `@theme` token；不写 `<style>` 块。
8. **事件**：原生事件（click / input）走 emits 包装后冒泡；不让子组件直接调父方法或共享可变 ref。
9. **类型导入**：所有 IPC 数据 / DarvinEvent / Message 从 `src/shared/darvin-api.ts` 导入；禁止在组件内 `any`（除非 dom 事件回调）。
10. **严禁**：
    - 组件内 `<style>` 块
    - 全局 CSS 文件（除 `src/renderer/styles/{theme,reset}.css`）
    - Tailwind 默认调色板（`bg-gray-800` / `text-red-500`），改用 `@theme` token
    - 内联 `style="..."` 颜色 / 间距
    - 第三方组件库（Naive UI / Element Plus / PrimeVue 等），除非业务 spec 显式引入
    - `import { ref, ... } from 'vue'` 之外的第三方状态库

### 设计 token（Tailwind v4 `@theme`）

颜色 / 间距 / 圆角 / 字号 / 阴影 / 动画 token 唯一来源：`src/renderer/styles/theme.css` 的 `@theme` 块。

- **颜色 token**（light default，dark 覆盖在 `@layer base`）：
  - 基础：`bg` / `surface` / `surface-2` / `surface-raised` / `surface-hover` / `border` / `border-strong` / `text` / `text-muted` / `text-subtle`
  - 品牌：`primary` (#FF5722) / `primary-hover` / `primary-muted` / `primary-soft`；`accent` 是 `primary` 的别名（向后兼容现有 IconButton / SessionItem）
  - 主题色 swatch：`accent-orange` / `accent-blue` / `accent-green`，通过 `<html data-accent="blue|green">` 覆盖
  - 语义：`success` / `danger` / `warning` / `thinking`
  - 消息气泡：`user-msg` / `user-msg-bg` / `assistant-msg` / `assistant-msg-bg`
  - 业务：`qa-{slide,data,doc,web}` / `agent-{amber,violet,blue,green,red,cyan,pink,orange,purple}` / `vendor-{anthropic,openai}`
- **深色覆盖**：根 `<html>` 默认 light；`<html class="dark">` 触发 `@layer base` 下 dark token 覆盖。主题色覆盖用 `<html data-accent="blue|green">` 独立于 light/dark 生效。组件**不要**用 Tailwind `dark:` 变体（class 切换 + token 覆盖更稳）。
- **圆角 token**：`sm` (4px) / `md` (6px) / `lg` (8px) / `xl` (12px) / `2xl` (16px)
- **间距 token**：`app-padding` (16px) / `section-gap` (24px)
- **字号 token**：`xs` (11px) / `sm` (13px) / `base` (14px) / `md` (15px) / `lg` (18px) / `xl` (24px) / `2xl` (32px) / `code` (13px)
- **阴影 token**：`sm` / `md` / `lg` / `primary`（品牌色 glow）
- **字体 token**：`--font-sans` / `--font-mono` / `--font-display`
- **动画 token**：`cursor-blink` 1s step-end infinite / `fade-in` 0.5s ease-out both / `mascot-breathe` / `mascot-blink` / `mascot-wave` / `plus-menu-in`

**消费规则**：
- ✅ `bg-surface` / `text-text-muted` / `border-border` / `rounded-md` / `text-sm`
- ❌ `bg-[#1a1a1a]` / `text-gray-300` / `bg-red-500` / `style={{ color: 'red' }}` / padding: `12px` 等 magic value

新加 token：先在 `theme.css` 的 `@theme` 块加 `--color-foo` / `--spacing-bar` / `--text-foo` 等；深色覆盖在 `@layer base` 的 `html.dark` 块补；**然后** `bg-foo` / `text-foo` utility 自动可用。

### 图标系统

**只用 SVG**，不引入 `lucide-vue-next` / `@heroicons/vue` / `naive-ui` 等图标库。

- 源文件：`src/renderer/assets/icons/*.svg`（当前 ~56 个，统一 `viewBox="0 0 34 34"` + `stroke="currentColor"` + `stroke-width="2.4"` + round caps）。少量用户导入的 SVG 含 `stroke="black"` 硬编码；`index.ts` 加载时一次性 `replace(/stroke="black"/g, 'stroke="currentColor"')` 归一化。
- 加载：`src/renderer/assets/icons/index.ts` 用 `import.meta.glob<string>('./*.svg', { eager: true, query: '?raw', import: 'default' })` 全部 inline 加载，导出 `SVG_SOURCES: Record<string, string>`（name → svg 字符串）。
- 组件：`src/renderer/components/common/Icon.vue`，`<Icon name="..." :size="18" />`；缺失 icon 渲染空 16×16 占位（不抛错）。
- 注册：`assets/icons/index.ts` 暴露 `registerIcons(app)` 一行注册为全局组件，main 入口调一次即可。
- 命名：`kebab-case`：`<Icon name="chevron-down" />` / `<Icon name="panel-right-close" />`。
- agent 头像另有 `src/renderer/assets/agent-avatars/*.svg`（30 个，artboard / books / brain / code / … / translation / travel），按业务组件按需静态引用。

**新增 icon 规则**：
- 丢到 `src/renderer/assets/icons/<name>.svg` 即可，无需 import（自动 glob）。
- **必须**用 `stroke="currentColor"`；不要写死颜色 / 用 `fill="black"` 等。
- 尺寸走 `viewBox` 不写死 `width` / `height`；`Icon` 组件通过 `:size` prop 注入实际渲染尺寸。

### 字体

renderer **不下载 woff2 / 不引用 CDN**，全走系统字体栈，写在 `src/renderer/styles/theme.css` 的 `@theme`：

- `--font-sans: "PingFang SC", "Inter", -apple-system, BlinkMacSystemFont, "Microsoft YaHei UI", "Segoe UI", sans-serif`
- `--font-mono: "JetBrains Mono", ui-monospace, "SF Mono", Menlo, monospace`
- `--font-display: "PingFang SC", "Inter", -apple-system, BlinkMacSystemFont, sans-serif`

`src/renderer/index.html` 只放 inline SVG favicon，不挂任何 `<link>` 字体 / 图标 / 分析脚本。**禁止**引入任何 CDN（图标库 / 字体 CDN / 分析脚本都不行）。如需离线 / 合规场景，自托管 woff2 + `@font-face` 注入，而不是再添加 `<link>`。

### 注释规范

源码追求「代码即文档」，靠清晰命名替代注释，注释越少越好。本规则仅约束 `.ts` / `.vue` / `.go` 等业务源代码文件；`docs/`、各类 spec 文档、子目录 `AGENTS.md` 可以随意编写阶段、版本、规划内容。

#### 一、绝对禁止编写的注释（出现即违规，必须删除）

- **阶段、版本、迭代规划类注释**
  - `// S1/S5 阶段实现` `// v0 占位，v1 重构` `// 后续迭代替换此处逻辑` `// 未来会接入 MCP 协议`
- **代码复述型废话注释**（把代码逻辑用文字再念一遍）
  - 例：代码已经写了 `if (!path) return undefined`，不许加 `// 如果路径不存在就返回空`
- **模型思考、编写过程、改动说明注释**
  - `// 按照规范调整写法` `// 适配项目架构修改逻辑` `// 根据 AGENTS.md 约束重构`
- **展望、TODO 大范围规划注释**（仅极小范围内部标记 TODO 可保留）
  - 禁止大面积罗列后续开发路线、架构演进内容
- **无关铺垫、开场白、收尾总结注释**
  - 代码块前后不要加：`// 下面实现二进制路径解析逻辑` `// 以上完成进程启动封装` 这类首尾解说注释
- **冗余分隔线、空行堆砌注释**
  - 不要用 `// --------------------------` 分割代码区块

#### 二、仅允许保留的注释场景（按需少量添加，能不写就不写）

- **导出公共函数 / 类型 / 类：JSDoc 注释**
  - 仅标注：入参含义、返回值、边界约束、业务不变量；简洁短句，不啰嗦。
- **非常规特殊逻辑：单行意图注释**
  - 代码写法违背常规写法、存在业务硬性约束、后续容易被误改时，一句话说明**为什么这么写**，而非写代码做了什么。
- **硬性架构约束校验注释**
  - 例：`// 事件结构必须对齐 DarvinEvent 类型定义`
- **关键边界兜底逻辑注释**
  - 异常兜底、平台差异化兼容逻辑可简短标注。

#### 三、通用注释格式要求

- 单行注释统一使用 `//`（空格分隔），放在代码上方；行内注释尽量不用。
- JSDoc 精简撰写，无冗余描述，不添加 `@example` 等非必要标签。
- Vue `<template>` 内：完全禁止写 HTML 注释，模板结构语义自解释即可。
- 标识符命名同样适用：避免 `ErrNotImplementedInV0` / `FixForV2` / `MockS5` 这类把版本号塞进 API 名字的做法。

### darvin-agent Go 代码规范

> 适用范围：`src/darvin-agent/` 下所有 Go 业务源代码。`docs/` / `specs/` / 子级 `AGENTS.md` 不受本规范约束。
> 规则变更追踪见 `specs/refactors/agent-code-readability/2026-08-08-agent-code-readability-design.md` §3.3。

#### 文件结构

- **F1 单文件软上限 800 行** — 超过按业务域拆分；不应通过"加行"继续往一个文件堆逻辑。
- **F2 god file 按业务域拆，不按语法元素** — 按"领域"拆（handlers / session / mcp / skill），不按"语法"拆（types / utils / interfaces）。`utils.go` / `helpers.go` / `common.go` 是垃圾抽屉的反面教材。
- **F3 每个 `.go` 顶部必须有 package / file-level comment** — 包的主文件用 `// Package foo does X.`；同包其他文件用 file-level 注释说明该文件承担的职责。`staticcheck` ST1000 兜底。
- **F4 文件名 `snake_case` 小写** — 例：`agent_run.go` / `compact_archive.go`。
- **F5 文件按"类型 + 操作"组织，禁建 `types.go` / `utils.go` 垃圾抽屉** — struct / const / interface 跟它的领域逻辑放在一起；不建一个集中放类型的 `types.go`。
- **F6 接口在调用方包定义，实现在被调方包** — 接口属于消费侧；对标 `agents/protocol/` 子包作为跨包契约层。
- **F7 能力接口 + `init()` 自注册** — 新增能力走 `Register*` 工厂注册到 process-global map，main 端通过 `Registered*()` 拉取，避免改 main wiring。`internal/llm/` 已合规，`internal/tools/` 是已知违规点。

#### 命名

- **N1 包名小写、单数、短** — `agent` / `gateway` / `tools`；不 `agents` / `utilities` / `helpers`。`staticcheck` ST1003 兜底 initialism（`Id` → `ID`）。
- **N2 导出 `PascalCase`，包内 `camelCase`，导出必须有 doc** — `staticcheck` ST1020+ 兜底 exported godoc。
- **N3 接口名用职责动词，不带 `I` 前缀** — `Reader` / `Handler` / `Resolver`；不 `IReader` / `IHandler`。
- **N3.1 接口位置遵守 F6** — 接口随消费侧；多包共享走独立 `protocol/` 子包。
- **N4 JSON-RPC handler `handle<Domain>` 前缀** — `handleSessionCreate` / `handleMessageAppend`；不要 `doCreate` / `onSession` / `processXxx`。
- **N5 wire 投影类型 `<Domain>Wire` 后缀** — `SessionCreateWire` / `MessageAppendWire`；区分内部业务类型与 IPC 协议类型。
- **N6 常量值禁止 magic value 散落** — 重复出现的字符串 / 数字 / 边界值（超时、分页大小、重试上限）一律提到 const 块。

#### 注释

- **C1 禁阶段 / 版本 / FR-N / Reasonix / 代码复述 / 思考过程注释** — 黑名单见下表"违规注释模式黑名单"。
- **C2 仅保留 doc / 非常规写法意图 / 架构边界 / 兜底注释** — 注释讲"为什么这么写"而非"做了什么"。
- **C3 注释密度 ≤ 0.30（核心业务文件可放宽 0.35）** — `comments / (total - comments - blanks) ≤ 0.30`。**豁免**：<30 行小文件、纯接口包（如 `agents/protocol/`）、纯包 doc 文件。`scripts/check-agent-readability.sh` 自动扫描。
- **C4 注释语言统一英文** — 全英文，不混中文行内注释。注释清理阶段一次性翻译。
- **C5 godoc 精简，无 `@example`** — 一句话讲清入参 / 返回 / 不变量；不堆 `@example` / `@deprecated` / `@see`。`staticcheck` ST1020-1023 兜底。

#### import

- **I1 三段（stdlib / 第三方 / 内部），空行分隔，组内字母序**

  ```go
  import (
      "context"
      "fmt"

      "github.com/anthropics/anthropic-sdk-go"

      "darvin-cowork/backend/internal/gateway"
  )
  ```

- **I2 `gofmt -s` + `goimports` 自动维护** — 改 import 顺序由工具负责，不手排。
- **I3 禁 `.` import，别名 import 须说明** — `. import` 隐藏符号来源难定位；别名 import 在上方写一行注释说明为何重命名（`// 别名避免与 foo.Bar 冲突`）。
- **I4 import 分组错误由 `goimports -w` 自动归一** — `goimports -w -local darvin-cowork/ .` 一次性归位。

#### 错误

- **E1 错误变量 `Err<Entity>` / `err<Entity>`** — 哨兵错误用 `Err` 前缀（`ErrSessionNotFound`），临时变量用 `err`。
- **E2 `fmt.Errorf` 用 `%w`** — 包装错误保留链路，不丢原始 error；调用方可 `errors.Is` / `errors.As` 判别。
- **E3 错误字符串小写开头、无尾标点** — `errors.New("connection refused")` 而非 `"Connection Refused."`。`staticcheck` ST1005 兜底。
- **E4 `if err != nil` 不加注释** — 不写 `// 处理错误` / `// 出错了` 这类废话。

#### 子包

- **P1 新子包阈值（≥300 行 / 独立依赖边界 / 独立测试）** — 三条同时满足才建子包。理由：建子包是引入 import 边界、测试成本、读代码跳转成本的复合决策。
- **P2 不满足 P1 的合并回父包** — 例：`agents/runtime`(78) / `agents/msgid`(85) / `agents/queue`(121) / `agents/usage`(124) / `harness/plugin`(229) / `harness/tooldridge`(311 行但子包反向依赖父包，典型该合的信号) 合并回父包。合并时改包名 + 改反向 import，逐个 commit。
- **P3 `agents/` 下子包名避免与父包同义** — 不建 `agents/agent` / `agents/agents`，避免 import 路径混淆。

#### 格式

- **G1 `gofmt -s` 强制** — `gofmt -l .` 输出必须为空。
- **G1.1 `goimports -l .` 强制** — `goimports -l .` 输出必须为空。
- **G2 `go vet ./...` 零警告** — vet 不通过不允许 commit。
- **G3 `golangci-lint` 聚合门** — `errcheck + govet + staticcheck + unused + ineffassign`，配置见 `src/darvin-agent/.golangci.yml`。
- **G3.1 `staticcheck` ST10xx 强制** — ST1000(包注释) / ST1003(initialism) / ST1005(错误字符串) / ST1006(receiver 名) / ST1019(import 重复) / ST1020-1023(exported godoc) 全 0 告警。
- **G4 Go 文件用 tab 缩进** — 不空格缩进。

#### 违规注释模式黑名单

| 模式 | 例 | 处理 |
|---|---|---|
| `Phase [0-9]` | `// Phase 5 default` | 删 |
| `FR-[0-9]+` | `// FR-4 implementation` | 删，必要时链接 spec 文档路径 |
| `D[0-9]+` | `// D10 archive` | 删 |
| `Reasonix` | `// Reasonix summaryTimeout` | 删，描述实际行为 |
| `v[0-9]+` / `S[0-9]+` | `// v0 placeholder` | 删 |
| 大范围 `TODO` | `// TODO: future migration` | 删或缩小到具体函数级 |

外部 spec 代号改写建议：用自然语言描述行为，或链接到 `specs/features/.../...md` 文档路径（相对仓库根）。

#### 落地工具

- `Makefile` target（位于 `src/darvin-agent/Makefile`）：
  - `fmt`：跑 `gofmt -s -w . && goimports -w -local darvin-cowork/ .`
  - `fmt-check`：`gofmt -l . && goimports -l .`（输出空为通过）
  - `vet`：`go vet ./...`
  - `lint-comments`：`staticcheck -checks 'ST10*' ./...`
  - `lint`：`golangci-lint run ./...`
  - `check`：聚合 `fmt-check + vet + lint-comments + lint + check-readability`
- `scripts/check-agent-readability.sh`：本规范的一键校验脚本（行数 / 注释密度 / 违规模式 / F3 / ST10xx / baseline 比对）。
- `.golangci-baseline.txt`：存量 lint 告警 baseline；新 PR 不允许新增同类告警。

## 遗留问题与小文件

仓库当前规模很小，但 webpack → vite 迁移刚完成，下面这些是已知的可清理项（**单独清理，不作为附带工作**）：

- `src/renderer/index.ts` 顶部 webpack 模板注释（仅注释，不影响功能）。
- `.bak/` 下备份的旧 `webpack.*.config.ts` / `webpack.plugins.ts` / `webpack.rules.ts`。可在确认 vite 路线彻底稳定后删除。
- `.gitignore` 末尾的 `.claude` 规则与 `.bak/` 是否入库需要一次明确决定。

涉及上述任一项时，**先列出计划再动**，不要顺手清。

## 分支、提交和 PR

- 分支命名：`feat/...` / `fix/...` / `chore/...` / `refactor/...`。
- **不要主动 commit**，**不要主动 broad refactor**，**不要写 `Co-Authored-By` 尾部**（仓库根 `CLAUDE.md` 顶部 `## 三句话总结` 第 2 条明文规定）。
- 提交信息遵循 Conventional Commits，英文：

```text
feat(runtime): spawn darvin-agent and pipe stdio
fix(forge): limit extraResources to current platform
chore: drop stale webpack templates from .bak
```

- PR：简洁描述 + 关联 issue（如有）+ UI 截图（若有 UI 改动）+ Electron 特定变更说明（IPC、preload、窗口、Go 二进制打包路径等）。

## 实践指导

- 优先用 `rg` 搜索；`Glob` / `Grep` 工具已就绪。
- 在动手前对照 `package.json` / `docs/系统架构.md` / 迁移 spec 验证历史叙述；老 spec 中的数字 / 路径可能在迁移后已变化。
- 忽略与 `package.json`、`forge.config.ts`、`vite.*.config.ts`、`src/main` 冲突的过时文档。
- **不要编辑打包产物**（`.vite/build/`、`out/`、`node_modules/`、`bin/darvin-agent-*`）和本地调试残留（`.bak/` / `.claude/` / `.playwright-cli/` / `*.db`），除非任务就是打包 / 运行时生成。
- 保持改动聚焦；修 bug 时不要做机会性重构。
- 如要拆 `src/main/index.ts`（当它真的长大），先给一个聚焦的拆分方案再动手。
- 文件里有无关的用户改动时，围着它们工作，不要还原。