/**
 * EventRouter — 把 AgentClient 的事件按 main 的 active session 路由，
 * 落库（assistant message streaming）后再 push 给所有 BrowserWindow。
 *
 * 设计目标：
 * - backend（Go agent）v0 单 session，所有 backend event 被 route 到
 *   main 的 activeSessionId
 * - 文本累积进 `SessionStore`（renderer 切回 active session 时能 reload）
 * - renderer 拿到的事件都是 "已经按 activeSessionId 过滤" 之后的；
 *   renderer 不用再判断 ev.sessionId
 *
 * v1 等 backend 支持多 session 时，只需要把 `handle()` 里"按 main
 * active 落库" 的部分改成 "按 ev.sessionId 落库"，再把 push payload
 * 加上 ev.sessionId 让 renderer 多窗口时按 window 路由。
 */

import type { BrowserWindow } from 'electron';
import type { DarvinEvent } from '../../shared/darvin-api';
import { DarvinPushEvent } from '../../shared/darvin-api';
import type { AgentClient } from '../runtime/client';
import type { SessionStore } from './SessionStore';

interface Logger {
  warn(msg: string, ...args: unknown[]): void;
}

/**
 * 把 darvin event 落库。EventRouter 唯一允许写入 message 表的位置。
 *
 * - `text_delta` / `thinking_delta`：按 messageId 累加 content
 * - `done`：标 done=true
 * - `error`：标 done + error 字段
 * - `tool_start` / `tool_end`：v0 不落库（tool label 只在 renderer 端
 *   临时展示，刷新即丢；schema 留扩展位，等 PR-3 接 tool 名称再补）
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
   * 处理单条 event：落库 + 按 activeSessionId 过滤 + push 给所有窗口。
   *
   * v1 (backend 多 session)：用 `ev.sessionId` 取代当前 active 即可。
   */
  handle(ev: DarvinEvent): void {
    const active = this.store.getActive();
    if (active === null) {
      // 没有 active session 时不路由（renderer 端没视图能接这些事件）
      return;
    }
    applyToStore(this.store, ev);
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