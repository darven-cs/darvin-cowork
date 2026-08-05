/**
 * 后台 session 完成时的系统通知。
 *
 * 仅在窗口不在前台时触发 —— Electron Notification 对前台窗口是噪声。
 * 通知文案按 session 标题 best-effort 解析，标题为空时退到 sessionId 前 8 位，
 * 让用户能识别出是哪个后台会话出结果了。
 */

import { Notification, type BrowserWindow } from 'electron';

interface NotifyOpts {
  win: BrowserWindow;
  sessionId: string;
  title?: string;
}

export function notifyIfHidden(opts: NotifyOpts): void {
  if (!Notification.isSupported()) return;
  const { win, sessionId } = opts;
  if (win.isDestroyed()) return;
  if (win.isFocused() || win.isMinimized()) return;

  const displayTitle = opts.title && opts.title.trim() !== ''
    ? opts.title
    : `Session ${sessionId.slice(0, 8)}`;

  const notif = new Notification({
    title: 'darvin-cowork',
    body: `${displayTitle} — 已完成`,
  });
  notif.show();
}