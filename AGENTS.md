# AGENTS.md

本文件为编码代理提供该仓库（`darvin-cowork`）的当前工作模型。当此处内容过时时，以 `package.json`、源代码为准。

## 指令范围

此根目录 `AGENTS.md` 是仓库级指南。子目录里目前没有更具体的覆盖文件，未来如果新增子级 `AGENTS.md` / `AGENTS.override.md`，更具体的指令覆盖更宽泛的指令。

不要把生成的运行时产物（`.vite/build/`、`out/`、`node_modules/`、`.bak/` 里备份的旧 webpack 配置）当作权威的项目指令。仅用作历史上下文，并对照当前源代码进行验证。

## 项目概览

`darvin-cowork` 是一款个人桌面智能助手，原型阶段。架构与近期演进方向如下（见 `docs/系统架构.md`）：

- **桌面壳**：Electron（`@electron-forge` + `@electron-forge/plugin-vite`）。
- **渲染层**：目标栈 Vue3 + Tailwind CSS v4（`@tailwindcss/vite` 插件接入 Vite）；样式统一走 utility class，组件里不写裸 CSS。**当前仍是 vanilla TS + HTML 占位（`Hello World!`）**，按上述栈迁移中（详见 `docs/系统架构.md` 的“前/后端”段）。
- **AI 产物预览沙箱 (Artifact 渲染器)**：`src/renderer/services/artifact-renderer/`，自建一个 sandboxed iframe 渲染器（参考 Claude.ai Artifacts 形态），用于隔离渲染 AI 生成的 HTML / SVG / Mermaid / React 组件 / 代码块等产物，支持交互；不要把 AI 原始产物直接塞进主页面 DOM。
- **Agent runtime**：Go 编写的 `darvin-agent`，作为 Electron 主进程的子进程运行（`src/main/runtime/manager.ts` → spawn `bin/darvin-agent-<platform>-<arch>`）。
- **主进程**：仅负责 Electron 生命周期 + 启动 Go 子进程；业务逻辑（agent 循环、工具调用、记忆、skills、MCP、上下文压缩、模型切换等）全部下放到 Go 运行时。
- **构建产物打包**：通过 `extraResources` 把 `bin/` 下当前平台的 Go 二进制随 `out/` 资源一起分发，置于 `Resources/bin/`（不进 asar，因为 spawn 需要可执行权限），由 `forge.config.ts` 的 filter 只打当前平台。

### 架构图

```
Electron 主进程 (src/main/index.ts)
    ├─ Electron 生命周期、BrowserWindow
    └─ RuntimeManager (src/main/runtime/manager.ts)
         ├─ resolveAgentBinaryPath()   解析 darvin-agent 二进制位置
         └─ startAgentRuntime()        spawn 子进程（占位：当前仅 console.log）
              │
              ▼
         darvin-agent (Go, src/darvin-agent/)   ← 子进程，IPC 协议待定
              │
              ▼
         AgentClient (src/main/runtime/client.ts)  ← 占位接口，待协议确定后实现

preload (src/preload/index.ts)   ← 当前为空占位；后续经 contextBridge 暴露给 renderer

renderer (src/renderer/)
    ├─ Vue3 应用 (createApp().mount('#app'))   ← 当前 Hello World 占位，按目标栈迁移
    ├─ Tailwind CSS v4 (@tailwindcss/vite)     ← 样式统一走 utility class
    └─ Artifact 渲染器 (services/artifact-renderer/)  ← sandboxed iframe, 隔离 AI 产物
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

# ESLint：src 全部 .ts/.tsx（注意：与 LobsterAI 不同，本仓库目前还没有 Vitest 跑通）
npm run lint
```

要求：
- Node.js `>=20`（具体看 `@types/node ^20.11.0` 与 `electron 43.2.0` 兼容性矩阵）。
- Go agent 构建需要本机 `go` 工具链；`scripts/build-go.js` 会设置 `CGO_ENABLED=0` 强制静态。
- Windows 下若安装时涉及 shortcuts 由 `electron-squirrel-startup` 处理；当前主进程已包含该 guard。

Go agent 相关：
- `src/darvin-agent/` 目前只有 `README.md`（尚无 `go.mod` / `main.go`，属于骨架占位）。`scripts/build-go.js` 在该目录不存在时会打印警告并 `exit 0`，不会阻塞 Electron 启动。
- Go 模块名见 `src/darvin-agent/README.md`，指向 `specs/refactors/electron-webpack-to-electron-forge-vite/2026-07-27-webpack-to-electron-forge-vite-design.md` 4.7 节（待定）。
- 主进程侧的 IPC 客户端占位 (`src/main/runtime/client.ts`)：`AgentClient` 接口 + `createAgentClient()` 抛 `Not Implemented`；后续要补充 JSON-RPC / protobuf / 自定义协议三者之一。

## 测试

当前仓库**尚未配置测试运行器**——`package.json` 没有 `test` 脚本，`node_modules` 里也没有 vitest / jest。不要假设测试可用：

