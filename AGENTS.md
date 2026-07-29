# AGENTS.md

本文件为编码代理提供该仓库（`darvin-cowork`）的当前工作模型。当此处内容过时时，以 `package.json`、源代码为准。

## 指令范围

此根目录 `AGENTS.md` 是仓库级指南。子目录里目前没有更具体的覆盖文件，未来如果新增子级 `AGENTS.md` / `AGENTS.override.md`，更具体的指令覆盖更宽泛的指令。

不要把生成的运行时产物（`.vite/build/`、`out/`、`node_modules/`、`.bak/` 里备份的旧 webpack 配置）当作权威的项目指令。仅用作历史上下文，并对照当前源代码进行验证。

## 项目概览

`darvin-cowork` 是一款个人桌面智能助手。架构与近期演进方向如下（见 `docs/系统架构.md`）：

- **桌面壳**：Electron（`@electron-forge` + `@electron-forge/plugin-vite`）。
- **渲染层**：Vue3 + Tailwind CSS v4（`@tailwindcss/vite` 插件接入 Vite）；样式统一走 utility class，组件里不写裸 CSS。
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
         └─ startAgentRuntime()        spawn 子进程
              │
              ▼
         darvin-agent (Go, src/darvin-agent/)   ← 子进程，IPC 协议见 src/shared/darvin-api.ts
              │
              ▼
         AgentClient (src/main/runtime/client.ts)  ← IPC 客户端（S5 阶段实现）

preload (src/preload/index.ts)   ← contextBridge 暴露 window.darvin

renderer (src/renderer/)
    ├─ Vue3 应用 (createApp().mount('#app'))
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
- `src/darvin-agent/` 下放置 Go agent 源码（含 `go.mod` / `main.go`）。`scripts/build-go.js` 在该目录不存在时会打印警告并 `exit 0`，不会阻塞 Electron 启动。
- Go 模块名见 `src/darvin-agent/go.mod`。
- 主进程侧的 IPC 客户端 (`src/main/runtime/client.ts`)：`AgentClient` 接口 + `createAgentClient()` 抛 `Not Implemented`；具体协议见 `src/shared/darvin-api.ts` 的 `DarvinApi` / `DarvinEvent` 定义。

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

`src/preload/index.ts` 通过 `contextBridge.exposeInMainWorld('darvin', api)` 把 Go agent 客户端的方法以受限 API 暴露给 renderer；API 形状见 `src/shared/darvin-api.ts`。

### renderer

`src/renderer/` 是 Vite root（`root: 'src/renderer'`，`base: './'` 用于生产相对路径）。当前栈：

- **Vue3**：`index.ts` 已 `createApp(App).mount('#app')`，挂载点 `<div id="app">` 在 `index.html`。
- **Tailwind CSS v4**：通过 `@tailwindcss/vite` 插件接入 `vite.renderer.config.mts`；`index.css` 顶部 `@import "./styles/theme.css"; @import "./styles/reset.css";`，设计 token 统一走 `styles/theme.css` 的 `@theme` 块。样式优先用 utility class；仅在跨组件复用的设计 token（颜色 / 间距 / 字号）才用 `@theme` 抽象。
- **Artifact 渲染器**：见下文"编码风格"小节关于 `services/artifact-renderer/` 的约束。

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
     - `layout/` — 页面级布局壳（与 `components/` 平级），如 `AppShell.vue` / `SettingsLayout.vue`
     - `components/` — 可复用功能组件，按 feature 子目录拆分（`sidebar/` / `chat/` / `side-panel/`）
     - `components/common/` — 跨 feature 通用组件（`Icon` / `IconButton` / `Dropdown`）
     - `composables/` — 所有 `useXxx.ts` 单例状态
     - `services/` — 纯函数 / IPC 客户端 / mock 数据
     - `styles/` — 全局 CSS（`theme.css` + `reset.css`）
     - `assets/icons/` — SVG 图标源（自动 glob 加载）
7. **样式约束**：组件 `<template>` 内只用 utility class（`bg-bg` / `text-text-muted` 等），颜色全走 `@theme` token；不写 `<style>` 块。
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

颜色 / 间距 / 圆角 / 字号 / 动画 token 唯一来源：`src/renderer/styles/theme.css` 的 `@theme` 块。

- **颜色 token**（dark default）：
  - 基础：`bg` / `surface` / `surface-2` / `border` / `text` / `text-muted` / `text-subtle` / `accent` / `accent-hover`
  - 语义：`user-msg` / `assistant-msg` / `danger` / `success` / `warning`
