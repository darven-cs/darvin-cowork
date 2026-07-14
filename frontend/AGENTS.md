# frontend/AGENTS.md — Electron + React 工作区

> frontend 工作区总入口。本目录由 **electron-vite** 统一编译三层，命令在根 `package.json` 里跑。

## 技术栈

- Electron 43+
- TypeScript（主进程 + preload + renderer 全 TS）
- electron-vite 6 beta（dev / build / preview 一体化）
- React 19 + Vite 8（renderer 层）
- Tailwind CSS

## 三层结构

| 目录        | 职责                                                               | 详细规则             |
| ----------- | ------------------------------------------------------------------ | -------------------- |
| `main/`     | Electron 主进程：BrowserWindow、IPC handler、Go 子进程生命周期     | `main/AGENTS.md`     |
| `preload/`  | 预加载脚本：`contextBridge.exposeInMainWorld('api', ...)` 安全桥接 | `preload/AGENTS.md`  |
| `renderer/` | React 渲染层：页面、组件、状态、API 调用                           | `renderer/AGENTS.md` |

## 命令

所有命令在根 `package.json`，**不在** frontend/ 单独跑：

```bash
npm run dev      # electron-vite dev frontend（三层统一 HMR + 启 Electron 窗口）
npm run build    # electron-vite build frontend（产物在 frontend/out/{main,preload,renderer}/）
npm run preview  # electron-vite preview frontend（跑构建产物）
npm test         # vitest run --config frontend/renderer/vitest.config.ts
```

## electron.vite.config.ts

**一份配置**管三层（`main` / `preload` / `renderer`），不要在 main/preload/renderer 下各自建 `vite.config.ts`。

关键段：

- `main.plugins`：`externalizeDepsPlugin()`（把 `electron` 等依赖外置）
- `preload.plugins`：同上
- `renderer.root`：`'renderer'`
- `renderer.server.proxy`：dev 期 `/api → http://127.0.0.1:8080`

修改配置后跑 `npm run dev` 验证三层都正常。

## 产物

`frontend/out/` 由 electron-vite 生成，**不要入库**（已在 `.gitignore` / `.prettierignore` / `.oxlintrc.json` 的 ignorePatterns）。

- `frontend/out/main/index.js` ← 根 `package.json` 的 `main` 字段指向这里
- `frontend/out/preload/index.js`
- `frontend/out/renderer/index.html` ← 生产期 Electron `loadFile` 加载

## 不要做

- 不要在 `main/` `preload/` `renderer/` 下各自建 `vite.config.ts` / `tsconfig.json`（除已指定的）
- 不要直接 `npm install` 到 frontend 工作区（依赖在根 `package.json`）
- 不要在主进程 / preload 里 import 渲染层代码（跨边界）
