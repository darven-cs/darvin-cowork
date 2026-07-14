# electron/AGENTS.md — Electron 主进程专属

## 技术栈

- Electron 43+（CommonJS）
- 主进程单文件 `main.js`（未来可拆）
- 渲染层跑 Vite（开发）或 dist-react（生产）

## 命令

主进程运行由根 `package.json` 的 `dev` / `start` 控制。不在 electron/ 单独跑。

## 安全配置（必读）

所有 BrowserWindow 必须有：

```js
webPreferences: {
  contextIsolation: true,
  nodeIntegration: false,
  sandbox: true,
  // preload: path.join(__dirname, 'preload.js'),  // 需要时加
}
```

任何窗口配置变更都要保留这三项。

## 子进程管理（Go 后端）

Go 后端由主进程 spawn：

```js
const { spawn } = require('child_process');
goProcess = spawn('go', ['run', './cmd'], {
  cwd: backendDir,
  stdio: ['ignore', 'pipe', 'pipe'],
});
```

清理 hook：`before-quit` / `will-quit` / `process.exit` 三处兜底都 kill 一次。

## 代理处理（开发期）

```js
app.whenReady().then(() => {
  session.defaultSession.setProxy({
    mode: process.env.ELECTRON_START_URL ? 'direct' : 'system',
  });
});
```

dev 期强制 `direct` 避开 shell 里的 `HTTP_PROXY` 干扰。

## 新增 IPC 频道

1. 在 `/shared/constants.ts` 加常量
2. preload 暴露 `window.api.xxx`
3. main 注册 `ipcMain.handle(channel, ...)`
4. renderer 用 `window.api.xxx()` 调用

## 调试

- DevTools：dev 期窗口加载完自动打开（detached）
- 主进程日志：`console.log` 输出到终端 stdout
- Go 后端日志：前缀 `[go]` 来自 main.js 的转发

## 不要做

- 不要打开 `nodeIntegration`
- 不要关闭 `contextIsolation`
- 不要让主进程直接执行用户输入
- 不要在生产加载远端 URL
- 不要把 Go 后端做成可执行文件后还要 spawn 两遍（开发期 `go run`、生产期用编译后的二进制）
