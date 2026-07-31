/**
 * 把 AgentClient 的事件落库 + push 给所有 BrowserWindow。
 *
 * Per-session 路由：每个 event 自带 sessionId（Go 侧 mapEventToTS 注入），
 * 落库时按 (sessionId, messageId) 定位；EventRouter 不再读 active session，
 * 也不再丢弃"非 active"的事件 —— 那些都是别的 session 的合法事件，
 * renderer 端按 sessionId 自己分发到 per-session 消息索引。
 *
 * 收到 `done` 时调一次 `agentTaskNotifier.notifyIfHidden`：让用户切回来
 * 时通过系统通知看到后台 session 完成。
 */

import type { BrowserWindow } from 'electron';
import type { DarvinEvent } from '../../shared/darvin-api';
import { DarvinPushEvent } from '../../shared/darvin-api';
import type { AgentClient } from '../runtime/client';
import type { SessionStore } from './SessionStore';
import { notifyIfHidden } from '../libs/agentTaskNotifier';

interface Logger {
  warn(msg: string, ...args: unknown[]): void;
}

/**
 * 把 darvin event 落库。EventRouter 唯一允许写入 message 表的位置。
 *
 * - `text_delta` / `thinking_delta`：按 messageId 累加 content
 * - `done`：标 done=true
 * - `error`：标 done + error 字段
 * - `tool_start` / `tool_end`：不落库（tool label 只在 renderer 端临时展示，刷新即丢）
 * - `agent_end`：仅 broadcast，不落库
 */
function applyToStore(store: SessionStore, ev: DarvinEvent): void {
  switch (ev.type) {
    case 'text_delta':
    case 'thinking_delta':
      store.appendMessageDelta(ev.messageId, ev.delta);
      return;
    case 'done':
      store.markMessageDone(ev.messageId);
      return;
    case 'error':
      store.markMessageError(ev.messageId, ev.message);
      return;
    case 'tool_start':
    case 'tool_end':
    case 'agent_end':
      return;
  }
}

export class EventRouter {
  private store: SessionStore;
  private client: AgentClient;
  private getWindow: () => BrowserWindow[];
  private logger: Logger;
  private unsubscribe: (() => void) | null = null;

  constructor(opts: {
    store: SessionStore;
    client: AgentClient;
    getWindows: () => BrowserWindow[];
    logger?: Logger;
  }) {
    this.store = opts.store;
    this.client = opts.client;
    this.getWindow = opts.getWindows;
    this.logger = opts.logger ?? console;
  }

  /** 订阅 AgentClient 事件流，开始路由。多次调用幂等。 */
  start(): void {
    if (this.unsubscribe !== null) return;
    this.unsubscribe = this.client.onEvent((ev) => this.handle(ev));
  }

  /** 停订阅。SessionStore 不关。 */
  stop(): void {
    if (this.unsubscribe === null) return;
    this.unsubscribe();
    this.unsubscribe = null;
  }

  /**
   * 处理单条 event：落库 + 全量广播给所有窗口 + 后台完成时发系统通知。
   * 无 active session 过滤：renderer 自己按 sessionId 路由。
   */
  handle(ev: DarvinEvent): void {
    applyToStore(this.store, ev);

    if (ev.type === 'done') {
      const sessionId = ev.sessionId;
      if (sessionId) {
        const sess = this.store.getSession(sessionId);
        for (const win of this.getWindow()) {
          notifyIfHidden({ win, sessionId, title: sess?.title });
        }
      }
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