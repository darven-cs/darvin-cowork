/**
 * 与 darvin-agent 子进程的 WebSocket JSON-RPC 2.0 客户端。
 *
 * 协议（S3 gateway）：
 * - request：`{jsonrpc:"2.0", id, method:"agent.prompt"|..., params}`
 * - response：`{jsonrpc:"2.0", id, result}` 或 `{jsonrpc:"2.0", id, error:{code,message}}`
 * - notification：`{jsonrpc:"2.0", method:"agent.event", params:{type,...}}`
 *
 * 事件推送需要先 `agent.subscribe_events`：Go 侧 EventLedger 按 sessionId
 * 维护订阅集合，没订阅的连接一条 notification 都收不到。
 *
 * session 数据所有权归 main：本文件不持有 activeSessionId，所有 routing
 * 由 `EventRouter` 接管，client 只暴露 raw event 给上层订阅者。
 */

import { WebSocket } from 'ws';
import { EventEmitter } from 'node:events';
import type {
  DarvinAbortResponse,
  DarvinEvent,
  DarvinGetMessagesResponse,
  DarvinListSessionsResponse,
  DarvinPromptRequest,
  DarvinPromptResponse,
} from '../../shared/darvin-api';

/**
 * Agent v0 只跑单 backend session。gateway 的 handlePrompt 会拒掉既非
 * 空、又不等于此值的 sessionId（-32602 "session not active"），且
 * EventLedger 只把事件路由给订阅了这个 id 的连接。
 *
 * 本常量保留给 `EventRouter` 用，main 侧不再裸调 `client.prompt`。
 */
export const BACKEND_DEFAULT_SESSION_ID = 'default';

interface Pending {
  resolve: (v: unknown) => void;
  reject: (e: Error) => void;
}

interface Logger {
  warn(msg: string, ...args: unknown[]): void;
}

export class AgentClient extends EventEmitter {
  private ws: WebSocket | undefined;
  private nextId = 1;
  private pending = new Map<string, Pending>();
  private eventListeners = new Set<(e: DarvinEvent) => void>();
  private logger: Logger;

  constructor(opts: { logger?: Logger } = {}) {
    super();
    this.logger = opts.logger ?? console;
  }

  /**
   * 建连。事件订阅由 caller（EventRouter）按需 `onEvent` 接入，不在 connect
   * 里自动 subscribe —— main 知道自己的 active session 何时 ready，
   * 不该让 client 把这个隐式假设固定下来。
   */
  async connect(port: number): Promise<void> {
    if (this.ws) return;
    await this.openSocket(port);
  }

  private openSocket(port: number): Promise<void> {
    return new Promise<void>((resolve, reject) => {
      const ws = new WebSocket(`ws://localhost:${port}/ws`);
      let opened = false;

      ws.once('open', () => {
        opened = true;
        this.ws = ws;
        resolve();
      });

      ws.on('error', (err: Error) => {
        if (!opened) {
          reject(err);
          return;
        }
        this.logger.warn(`[agentclient] ws error: ${err.message}`);
      });

      ws.on('close', () => {
        if (this.ws !== ws) return;
        this.ws = undefined;
        // 连接断了 in-flight 请求永远不会有回包，全部 reject 防泄漏
        for (const p of this.pending.values()) {
          p.reject(new Error('agent offline'));
        }
        this.pending.clear();
        this.emit('offline');
      });

      ws.on('message', (data: Buffer) => {
        let msg: unknown;
        try {
          msg = JSON.parse(data.toString('utf8'));
        } catch (e) {
          this.logger.warn(`[agentclient] 非法 JSON 帧: ${(e as Error).message}`);
          return;
        }
        this.handleIncoming(msg);
      });
    });
  }

