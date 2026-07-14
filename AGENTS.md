# AGENTS.md — darvin-cowork 项目说明书

> 给 AI coding 助手看的项目说明（中文）。
> 子目录还有更具体的 AGENTS.md（frontend / frontend/main / frontend/preload / frontend/renderer / backend）。

## 1. 项目快照

darvin-cowork 是一个 **Electron + React + Go** 桌面应用原型。前端用 **electron-vite** 统一构建三层（main / preload / renderer）。架构：

```
┌──────────────────────────────────────────────────────────┐
│ Electron 主进程  frontend/main/index.ts（TypeScript）     │
│  ├─ BrowserWindow + 加载 React 渲染层                     │
│  ├─ contextBridge 通过 frontend/preload/index.ts 桥接     │
│  └─ spawn Go 后端进程                                     │
│         ↓                                                 │
│  ┌────────────────────────────────────────────────────┐  │
│  │ Go HTTP 后端 (backend/cmd/server)                  │  │
│  │  - stdlib net/http                                 │  │
│  │  - 监听 127.0.0.1:8080                             │  │
│  └────────────────────────────────────────────────────┘  │
│         ↑ HTTP /api/*                                     │
│  ┌────────────────────────────────────────────────────┐  │
│  │ React 渲染层  frontend/renderer/                    │  │
│  │  - Vite + React 19 + Tailwind                       │  │
│  │  - dev URL（HMR）/ loadFile（prod）                 │  │
│  └────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────┘
```

跨进程边界：

- **Electron main → renderer**：`mainWindow.loadURL(ELECTRON_RENDERER_URL)`（dev，由 electron-vite 注入）/ `loadFile(frontend/out/renderer/index.html)`（prod）
- **renderer → Go**：dev 期走 electron-vite proxy（`/api/* → http://127.0.0.1:8080`），生产期直连
- **Electron main → Go**：`spawn('go', ['run', './cmd/server'])`（dev）/ 编译后的二进制（prod），由 Electron 主进程全权管理生命周期

## 2. 命令速查

```bash
# 日常开发（electron-vite 统一管 main + preload + renderer 三层 + 启 Electron）
npm run dev

# 单独跑 Go 后端（127.0.0.1:8080）
npm run start:go

# 构建 + 跑生产
npm run build && npm start

# 质量门禁
npm test                 # Vitest 前端测试
npm run test:go          # go test ./...
npm run lint             # oxlint 全量
npm run lint:fix         # oxlint --fix
npm run format           # prettier --write
npm run format:check     # prettier --check
npm run check            # lint + format:check + test + test:go（提交前必跑）
```

`npm run dev` = `electron-vite dev frontend`，一条命令同时编译 main / preload / renderer 三层并启 Electron 窗口，**不再**有 `dev:web` / `dev:electron` / `build:react` 拆分命令。

## 3. 目录结构

```
darvin-cowork/
├── AGENTS.md                  ← 你在这里
├── CLAUDE.md                  ← 短入口
├── package.json               ← 主入口 + scripts（electron-vite）
├── .oxlintrc.json             ← Oxlint 规则
├── .prettierrc.json           ← Prettier 配置
├── .prettierignore
├── commitlint.config.cjs      ← Commit 消息规则
├── .lintstagedrc.json         ← pre-commit 钩子行为
├── .husky/                    ← Git 钩子
├── specs/                     ← spec-driven 开发文档
├── shared/
│   └── constants.ts           ← 跨进程常量（IPC 频道 / 状态码）
├── docs/                      ← 研究文档
├── resources/                 ← 图标 / 默认配置（v0.1 第 4 周填）
│
├── frontend/                  ← electron-vite 工作区
│   ├── AGENTS.md              ← frontend 总入口
│   ├── electron.vite.config.ts ← 一份配置管三层
│   ├── tsconfig.json          ← 项目引用根
│   ├── tsconfig.node.json     ← main / preload 共用
│   ├── package.json           ← frontend 工作区依赖
│   │
│   ├── main/                  ← Electron 主进程（TS）
│   │   ├── AGENTS.md          ← 主进程专属
│   │   ├── index.ts           ← 主入口
│   │   ├── window.ts          ← createWindow / getMainWindow
│   │   ├── ipc.ts             ← registerIpc
│   │   └── go.ts              ← startGo / waitForGo / killGo
│   │
│   ├── preload/               ← 预加载脚本（TS）
│   │   ├── AGENTS.md          ← preload 专属
│   │   └── index.ts           ← contextBridge.exposeInMainWorld('api', ...)
│   │
│   └── renderer/              ← React 渲染层
│       ├── AGENTS.md          ← 渲染层专属
│       ├── index.html
│       ├── vitest.config.ts
│       ├── tsconfig.json
│       ├── tsconfig.app.json
│       ├── tsconfig.node.json
│       └── src/
│           ├── main.tsx
│           ├── App.tsx
│           ├── pages/
│           ├── components/
│           ├── services/      ← i18n / 业务封装
│           ├── api/           ← 后端调用（hello.ts / request.ts）
│           ├── locales/       ← zh.json / en.json
│           ├── assets/
│           ├── store/         ← Zustand 入口（v0.1 第 2-3 周）
│           ├── utils/
│           ├── types/global.d.ts  ← window.api 类型声明
│           └── shared/        ← symlink → /shared
│
├── backend/                   ← Go 后端
│   ├── AGENTS.md
│   ├── go.mod
│   ├── cmd/server/main.go     ← 入口
│   ├── internal/
│   │   ├── agent/             ← Agent 推理调度引擎
│   │   ├── memory/            ← 四层记忆系统
│   │   ├── tools/             ← 工具调用
│   │   ├── mcp/               ← MCP 协议客户端
│   │   ├── gateway/           ← OpenClaw Gateway 客户端
│   │   └── config/            ← 配置集中入口（ListenAddr / HttpBasePath）
│   └── pkg/                   ← 可被外部 import 的公共库
│
└── (frontend/out/)            ← electron-vite 产物（gitignore，不入库）
```

