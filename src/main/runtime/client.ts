/**
 * 与 darvin-agent 子进程的 WebSocket JSON-RPC 2.0 客户端。
 *
 * 协议（gateway）：
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
  DarvinGetSessionUsageResponse,
  DarvinInvokeSkillRequest,
  DarvinInvokeSkillResponse,
  DarvinListSessionsResponse,
  DarvinListSkillsResponse,
  DarvinListToolsResponse,
  DarvinMcpConnectionChangedEvent,
  DarvinMcpResolutionChangedEvent,
  DarvinMcpResourcesListResponse,
  DarvinMcpResourceReadResponse,
  DarvinMcpPromptsListResponse,
  DarvinMcpPromptGetResponse,
  DarvinMcpLogsResponse,
  DarvinMcpServer,
  DarvinMcpServerPatch,
  DarvinPromptRequest,
  DarvinPromptResponse,
  DarvinSetSkillEnabledRequest,
  DarvinSetSkillEnabledResponse,
  DarvinSetWorkspaceResponse,
  DarvinSkillSummary,
  DarvinTestMcpConnectionRequest,
  DarvinTestMcpConnectionResponse,
  SubagentMessage,
  SubagentRun,
} from '../../shared/darvin-api';

/**
 * 后端默认 session id：EventRouter 把它作为订阅 / prompt 的目标 session。
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
  private skillsListeners = new Set<(skills: DarvinSkillSummary[]) => void>();
  private mcpConnListeners = new Set<(e: DarvinMcpConnectionChangedEvent) => void>();
  private mcpResListeners = new Set<(e: DarvinMcpResolutionChangedEvent) => void>();
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

    if (frame.method === 'agent.skills.changed') {
      if (typeof frame.params !== 'object' || frame.params === null) {
        this.logger.warn('[agentclient] agent.skills.changed 缺少 params');
        return;
      }
      const skills = (frame.params as { skills?: DarvinSkillSummary[] }).skills ?? [];
      for (const cb of this.skillsListeners) {
        try {
          cb(skills);
        } catch (e) {
          this.logger.warn(`[agentclient] skills listener 抛错: ${(e as Error).message}`);
        }
      }
      return;
    }

    if (frame.method === 'mcp.connection_changed') {
      if (typeof frame.params !== 'object' || frame.params === null) {
        this.logger.warn('[agentclient] mcp.connection_changed 缺少 params');
        return;
      }
      const p = frame.params as { id?: unknown; status?: unknown; error?: unknown };
      if (typeof p.id !== 'string' || typeof p.status !== 'string') {
        this.logger.warn('[agentclient] mcp.connection_changed id/status 缺失');
        return;
      }
      const e: DarvinMcpConnectionChangedEvent = {
        id: p.id,
        status: p.status as DarvinMcpConnectionChangedEvent['status'],
        error: typeof p.error === 'string' ? p.error : undefined,
      };
      for (const cb of this.mcpConnListeners) {
        try {
          cb(e);
        } catch (err) {
          this.logger.warn(`[agentclient] mcp conn listener 抛错: ${(err as Error).message}`);
        }
      }
      return;
    }

    if (frame.method === 'mcp.resolution_changed') {
      if (typeof frame.params !== 'object' || frame.params === null) {
        this.logger.warn('[agentclient] mcp.resolution_changed 缺少 params');
        return;
      }
      const p = frame.params as { serverId?: unknown; resolution?: unknown };
      if (typeof p.serverId !== 'string' || !p.resolution || typeof p.resolution !== 'object') {
        this.logger.warn('[agentclient] mcp.resolution_changed 缺字段');
        return;
      }
      const e: DarvinMcpResolutionChangedEvent = {
        serverId: p.serverId,
        resolution: p.resolution as DarvinMcpResolutionChangedEvent['resolution'],
      };
      for (const cb of this.mcpResListeners) {
        try {
          cb(e);
        } catch (err) {
          this.logger.warn(`[agentclient] mcp res listener 抛错: ${(err as Error).message}`);
        }
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

  prompt(
    req: DarvinPromptRequest & { sessionId: string; runId: string },
  ): Promise<DarvinPromptResponse> {
    return this.request<DarvinPromptResponse>('agent.prompt', req);
  }

  abort(req: { sessionId: string; runId: string }): Promise<DarvinAbortResponse> {
    return this.request<DarvinAbortResponse>('agent.abort', req);
  }

  /**
   * `/skill-name args` 用户显式触发。Go 端同步校验（存在 +
   * enabled + userInvocable）失败时以 RPC error 返回，main 据此 toast；
   * 通过后异步跑 mini agent loop，事件流跟普通 prompt 一致。
   */
  invokeSkill(req: DarvinInvokeSkillRequest): Promise<DarvinInvokeSkillResponse> {
    return this.request<DarvinInvokeSkillResponse>('agent.skill.invoke_user', req);
  }

  /**
   * 在 `connect` 成功之后调一次；EventRouter 接入前需要先 subscribe
   * 才能拿到 notification。
   */
  async subscribeEvents(sessionId: string): Promise<void> {
    await this.request<{ subscribed: boolean }>('agent.subscribe_events', { sessionId });
  }

  listSessions(workspaceId?: string): Promise<DarvinListSessionsResponse> {
    return this.request<DarvinListSessionsResponse>('agent.list_sessions', { workspaceId });
  }

  getMessages(sessionId: string, limit = 1000, offset = 0): Promise<DarvinGetMessagesResponse> {
    return this.request<DarvinGetMessagesResponse>('agent.get_messages', {
      sessionId,
      limit,
      offset,
    });
  }

  getSessionUsage(sessionId: string): Promise<DarvinGetSessionUsageResponse> {
    return this.request<DarvinGetSessionUsageResponse>('agent.get_session_usage', {
      sessionId,
    });
  }

  /** 运行时重锚 workspace（Go 侧 sandbox + 项目 skills），不重启子进程。 */
  setWorkspace(rootPath: string): Promise<DarvinSetWorkspaceResponse> {
    return this.request<DarvinSetWorkspaceResponse>('agent.set_workspace', {
      rootPath,
    });
  }

  /**
   * Go 端 skills 命名空间。bootstrap 由 main 端在启动期调用一
   * 次推初始 enabled 状态；list / setEnabled 既可被 main 端主动调
   * （renderer 通过 IPC 触发），也可在 setEnabled 之后由 main 写
   * SQLite → Go SetEnabled → broadcast notification 回到 main。
   */
  skills = {
    list: (): Promise<DarvinListSkillsResponse> =>
      this.request<DarvinListSkillsResponse>('agent.skills.list', {}),
    setEnabled: (req: DarvinSetSkillEnabledRequest): Promise<DarvinSetSkillEnabledResponse> =>
      this.request<DarvinSetSkillEnabledResponse>('agent.skills.set_enabled', req),
    bootstrap: (req: { skills: DarvinSkillSummary[] }): Promise<{ ok: boolean }> =>
      this.request<{ ok: boolean }>('agent.skills.bootstrap', req),
    onChanged: (cb: (skills: DarvinSkillSummary[]) => void): (() => void) => {
      this.skillsListeners.add(cb);
      return () => {
        this.skillsListeners.delete(cb);
      };
    },
  };

  /**
   * Go 端 MCP 命名空间。bootstrap 由 main 端在启动期调用一次推
   * 初始 server 列表；list / register / update / unregister / setEnabled /
   * test / retryResolution 既可被 main 端主动调（renderer 通过 IPC 触发），
   * 也可由 Go 端连接状态变化时 broadcast notification 回到 main。
   */
  mcp = {
    list: (): Promise<{ servers: DarvinMcpServer[] }> =>
      this.request<{ servers: DarvinMcpServer[] }>('agent.mcp.list', {}),
    register: (req: { server: DarvinMcpServer }): Promise<{ ok: boolean }> =>
      this.request<{ ok: boolean }>('agent.mcp.register', req),
    update: (req: { id: string; patch: DarvinMcpServerPatch }): Promise<{ server: DarvinMcpServer }> =>
      this.request<{ server: DarvinMcpServer }>('agent.mcp.update', req),
    unregister: (req: { id: string }): Promise<{ ok: boolean }> =>
      this.request<{ ok: boolean }>('agent.mcp.unregister', req),
    setEnabled: (req: { id: string; enabled: boolean }): Promise<{ ok: boolean }> =>
      this.request<{ ok: boolean }>('agent.mcp.set_enabled', req),
    test: (req: DarvinTestMcpConnectionRequest): Promise<DarvinTestMcpConnectionResponse> =>
      this.request<DarvinTestMcpConnectionResponse>('agent.mcp.test', req),
    retryResolution: (req: { id: string }): Promise<{ ok: boolean }> =>
      this.request<{ ok: boolean }>('agent.mcp.retry_resolution', req),
    bootstrap: (req: { servers: DarvinMcpServer[] }): Promise<{ ok: boolean }> =>
      this.request<{ ok: boolean }>('agent.mcp.bootstrap', req),
    resourcesList: (req: { id: string }): Promise<DarvinMcpResourcesListResponse> =>
      this.request<DarvinMcpResourcesListResponse>('agent.mcp.resources.list', req),
    resourceRead: (req: { id: string; uri: string }): Promise<DarvinMcpResourceReadResponse> =>
      this.request<DarvinMcpResourceReadResponse>('agent.mcp.resource.read', req),
    promptsList: (req: { id: string }): Promise<DarvinMcpPromptsListResponse> =>
      this.request<DarvinMcpPromptsListResponse>('agent.mcp.prompts.list', req),
    promptGet: (req: { id: string; name: string; arguments?: Record<string, unknown> }): Promise<DarvinMcpPromptGetResponse> =>
      this.request<DarvinMcpPromptGetResponse>('agent.mcp.prompt.get', req),
    logsGet: (req: { id: string }): Promise<DarvinMcpLogsResponse> =>
      this.request<DarvinMcpLogsResponse>('agent.mcp.logs.get', req),
    onConnectionChanged: (cb: (e: DarvinMcpConnectionChangedEvent) => void): (() => void) => {
      this.mcpConnListeners.add(cb);
      return () => {
        this.mcpConnListeners.delete(cb);
      };
    },
    onResolutionChanged: (cb: (e: DarvinMcpResolutionChangedEvent) => void): (() => void) => {
      this.mcpResListeners.add(cb);
      return () => {
        this.mcpResListeners.delete(cb);
      };
    },
  };

  /**
   * Go 端工具面命名空间。list 返回内置 + skill + mcp 合并
   * 视图，renderer / 调试用；skill / mcp 状态变化后 Go 端自动重注册。
   */
  tools = {
    list: (): Promise<DarvinListToolsResponse> =>
      this.request<DarvinListToolsResponse>('agent.tools.list', {}),
  };

  /**
   * Subagent 命名空间：给 renderer 的 Subagents artifact tab 拉列表 /
   * 读历史 / 取消 run / 分页读结果。
   */
  subagent = {
    list: (parentSessionId: string): Promise<{ subagents: SubagentRun[] }> =>
      this.request<{ subagents: SubagentRun[] }>('agent.subagent.list', { sessionId: parentSessionId }),
    getMessages: (runId: string): Promise<{ messages: SubagentMessage[] }> =>
      this.request<{ messages: SubagentMessage[] }>('agent.subagent.get_messages', { runId }),
    abort: (runId: string): Promise<{ ok: boolean }> =>
      this.request<{ ok: boolean }>('agent.subagent.abort', { runId }),
    readResult: (runId: string, offsetBytes: number, limitBytes: number): Promise<{ text: string }> =>
      this.request<{ text: string }>('agent.subagent.read_result', {
        runId,
        offset_bytes: offsetBytes,
        limit_bytes: limitBytes,
      }),
  };

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
]);

