# frontend/main/AGENTS.md — Electron 主进程专属

> 由原 `electron/AGENTS.md` 迁移并 TS 化。所有 BrowserWindow、IPC、Go 子进程逻辑都在这里。

## 技术栈

- Electron 43+
- TypeScript（严格模式）
- electron-vite 6 beta（编译输出到 `frontend/out/main/index.js`）

## 文件结构

| 文件        | 职责                                                                                                                                                                                |
| ----------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `index.ts`  | 主入口：`app.whenReady()` → `configureProxy()` → `startGo()` → `waitForGo()` → `registerIpc()` → `createWindow()`；注册 `before-quit` / `will-quit` / `process.exit` 三处 kill hook |
| `window.ts` | `createWindow()` / `getMainWindow()`；BrowserWindow 配置；dev/prod 加载分支                                                                                                         |
| `ipc.ts`    | `registerIpc()`：注册所有 `ipcMain.handle`（v0.1 第 2 周接入 OpenClaw 后填充）                                                                                                      |
| `go.ts`     | `startGo()` / `waitForGo()` / `killGo()`：Go 子进程生命周期                                                                                                                         |

## 安全配置（必读）

所有 `BrowserWindow` 必须保留这三项：

```ts
webPreferences: {
  contextIsolation: true,   // 渲染层与主进程隔离
  nodeIntegration: false,   // 渲染层不直接拿 Node
  sandbox: true,            // 沙箱
  preload: join(__dirname, '../preload/index.js'), // electron-vite 输出位置
}
```

任何窗口配置变更都要保留这三项。

## 加载分支（dev / prod）

```ts
// electron-vite dev 期注入 ELECTRON_RENDERER_URL
if (process.env['ELECTRON_RENDERER_URL']) {
  mainWindow.loadURL(process.env['ELECTRON_RENDERER_URL']);
} else {
  mainWindow.loadFile(join(__dirname, '../renderer/index.html'));
}
```

## Go 子进程管理

主进程 spawn Go 后端并全权管理生命周期：

```ts
const backendDir = join(__dirname, '../../../backend'); // out/main → frontend → root → backend
goProcess = spawn('go', ['run', './cmd/server'], {
  cwd: backendDir,
  stdio: ['ignore', 'pipe', 'pipe'],
});
```

清理 hook：`before-quit` / `will-quit` / `process.exit` 三处兜底都 kill 一次。

**dev**：`go run ./cmd/server`；**prod**：跑编译后的二进制（v0.1 第 4 周打包引入）。

## 代理处理

```ts
const isDev = !app.isPackaged;
session.defaultSession.setProxy({ mode: isDev ? 'direct' : 'system' });
```

dev 期强制 `direct`，避开 shell 里 `HTTP_PROXY` 干扰 Vite proxy。

## 新增 IPC 频道（四步流程）

1. `shared/constants.ts` 加 `IpcChannel.Xxx` 常量
2. `frontend/preload/index.ts` 通过 `contextBridge` 暴露 `window.api.xxx`
3. `frontend/main/ipc.ts` 注册 `ipcMain.handle(IpcChannel.Xxx, ...)`
4. renderer 用 `window.api.xxx()` 调用

频道常量一律走 `shared/constants.ts`，**不要**在代码里硬编码字符串。

## 调试

- **DevTools**：dev 期 `did-finish-load` 自动开 detach 模式
- **主进程日志**：`console.log` 输出到终端 stdout，前缀用 `[main]`
- **Go 后端日志**：主进程转发，前缀 `[go]`（错误用 `[go!]`）
- 改主进程代码：electron-vite 自动重启 Electron

## 不要做

- 不要开 `nodeIntegration`
- 不要关 `contextIsolation`
- 不要让主进程直接执行用户输入
- 不要在生产加载远端 URL
- 不要把 Go 后端 spawn 两遍（dev 用 `go run`，prod 用二进制）
- 不要在主进程里硬编码 IPC 频道字符串（走 `shared/constants.ts`）