## 4. 质量门禁

改任何代码前确认跑过：

| 改了什么                            | 必须跑                                               |
| ----------------------------------- | ---------------------------------------------------- |
| TS / TSX 文件                       | `npm run lint`                                       |
| Go 文件                             | `cd backend && go build ./... && go test ./...`      |
| 跨进程边界（Electron ↔ React ↔ Go） | 上面两条 + `npm run build` + 手动 `npm run dev` 验证 |
| 共享常量                            | 上面三条                                             |

提交前必须 `npm run check` 全绿。

## 5. 约定纪律

### 5.1 i18n

**绝不**硬编码面向用户的字符串。用 `t('key')`，key 必须**同时**存在于 `frontend/renderer/src/locales/zh.json` 和 `en.json`。缺一个不能 commit。

### 5.2 常量

discriminant / 状态码 / IPC 频道名一律走 `as const` 中心化：

```ts
export const SessionStatus = {
  Active: 'active',
  Done: 'done',
  Failed: 'failed',
} as const;
export type SessionStatus = (typeof SessionStatus)[keyof typeof SessionStatus];
```

一次性错误消息、CSS 类名、HTML 属性不需要常量化。

### 5.3 Commit

- 格式：`<type>(<scope>): <subject>`（Conventional Commits）
- type：`feat` / `fix` / `chore` / `docs` / `refactor` / `test` / `perf` / `ci` / `build` / `style`
- subject 长度 ≤ 100 字符
- 不加 `Co-Authored-By` 除非用户明确要求
- subject 用英文写

### 5.4 分支

- `feat/<short-desc>` 或 `fix/<short-desc>`
- 不用 `codex/...` / `claude/...` 前缀（除非用户明确说）

### 5.5 不要做

- 不要 broad refactor（除非用户明确说）
- 不要碰历史大文件做 drive-by 清理
- 不要主动 commit（除非用户明确说）
- 不要为假设的未来需求写抽象
- 不要把调试代码 / console.log 留在主路径
- 不要在源码中保留注释掉的旧代码

## 6. AI 协作模式

| 场景                  | 做法                                                                                                                                                                                               |
| --------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 不熟悉的代码 / 找文件 | 用 `Explore` 子 agent                                                                                                                                                                              |
| 跨多文件架构决策      | 用 `Plan` 子 agent                                                                                                                                                                                 |
| 简单单文件改动        | 直接做                                                                                                                                                                                             |
| 改 Electron 主进程    | 跑 `npm run build` + 手动 `npm run dev` 验证窗口能起来                                                                                                                                             |
| 改 Go 后端            | 改完跑 `go build ./...` + `go test ./...`（详见 `backend/AGENTS.md`）                                                                                                                              |
| 改 React 渲染层       | 改完跑 `npm test`（相关测试）+ `npm run dev` 看 HMR（路径都在 `frontend/renderer/src/`）                                                                                                           |
| 加新 IPC 频道         | 四步：① `shared/constants.ts` 加 `IpcChannel.Xxx` 常量；② `frontend/preload/index.ts` 暴露 `window.api.xxx`；③ `frontend/main/ipc.ts` 注册 `ipcMain.handle`；④ renderer 用 `window.api.xxx()` 调用 |
| 加 Go HTTP endpoint   | 看 `backend/AGENTS.md` → "新增 handler"                                                                                                                                                            |
| 加新 i18n key         | `frontend/renderer/src/locales/{zh,en}.json` **同时**加                                                                                                                                            |
| 写多文件改动          | 先列出计划，等用户确认后再动                                                                                                                                                                       |

## 7. 常见任务指向

- **加 Go HTTP endpoint** → 看 `backend/AGENTS.md` → "新增 handler"
- **加 React 页面** → 看 `frontend/renderer/AGENTS.md` → "新增页面"
- **加 IPC 频道** → 看 `frontend/main/AGENTS.md` → "新增 IPC"
- **改 Vite 配置** → `frontend/electron.vite.config.ts`（三层统一配置）
- **改 Electron 启动行为** → `frontend/main/index.ts`

## 8. 不可逆操作清单（要先问用户）

- `git push` 到远端
- `git reset --hard` / `git push --force`
- 删除文件 / 目录
- 装全局 npm / go 包
- 修改 `package.json` 的 `engines` / Node 版本要求
- 修改打包配置
- 改 Go 模块路径
- 改 git remote

## 9. 文档过期处理

**以源码为准**。文档写错 / 过时，AI 应该：

1. 先在源码里验证
2. 在 PR 里顺手修文档
3. 不要盲信文档