/**
 * 把 notification 的 params 收口成 DarvinEvent union。
 *
 * 未知 type 返回 null（caller 打 warn 后丢弃），不抛错 —— Go 侧新增事件
 * 类型时旧版 Electron 不应该崩，后续合法事件仍要能继续消费。
 */
/**
 * 从 raw 里提升 toolUseId：tool_use / tool_result 加进消息 union
 * 后，事件侧的 tool_start / tool_end 仍缺 toolUseId。Go 的 CallID 注入在
 * `message.id`（eventledger.go mapEventToTS），这里提升出来；老 backend
 * 没有 message.id 时按 messageId 兜底，保持向后兼容。
 */
function readToolUseId(raw: Record<string, unknown>): string | undefined {
  const message = raw.message;
  if (message && typeof message === 'object') {
    const id = (message as Record<string, unknown>).id;
    if (typeof id === 'string' && id) return id;
  }
  if (typeof raw.messageId === 'string' && raw.messageId) return raw.messageId;
  return undefined;
}

export function parseDarvinEvent(
  raw: Record<string, unknown>,
): DarvinEvent | null {
  switch (raw.type) {
    case 'tool_start':
      return {
        type: 'tool_start',
        sessionId: raw.sessionId,
        runId: raw.runId,
        messageId: raw.messageId,
        toolUseId: readToolUseId(raw),
        tool: raw.tool,
        toolKind: raw.toolKind as string | undefined,
        skillId: raw.skillId as string | undefined,
        mcpServerId: raw.mcpServerId as string | undefined,
        input: raw.input,
      } as unknown as DarvinEvent;
    case 'tool_end':
      // Go 的 mapEventToTS 把输出内容直接塞进 `tool` 字段（没有 `output`），
      // 这里收敛成 output；等 Go 侧补齐字段后 raw.output 优先。
      return {
        type: 'tool_end',
        sessionId: raw.sessionId,
        runId: raw.runId,
        messageId: raw.messageId,
        toolUseId: readToolUseId(raw),
        tool: raw.tool,
        toolKind: raw.toolKind as string | undefined,
        skillId: raw.skillId as string | undefined,
        mcpServerId: raw.mcpServerId as string | undefined,
        output: raw.output ?? raw.tool,
      } as unknown as DarvinEvent;
    case 'text_delta':
    case 'thinking_delta':
    case 'done':
    case 'error':
    case 'agent_end':
    case 'compaction':
    case 'context_usage':
    case 'artifact':
    case 'permission_request':
      return raw as unknown as DarvinEvent;
    default:
      return null;
  }
}