- 修改逻辑时不要新增依赖隐式测试覆盖。
- CI 入口为 `npm run lint`。
- 真正落地功能时，应在 `package.json` 加 `test` 脚本（建议 Vitest，与 Electron 主进程解耦测试逻辑函数），并把测试文件放在被测源码旁 `*.test.ts`。

针对 UI / Electron 行为，目前只能通过 `npm start` 手动验证窗口与 DevTools。

## 质量门槛

ESLint 配置在 `.eslintrc.json`：基于 `eslint:recommended` + `@typescript-eslint/recommended` + `import/recommended` + `import/electron` + `import/typescript`。

```bash
# 全量 lint（src 内全部 .ts/.tsx）
npm run lint

# 仅 lint 受影响的文件（与 CI 一致）
npx eslint --ext .ts,.tsx <files>
```

验证期望：
- **文档 / 配置变更**：不需要跑 lint；说明“仅改文档 / 配置”即可。
- **主进程 / preload / runtime 改动**：跑 `npm run lint`，并用 `npm start` 手动起一次确认窗口能开、Go agent 解析路径符合预期。
- **renderer 改动**：跑 `npm run lint`，并通过 `npm start` 在 DevTools 里观察 console。
- **Go 端改动**：在 `src/darvin-agent/` 下用 `go build` / `go vet` 本地验证；若改了 `scripts/build-go.js`，同步跑 `npm run build:agent`。
- **forge.config.ts / vite.* 改动**：必须先 `npm start` 验证 HMR / 构建流程没坏，再考虑打 `npm run package`。
- **交接前**：检查 diff 里有没有无关改动、风险性大的重构、生成的产物（`.vite/`、`out/`、`bin/darvin-agent-*`）以及用户可见的字符串错误。

## 架构要点

### 主进程

`src/main/index.ts`：
- 处理 `electron-squirrel-startup` 短路；
- `createWindow()` 创建 `BrowserWindow`，`webPreferences.preload` 指向 `path.join(__dirname, '../preload/index.js')`（Vite 产物路径）；
- 开发态 `loadURL(MAIN_WINDOW_VITE_DEV_SERVER_URL)`，生产 `loadFile(path.join(__dirname, '../renderer/${MAIN_WINDOW_VITE_NAME}/index.html'))`；
- DevTools 当前默认打开（`webContents.openDevTools()`）；
- `window-all-closed` 在 macOS 外退出；`activate` 时若无窗口则重建。

`src/main/runtime/manager.ts`：
- `resolveAgentBinaryPath()`：根据 `app.isPackaged` 选择 `process.resourcesPath/bin/...`（生产） 或 `__dirname` 回溯三级的 `bin/...`（开发）。不存在时返回 `undefined`，由 `startAgentRuntime()` 打 warning 而不是抛错。
- `startAgentRuntime()`：当前仅 `console.log('[runtime] would start: ${bin}')`；TODO 是 `spawn(bin, [], { stdio: 'pipe' })` + 与 `client.ts` 配合。

`src/main/runtime/client.ts`：
- 接口占位 `AgentClient { connect; disconnect }`；
- `createAgentClient()` 抛 `Not Implemented`；
- 落地后需补 `request<T>(method, params): Promise<T>` / `send<T>(event, payload): void` / `on<T>(event, handler): Unsubscribe`。

### preload

`src/preload/index.ts` 当前仅一行注释占位。未来要走 `contextBridge.exposeInMainWorld(...)`，把 Go agent 客户端的方法（request / send / on）以受限 API 暴露给 renderer。

### renderer

`src/renderer/` 是 Vite root（`root: 'src/renderer'`，`base: './'` 用于生产相对路径）。目标栈：

- **Vue3**：把 `index.ts` 改为 `createApp(...).mount('#app')` 并在 `index.html` 加挂载点。
- **Tailwind CSS v4**：通过 `@tailwindcss/vite` 插件接入 `vite.renderer.config.ts`；在 `index.css` 顶部 `@import "tailwindcss";` 即可，不要再单独建 `tailwind.config.js`（v4 走 CSS-based config，需要时再补 `@theme` 块）。样式优先用 utility class；仅在跨组件复用的设计 token（颜色 / 间距 / 字号）才用 `@theme` 抽象。
- **Artifact 渲染器**：见下文"编码风格"小节关于 `services/artifact-renderer/` 的约束。

当前仍是 Hello World 占位：

- `index.html` 内 `<script type="module" src="./index.ts">` —— 已被 vite 接管。
- `index.ts` 顶部仍残留 webpack 模板的注释（`loaded by webpack`），属于 `electron-forge-webpack` 模板遗迹，建议清理但与功能无关，**不是阻塞项**。
- `index.css` 占位（后续 Tailwind 接入后这里就只放 `@import` + 少量全局样式）。

### Go agent

`src/darvin-agent/` 是 Go 源码落点（当前仅 README）。`scripts/build-go.js`：

