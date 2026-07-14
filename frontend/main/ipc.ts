/**
 * 注册主进程 IPC handler。
 * v0.1 第 2 周接入 OpenClaw 后在这里挂 message:stream / session:create 等频道。
 * 频道常量从 shared/constants.ts 引用（symlink）。
 *
 * 实际填充时：
 *   import { ipcMain } from 'electron';
 *   import { IpcChannel } from '../shared/constants';  // via symlink
 *   ipcMain.handle(IpcChannel.SessionCreate, ...);
 */
export function registerIpc(): void {
  // 占位：v0.1 第 2 周填充
}