- **浅色覆盖**：HTML 根 `<html class="light">` 触发；token 值在 `@layer base` 下被覆盖。**禁用** Tailwind `dark:` 变体（用 class 切换 + token 覆盖）。
- **圆角 token**：`sm` (4px) / `md` (6px) / `lg` (8px) / `xl` (12px)
- **间距 token**：`app-padding` (12px) / `section-gap` (16px)
- **字号 token**：`xs` (11px) / `sm` (13px) / `base` (14px) / `md` (15px) / `lg` (17px) / `xl` (20px)
- **字体**：`--font-sans` / `--font-mono`
- **动画**：`cursor-blink` 1s step-end infinite

**消费规则**：
- ✅ `bg-bg` / `text-text-muted` / `border-border` / `rounded-md` / `text-sm`
- ❌ `bg-[#1a1a1a]` / `text-gray-300` / `bg-red-500` / `style={{ color: 'red' }}` / padding: `12px` 等 magic value

新加 token：先在 `theme.css` 的 `@theme` 块加 `--color-foo` / `--spacing-bar` / `--text-foo` 等；然后 `@layer base` 下加浅色覆盖；**然后** `bg-foo` / `text-foo` utility 自动可用。

### 图标系统

**只用 SVG**，不引入 `lucide-vue-next` / `@heroicons/vue` / `naive-ui` 等图标库。

- 源文件：`src/renderer/assets/icons/*.svg`，分两组：
  - **A 组（Chat UI）**：11 个，本仓库已生成（`plus` / `sun` / `moon` / `menu` / `panel-right-close` / `panel-right-open` / `send` / `chevron-down` / `cog` / `alert-circle` / `check`）；统一 `viewBox="0 0 34 34"` + `stroke="currentColor"` + `stroke-width="2.4"` + round caps
  - **B 组（用户中心预留）**：5 个（`invite-credits` / `logout` / `promo-subscription` / `recharge` / `usage-overview`），由用户导入，含 `stroke="black"` 硬编码；`Icon` 组件加载时一次 `replace("stroke=\"black\"", "stroke=\"currentColor\"")` 转为 currentColor
- 加载：`src/renderer/assets/icons/index.ts` 用 `import.meta.glob('./*.svg', { eager: true, query: '?raw', import: 'default' })` 全部 inline 加载
- 消费：`<Icon name="send" />` 全局组件（`<script setup>` 注册到 `app.component('Icon', Icon)`）
- 命名：`kebab-case`：`<Icon name="chevron-down" />` / `<Icon name="panel-right-close" />`
- 缺失 icon：组件警告 + 渲染空 16×16 占位（不抛错）

**新增 icon 规则**：
- 丢到 `src/renderer/assets/icons/<name>.svg` 即可，无需 import（自动 glob）
- **必须**用 `stroke="currentColor"`；不要写死颜色 / 用 `fill="black"` 等
- 尺寸走 `viewBox` 不写死 `width` / `height`（除非用户明确指定）；`Icon` 组件通过 `:size` prop 注入实际渲染尺寸

### 字体

renderer 允许从 `fonts.googleapis.com` 加载 3 套字体（Fraunces / Inter Tight / JetBrains Mono），由 `src/renderer/index.html` 的 `<link>` 引入；token 名称 `--font-display` / `--font-sans` / `--font-mono` 与 fallback 链（`ui-serif` / `-apple-system` / `ui-monospace`）写在 `src/renderer/styles/theme.css`。**禁止**引入其他 CDN 资源（图标库 / 分析脚本 / 字体 CDN 都不行）。如需离线/合规场景，应改成自托管 woff2 而不是新加 CDN。

### 注释规范

注释描述代码做什么 / 怎么用，**不写阶段、版本、后续路线**。前后端一致。

- ❌ 禁用的注释形态
  - 阶段 / 版本号：`// S1 阶段` `// S5 替换为` `// v0 returns nil` `// v0 TODO seam` `// 后续 S 阶段按需`
  - 占位 / 待定 / 未来：`// 占位：后续接入子进程 spawn` `// 未来要走 contextBridge` `// IPC 协议待定` `// 当前仍是占位`
  - Roadmap 暗示：`// 真实 S5 阶段替换为 AgentClient.request()` `// 真实实现留 future spec` `// the Skills spec will populate it`
- ✅ 允许的注释形态
  - 导出 API / 公共函数的 JSDoc（描述输入、输出、约束、不变量）
  - 单行说明代码意图（避免读者必须理解上下文才能知道为什么这么写）
  - 设计约束（`event 形状必须与 DarvinEvent union 一致` `CreatedAt is overwritten with time.Now() if zero`）
- 标识符命名同样适用：避免 `ErrNotImplementedInV0` / `FixForV2` / `MockS5` 这类把版本号塞进 API 名字的做法

注释之外，**文档**（`docs/`、`specs/`、`AGENTS.md` 本体）允许讨论阶段、版本、roadmap——本规则只针对源码注释。

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
