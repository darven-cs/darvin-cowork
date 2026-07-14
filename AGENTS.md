# AGENTS.md — darvin-cowork 项目说明书

> 给 AI coding 助手看的项目说明（中文）。
> 子目录还有更具体的 AGENTS.md（frontend / backend / electron）。

## 1. 项目快照

darvin-cowork 是一个 **Electron + React + Go** 桌面应用原型。架构：

```
┌────────────────────────────────────────────┐
│ Electron 主进程 (Node.js, electron/main.js) │
│  ├─ BrowserWindow + 加载 React 渲染层       │
│  └─ spawn Go 后端进程                       │
│         ↓                                  │
│  ┌──────────────────────────────────────┐  │
│  │ Go HTTP 后端 (backend/cmd)           │  │
│  │  - stdlib net/http                   │  │
│  │  - 监听 127.0.0.1:8080                │  │
│  └──────────────────────────────────────┘  │
│         ↑ HTTP /api/*                      │
│  ┌──────────────────────────────────────┐  │
│  │ React 渲染层 (frontend/)              │  │
│  │  - Vite + React 18 + Tailwind         │  │
│  │  - 5175 端口 (dev) / dist-react (prod) │  │
│  └──────────────────────────────────────┘  │
└────────────────────────────────────────────┘
```

跨进程边界：

- **Electron → React**：`mainWindow.loadURL(5175)`（开发）/ `loadFile(dist-react/index.html)`（生产）
- **React → Go**：开发期走 Vite proxy（`/api/* → 127.0.0.1:8080`），生产期直连
- **Electron → Go**：`child_process.spawn('go', ['run', './cmd'])`，由 Electron 全权管理生命周期

## 2. 命令速查

```bash
# 日常开发（Vite HMR + Electron + Go 全开）
npm run dev

# 单独跑
npm run dev:web          # 只起 Vite (5175)
npm run dev:electron     # 只起 Electron（要 Vite 已经起来）
npm run start:go         # 只跑 Go 后端（127.0.0.1:8080）

# 构建 + 跑生产
npm run build:start      # build React → 启 Electron 加载打包文件

# 质量门禁
npm test                 # Vitest 前端测试
npm run test:go          # go test ./...
npm run lint             # oxlint 全量
npm run lint:changed     # oxlint 只 lint 改过的文件
npm run format           # prettier --write
npm run format:check     # prettier --check
npm run check            # lint + format:check + test + test:go（提交前必跑）
```

## 3. 目录结构

```
darvin-cowork/
├── AGENTS.md              ← 你在这里
├── CLAUDE.md              ← 短入口
├── package.json
├── .oxlintrc.json         ← Oxlint 规则
├── .prettierrc.json       ← Prettier 配置
├── commitlint.config.cjs  ← Commit 消息规则
├── .lintstagedrc.json     ← pre-commit 钩子行为
├── .husky/                ← Git 钩子
├── electron/              ← Electron 主进程
│   ├── AGENTS.md
│   ├── main.js
│   ├── preload.js         ← TODO
│   └── dist-react/        ← Vite 打包产物（gitignore）
├── frontend/              ← React 渲染层
│   ├── AGENTS.md
│   ├── src/
│   │   ├── pages/
│   │   ├── components/
│   │   ├── services/
│   │   │   ├── cowork.ts
│   │   │   └── i18n.ts
│   │   ├── locales/
│   │   │   ├── zh.json
│   │   │   └── en.json
│   │   └── shared/        ← symlink 到 /shared
│   ├── vite.config.ts
│   └── package.json
├── backend/               ← Go 后端
│   ├── AGENTS.md
│   ├── cmd/main.go        ← 入口
│   ├── internal/<domain>/ ← 业务包
│   └── go.mod
├── shared/                ← 跨进程常量（TypeScript）
│   └── constants.ts
└── docs/
    └── study/             ← 对 LobsterAI 的研究文档
```

## 4. 质量门禁

改任何代码前确认跑过：

| 改了什么                            | 必须跑                                               |
| ----------------------------------- | ---------------------------------------------------- |
| TS / TSX 文件                       | `npm run lint:changed`                               |
| Go 文件                             | `cd backend && go build ./... && go test ./...`      |
| 跨进程边界（Electron ↔ React ↔ Go） | 上面两条 + `npm run build` + 手动 `npm run dev` 验证 |
| 共享常量                            | 上面三条                                             |

提交前必须 `npm run check` 全绿。

## 5. 约定纪律

### 5.1 i18n

**绝不**硬编码面向用户的字符串。用 `t('key')`，key 必须**同时**存在于 `frontend/src/locales/zh.json` 和 `en.json`。缺一个不能 commit。

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

| 场景                  | 做法                                                                     |
| --------------------- | ------------------------------------------------------------------------ |
| 不熟悉的代码 / 找文件 | 用 `Explore` 子 agent                                                    |
| 跨多文件架构决策      | 用 `Plan` 子 agent                                                       |
| 简单单文件改动        | 直接做                                                                   |
| 改 Electron 主进程    | 改完跑 `npm run dev` 验证窗口能起来                                      |
| 改 Go 后端            | 改完跑 `go build ./...` + `go test ./...`                                |
| 改 React 渲染层       | 改完跑 `npm test`（相关测试）+ `npm run dev` 看 HMR                      |
| 加新 IPC 频道         | 先在 `shared/constants.ts` 加常量，再 preload + main + renderer 三处引用 |
| 加新 i18n key         | `zh.json` 和 `en.json` **同时**加                                        |
| 写多文件改动          | 先列出计划，等用户确认后再动                                             |

## 7. 常见任务指向

- **加 Go HTTP endpoint** → 看 `backend/AGENTS.md` → "新增 handler"
- **加 React 页面** → 看 `frontend/AGENTS.md` → "新增页面"
- **加 IPC 频道** → 看 `electron/AGENTS.md` → "新增 IPC"
- **改 Vite 配置** → `frontend/vite.config.ts`
- **改 Electron 启动行为** → `electron/main.js`

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
