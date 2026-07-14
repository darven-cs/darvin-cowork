/**
 * 跨 Electron / React / Go 共享的常量。
 *
 * 规则：discriminant / 状态码 / IPC 频道名一律在这里集中。
 * 一次性错误消息、CSS class、HTML 属性不需要常量化。
 *
 * 新增 IPC 频道：
 *   1. 在这里加常量
 *   2. preload.ts 暴露 window.api.xxx
 *   3. main.ts 注册 ipcMain.handle
 *   4. renderer 通过 window.api.xxx() 调用
 */

/** Electron IPC 频道（主进程 ↔ 渲染进程） */
export const IpcChannel = {
  // 会话
  SessionCreate: 'session:create',
  SessionList: 'session:list',
  SessionGet: 'session:get',
  SessionDelete: 'session:delete',
  // 消息（流式回复走 MessageStream 的 SSE，HTTP 后端直连）
  MessageSend: 'message:send',
  MessageStream: 'message:stream',
  // 应用
  AppReady: 'app:ready',
  AppError: 'app:error',
} as const;
export type IpcChannel = (typeof IpcChannel)[keyof typeof IpcChannel];

/** 消息角色 discriminator */
export const MessageRole = {
  Assistant: 'assistant',
  System: 'system',
  Tool: 'tool',
  User: 'user',
} as const;
export type MessageRole = (typeof MessageRole)[keyof typeof MessageRole];

/** 会话状态 */
export const SessionStatus = {
  Active: 'active',
  Done: 'done',
  Failed: 'failed',
} as const;
export type SessionStatus = (typeof SessionStatus)[keyof typeof SessionStatus];

/** 后端 HTTP base path 前缀 */
export const HttpBasePath = '/api' as const;

/** 默认后端监听地址 */
export const DefaultBackendHost = '127.0.0.1:8080' as const;

/** 默认前端 dev server 端口 */
export const DefaultDevServerPort = 5175 as const;
