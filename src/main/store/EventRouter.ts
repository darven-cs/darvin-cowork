/**
 * 把 AgentClient 的事件推给所有 BrowserWindow —— 纯转发，不再落库。
 *
 * Per-session 路由：每个 event 自带 sessionId（Go 侧 mapEventToTS 注入），
 * renderer 端按 sessionId 自己分发到 per-session 消息索引；EventRouter
 * 不读 active session、不读 store，只做 `go event → webContents.send`。
 *
 * 收到 `done` 时调一次 `agentTaskNotifier.notifyIfHidden`：让用户切回来
 * 时通过系统通知看到后台 session 完成。title 由 main 端通过 getTitle
 * 回调从缓存 Map 补（FR-10），查不到就交给 notifier 的 sessionId 兜底。
 */

import type { BrowserWindow } from 'electron';
import type { DarvinEvent } from '../../shared/darvin-api';
import { DarvinPushEvent } from '../../shared/darvin-api';
import type { AgentClient } from '../runtime/client';
import { notifyIfHidden } from '../libs/agentTaskNotifier';

interface Logger {
  warn(msg: string, ...args: unknown[]): void;
}

export class EventRouter {
  private client: AgentClient;
  private getWindow: () => BrowserWindow[];
  private logger: Logger;
  private getTitle: (sessionId: string) => string | undefined;
  private unsubscribe: (() => void) | null = null;

  constructor(opts: {
    client: AgentClient;
    getWindows: () => BrowserWindow[];
    logger?: Logger;
    getTitle?: (sessionId: string) => string | undefined;
  }) {
    this.client = opts.client;
    this.getWindow = opts.getWindows;
    this.logger = opts.logger ?? console;
    this.getTitle = opts.getTitle ?? (() => undefined);
  }

  /** 订阅 AgentClient 事件流，开始路由。多次调用幂等。 */
  start(): void {
    if (this.unsubscribe !== null) return;
    this.unsubscribe = this.client.onEvent((ev) => this.handle(ev));
  }

  /** 停订阅。 */
  stop(): void {
    if (this.unsubscribe === null) return;
    this.unsubscribe();
    this.unsubscribe = null;
  }

  /**
   * 处理单条 event：全量广播给所有窗口 + 后台完成时发系统通知。
   * 无 active session 过滤：renderer 自己按 sessionId 路由。
   */
  handle(ev: DarvinEvent): void {
    if (ev.type === 'done') {
      const sessionId = ev.sessionId;
      if (sessionId) {
        const title = this.getTitle(sessionId);
        for (const win of this.getWindow()) {
          notifyIfHidden({ win, sessionId, title });
        }
      }
    }

    if (ev.type === 'ScheduleChanged') {
      for (const win of this.getWindow()) {
        if (win.isDestroyed()) continue;
        try {
          win.webContents.send(DarvinPushEvent.SchedulesChanged, ev.payload);
        } catch (e) {
          this.logger.warn(`[eventrouter] send schedules-changed 失败: ${(e as Error).message}`);
        }
      }
      return;
    }
    if (ev.type === 'ScheduleRunsChanged') {
      for (const win of this.getWindow()) {
        if (win.isDestroyed()) continue;
        try {
          win.webContents.send(DarvinPushEvent.ScheduleRunsChanged, ev.payload);
        } catch (e) {
          this.logger.warn(`[eventrouter] send schedule-runs-changed 失败: ${(e as Error).message}`);
        }
      }
      return;
    }
    if (ev.type === 'ScheduleFired') {
      for (const win of this.getWindow()) {
        if (win.isDestroyed()) continue;
        try {
          win.webContents.send(DarvinPushEvent.ScheduleFired, ev.payload);
        } catch (e) {
          this.logger.warn(`[eventrouter] send schedule-fired 失败: ${(e as Error).message}`);
        }
      }
      return;
    }

    for (const win of this.getWindow()) {
      if (win.isDestroyed()) continue;
      try {
        win.webContents.send(DarvinPushEvent.SessionEvent, ev);
      } catch (e) {
        this.logger.warn(`[eventrouter] send 失败: ${(e as Error).message}`);
      }
    }
  }
}
