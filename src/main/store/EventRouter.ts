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
  private onSessionsChanged?: () => void;
  private onWorkspacesChanged?: () => void;

  constructor(opts: {
    client: AgentClient;
    getWindows: () => BrowserWindow[];
    logger?: Logger;
    getTitle?: (sessionId: string) => string | undefined;
    /** Go 广播 SessionsChanged 时触发；main 用它重查并广播真实 session 列表。 */
    onSessionsChanged?: () => void;
    /** Go 广播 WorkspacesChanged 时触发；main 用它重查并广播真实 workspace 列表。 */
    onWorkspacesChanged?: () => void;
  }) {
    this.client = opts.client;
    this.getWindow = opts.getWindows;
    this.logger = opts.logger ?? console;
    this.getTitle = opts.getTitle ?? (() => undefined);
    this.onSessionsChanged = opts.onSessionsChanged;
    this.onWorkspacesChanged = opts.onWorkspacesChanged;
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

    if (ev.type === 'ImChanged') {
      for (const win of this.getWindow()) {
        if (win.isDestroyed()) continue;
        try {
          win.webContents.send(DarvinPushEvent.ImChanged, ev.payload);
        } catch (e) {
          this.logger.warn(`[eventrouter] send im-changed 失败: ${(e as Error).message}`);
        }
      }
      return;
    }
    if (ev.type === 'ImStatusChanged') {
      for (const win of this.getWindow()) {
        if (win.isDestroyed()) continue;
        try {
          win.webContents.send(DarvinPushEvent.ImStatusChanged, ev.payload);
        } catch (e) {
          this.logger.warn(`[eventrouter] send im-status-changed 失败: ${(e as Error).message}`);
        }
      }
      return;
    }

    if (ev.type === 'WorkspacesChanged') {
      // Go 广播的是空通知（无列表）。让 main 重查并广播真实 workspace 列表，
      // 否则 renderer 会把 workspaces.value 覆盖成空/undefined 导致侧栏异常。
      this.onWorkspacesChanged?.();
      return;
    }
    if (ev.type === 'SessionsChanged') {
      // 同上：重查并广播真实 session 列表，避免空 payload 覆盖 renderer 状态。
      this.onSessionsChanged?.();
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
