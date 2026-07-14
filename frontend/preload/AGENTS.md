# frontend/preload/AGENTS.md — 预加载脚本专属

> 唯一文件：`index.ts`。通过 `contextBridge.exposeInMainWorld('api', ...)` 给渲染层暴露安全 API。

## 职责

在渲染层（React）和主进程（Electron）之间做**安全桥接**：

- 渲染层只能调这里暴露的方法，不能直接 `require('electron')`
- 这里只做 `ipcRenderer.invoke` 转发，**不写业务逻辑**
- 业务逻辑在 `frontend/main/ipc.ts` 的 `ipcMain.handle` 里

## 文件结构

只有 `index.ts`：

```ts
import { contextBridge } from 'electron';

contextBridge.exposeInMainWorld('api', {
  // 占位：v0.1 第 2 周填充
});

export type DarvinApi = Record<string, never>;
```

## 新增 IPC 频道

四步流程（详见 `frontend/main/AGENTS.md`）：

1. `shared/constants.ts` 加常量
2. **这里**加 `xxx: () => ipcRenderer.invoke(IpcChannel.Xxx)`
3. `frontend/main/ipc.ts` 注册 handler
4. renderer 调用 `window.api.xxx()`

## 类型导出

暴露的方法签名要导出 `type DarvinApi`，让渲染层的 `frontend/renderer/src/types/global.d.ts` 可以引用：

```ts
// preload/index.ts
export type DarvinApi = {
  sendMessage: (msg: string) => Promise<string>;
};
```

```ts
// renderer/src/types/global.d.ts
import type { DarvinApi } from '../../../preload';
declare global {
  interface Window {
    api: DarvinApi;
  }
}
```

类型用 `import type`，**避免运行时跨边界值导入**（preload 和 renderer 是两个 bundle）。

## 规则

- 不要直接 `import` 主进程模块（`frontend/main/*`）—— 只能用 `ipcRenderer.invoke`
- 不要在这里做副作用（fetch、文件读写、console.log）
- 不要硬编码 IPC 频道字符串 —— 走 `shared/constants.ts` 的 `IpcChannel` 常量
- 不要暴露泛型 `ipcRenderer.on` —— 改用具体的 invoke / send 包装方法

## 不要做

- 不要 `import { ipcRenderer } from 'electron'` 然后直接暴露给 renderer（要包装一层）
- 不要在 preload 里 import 渲染层代码（React、Zustand 等）
- 不要改 `contextIsolation: true`（这是 preload 存在的前提）
