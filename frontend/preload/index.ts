import { contextBridge } from 'electron';

/**
 * 通过 contextBridge 暴露给渲染层的安全 API。
 *
 * 新增 IPC 频道流程：
 *   1. shared/constants.ts 加 IpcChannel.Xxx 常量
 *   2. 这里加 `xxx: () => ipcRenderer.invoke(IpcChannel.Xxx)`
 *   3. main/ipc.ts 加 `ipcMain.handle(IpcChannel.Xxx, ...)`
 *   4. renderer 用 `window.api.xxx()` 调用
 *
 * 暂时为空对象，v0.1 第 2 周填充。
 */
contextBridge.exposeInMainWorld('api', {
  // 占位
});

export type DarvinApi = Record<string, never>;