- 输出 `<repo>/bin/darvin-agent-<platform>-<arch><.exe?>`；
- 设置 `CGO_ENABLED=0`、`GOOS=process.platform`、`GOARCH=process.arch`；
- `cwd: src/darvin-agent`，`go build -ldflags="-s -w" -o <out> .`；
- 捕获 `err.message + err.status`，失败非零退出，被 `npm run make` 的 `premake` 钩子传播。

`forge.config.ts` 的 `extraResources.filter` 仅保留 `darvin-agent-<platform>-<arch>(.exe)` 与 `.gitkeep`，避免 dev 机器把 darwin+linux+win 全打进去。

## 字符串常量

**目前 IPC 通道、模式名、判别值尚未落地**（主进程除 Go agent 启动外无业务逻辑）。一旦开始接入：

```ts
export const AgentChannel = {
  Connect: 'agent:connect',
  Disconnect: 'agent:disconnect',
  Request: 'agent:request',
  Event: 'agent:event',
} as const;
export type AgentChannel = typeof AgentChannel[keyof typeof AgentChannel];
```

- 模块一源；
- 同时导出值对象 + 类型；
- 测试断言使用同一常量；
- 一次性错误消息、CSS class、HTML attribute、外部平台 ID 不用常量化。

## 国际化

目前**还没有 UI 字符串**（Hello World 不算）。一旦接入 Vue3 / 实现功能：

- renderer 侧：在 `src/renderer/services/i18n.ts` 提供 `t('key')`，同时维护 `zh` 与 `en` 字典；
- 主进程侧：托盘 / 菜单 / 窗口标题 / 通知使用 `src/main/i18n.ts` 的 `t()`；
- 仅开发者可见的日志、DevTools 诊断不强制 i18n。

## 编码风格

- TypeScript 是默认语言。`tsconfig.json` 已 `strict + noImplicitAny`。
- React / Vue 组件（未来）：函数组件 + Composition API，优先使用 `<script setup>`。
- 2 空格缩进、单引号、分号；ESLint 规则按现有 `.eslintrc.json` 为准。
- 文件命名沿用 Vite 默认（保留现有 `index.ts`），新模块按职责拆 `PascalCase` 组件 / `camelCase` 工具函数。
- 业务逻辑保持在 `src/main/libs/`、`src/main/runtime/`、`src/darvin-agent/` 等模块中，不要塞进 UI 组件。
- 优先使用现有模式与本地 helper，不要为了新加一个功能造新抽象。
- **样式 (Tailwind CSS v4)**：组件内优先用 utility class，**不要写裸 `<style>` 或组件级 CSS 文件**（除 `index.css` 的 `@import "tailwindcss"` + 极少量全局 reset / 字体外）。跨组件复用的设计 token 走 `@theme` 块，不要散落成 magic value。新增交互态/动效用 Tailwind 自带的 `hover:` / `focus:` / `dark:` 等变体，不要为单个 hover 写新 CSS。
- **Artifact 渲染器 (`src/renderer/services/artifact-renderer/`)**：AI 生成的产物（HTML / SVG / Mermaid / React / 代码块）**一律走 sandboxed iframe**，iframe 必须 `sandbox="allow-scripts"` 起跳、按需 `allow-same-origin`（仅在需要 DOM API 时打开），不与主页面共享 DOM。渲染器对外只暴露受控 API（`mount(artifact, container)` / `update(payload)` / `destroy()`），不把 iframe `contentWindow` 直接透出。产物源（来自 Go agent 的 IPC payload）须按类型分派（html / react / mermaid / svg / code），不要 `innerHTML` 注入主页面。

## 遗留问题与小文件

仓库当前规模很小，但 webpack → vite 迁移刚完成，下面这些是已知的可清理项（**单独清理，不作为附带工作**）：

- `src/renderer/index.ts` 顶部 webpack 模板注释（仅注释，不影响功能）。
- `.bak/` 下备份的旧 `webpack.*.config.ts` / `webpack.plugins.ts` / `webpack.rules.ts`。可在确认 vite 路线彻底稳定后删除。
- `.gitignore` 末尾的 `.claude` 规则与 `.bak/` 是否入库需要一次明确决定。

涉及上述任一项时，**先列出计划再动**，不要顺手清。

## 分支、提交和 PR

- 分支命名：`feat/...` / `fix/...` / `chore/...` / `refactor/...`。
- **不要主动 commit**，**不要主动 broad refactor**，**不要写 `Co-Authored-By` 尾部**（仓库根 `CLAUDE.md` 第 13 行明文规定）。
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
- **不要编辑打包产物**（`.vite/build/`、`out/`、`node_modules/`）和生成的 `bin/darvin-agent-*` 二进制，除非任务就是打包 / 运行时生成。
- 保持改动聚焦；修 bug 时不要做机会性重构。
- 如要拆 `src/main/index.ts`（当它真的长大），先给一个聚焦的拆分方案再动手。
- 文件里有无关的用户改动时，围着它们工作，不要还原。
