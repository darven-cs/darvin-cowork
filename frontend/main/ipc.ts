import { ipcMain } from 'electron';

/**
 * 注册主进程 IPC handler。
 * v0.1 第 2 周接入 OpenClaw 后在这里挂 message:stream / session:create 等频道。
 * 频道常量从 shared/constants.ts 引用（symlink）。
 */
export function registerIpc(): void {
  // 占位：v0.1 第 2 周填充
  // 避免 unused 告警，下面 ipcMain 仅作为 API 存在性占位
  void ipcMain;
}