  private handleIncoming(msg: unknown): void {
    if (typeof msg !== 'object' || msg === null) return;
    const frame = msg as {
      id?: unknown;
      error?: { code?: number; message?: string };
      result?: unknown;
      method?: unknown;
      params?: unknown;
    };

    if (frame.id !== undefined && frame.id !== null) {
      const p = this.pending.get(String(frame.id));
      if (!p) return;
      this.pending.delete(String(frame.id));
      if (frame.error) {
        p.reject(
          new Error(`rpc ${frame.error.code}: ${frame.error.message ?? ''}`),
        );
      } else {
        p.resolve(frame.result);
      }
      return;
    }

    if (frame.method !== 'agent.event') return;
    if (typeof frame.params !== 'object' || frame.params === null) {
      this.logger.warn('[agentclient] agent.event 缺少 params');
      return;
    }
    const raw = frame.params as Record<string, unknown>;
    const ev = parseDarvinEvent(raw);
    if (!ev) {
      // 生命周期事件 renderer 不渲染，但 Go 每轮 prompt 都发，不能当异常刷日志
      if (!LIFECYCLE_EVENT_TYPES.has(String(raw.type))) {
        this.logger.warn(`[agentclient] 未知 event type: ${String(raw.type)}`);
      }
      return;
    }
    for (const cb of this.eventListeners) {
      try {
        cb(ev);
      } catch (e) {
        // 单个 listener 抛错不能带崩整条 fanout
        this.logger.warn(`[agentclient] listener 抛错: ${(e as Error).message}`);
      }
    }
  }

  async request<T>(method: string, params?: unknown): Promise<T> {
    const ws = this.ws;
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      throw new Error('agent offline');
    }
    const id = String(this.nextId++);
    return new Promise<T>((resolve, reject) => {
      this.pending.set(id, { resolve: resolve as (v: unknown) => void, reject });
      try {
        ws.send(
          JSON.stringify({ jsonrpc: '2.0', id, method, params: params ?? {} }),
        );
      } catch (e) {
        this.pending.delete(id);
        reject(e as Error);
      }
    });
  }

  prompt(req: DarvinPromptRequest & { sessionId: string }): Promise<DarvinPromptResponse> {
    return this.request<DarvinPromptResponse>('agent.prompt', req);
  }

  abort(req: { sessionId: string }): Promise<DarvinAbortResponse> {
    return this.request<DarvinAbortResponse>('agent.abort', req);
  }

  /**
   * 订阅 backend 单 session 的事件流（v0 backend 不支持多 session）。
   * 在 `connect` 成功之后调一次；EventRouter 接入前需要先 subscribe
   * 才能拿到 notification。
   */
  async subscribeEvents(sessionId: string): Promise<void> {
    await this.request<{ subscribed: boolean }>('agent.subscribe_events', { sessionId });
  }

  listSessions(): Promise<DarvinListSessionsResponse> {
    return this.request<DarvinListSessionsResponse>('agent.list_sessions', {});
  }

  getMessages(sessionId: string, limit = 1000, offset = 0): Promise<DarvinGetMessagesResponse> {
    return this.request<DarvinGetMessagesResponse>('agent.get_messages', {
      sessionId,
      limit,
      offset,
    });
  }

  onEvent(cb: (e: DarvinEvent) => void): () => void {
    this.eventListeners.add(cb);
    return () => {
      this.eventListeners.delete(cb);
    };
  }

  isConnected(): boolean {
    return this.ws?.readyState === WebSocket.OPEN;
  }

  disconnect(): Promise<void> {
    const ws = this.ws;
    if (!ws) return Promise.resolve();
    return new Promise<void>((resolve) => {
      if (ws.readyState === WebSocket.CLOSED) {
        resolve();
        return;
      }
      ws.once('close', () => resolve());
      try {
        ws.close();
      } catch {
        resolve();
      }
    });
  }
}

/**
 * Go 侧 mapEventToTS 只把 7 类事件映射成 DarvinEvent，其余按
 * `{type}` 裸壳发出（internal/gateway/eventledger.go）。这些是运行
 * 生命周期事件，renderer 无需渲染，静默丢弃即可 —— 否则每轮 prompt
 * 都会在主进程 stderr 刷四五条 warn。
 */
const LIFECYCLE_EVENT_TYPES = new Set([
  'prompt_received',
  'run_start',
  'run_end',
  'turn_start',
  'turn_end',
  'llm_start',
  'compaction',
]);

/**
 * 把 notification 的 params 收口成 DarvinEvent union。
 *
 * 未知 type 返回 null（caller 打 warn 后丢弃），不抛错 —— Go 侧新增事件
 * 类型时旧版 Electron 不应该崩，后续合法事件仍要能继续消费。
 */
export function parseDarvinEvent(
  raw: Record<string, unknown>,
): DarvinEvent | null {
  switch (raw.type) {
    case 'text_delta':
    case 'thinking_delta':
    case 'tool_start':
    case 'tool_end':
    case 'done':
    case 'error':
    case 'agent_end':
      return raw as unknown as DarvinEvent;
    default:
      return null;
  }
}