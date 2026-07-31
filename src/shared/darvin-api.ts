/**
 * Darvin API — 渲染层 ↔ preload ↔ main ↔ Go agent 之间的契约。
 *
 * 单一职责：
 * - 定义 `DarvinEvent` union（事件流）
 * - 定义请求/响应类型
 * - 定义 `DarvinApi` 接口（contextBridge 暴露给 renderer）
 *
 * session / message 数据所有权归 main（`SessionStore`），renderer 走
 * `DarvinApi` 只读视图 + 发命令；`activeSessionId` 由 main 单向 push
 * 给 renderer，renderer 不持久化。
 *
 * 修改这里 = 改契约。
 */

export type DarvinRuntimeStatus = 'ready' | 'offline' | 'no-binary' | 'online';

export type DarvinModelId =
  | 'claude-sonnet-4-5'
  | 'claude-opus-4-5'
  | 'gpt-4o';

export type DarvinSessionStatus = 'active' | 'archived';

/**
 * session 的 renderer 视图：id 由 main 端 uuidv4 生成，renderer 不参与生成。
 */
export interface DarvinSession {
  id: string;
  title: string;
  updatedAt: number;
  status?: DarvinSessionStatus;
  claudeSessionId?: string | null;
}

export interface DarvinMessage {
  id: string;
  sessionId: string;
  role: 'user' | 'assistant';
  content: string;
  done: boolean;
  error?: string;
  toolLabel?: string;
  createdAt: number;
}

export interface DarvinUsage {
  inputTokens: number;
  outputTokens: number;
  totalTokens: number;
}

/**
 * 每条事件携带 sessionId + runId，让 renderer 端按 session 路由、上层
 * 派生 running / unread 状态。本期先标 optional（PR 4 起 Go 开始注入，
 * PR 6 起转强类型）——加 optional 不破坏现有可选字段的 union 收紧规则。
 */
export type DarvinEvent =
  | { type: 'text_delta'; sessionId?: string; runId?: string; messageId: string; delta: string }
  | { type: 'thinking_delta'; sessionId?: string; runId?: string; messageId: string; delta: string }
  | { type: 'tool_start'; sessionId?: string; runId?: string; messageId: string; tool: string; input: unknown }
  | { type: 'tool_end'; sessionId?: string; runId?: string; messageId: string; tool: string; output: unknown }
  | { type: 'done'; sessionId?: string; runId?: string; messageId: string; usage?: DarvinUsage }
  | { type: 'error'; sessionId?: string; runId?: string; messageId: string; message: string }
  | { type: 'agent_end'; sessionId?: string; runId?: string };

export interface DarvinPromptRequest {
  content: string;
  model?: DarvinModelId;
}

export interface DarvinPromptResponse {
  sessionId: string;
  messageId: string;
  /** 本次 prompt 在 main 端生成的 runId（UUIDv4），abort / 事件路由用 */
  runId?: string;
}

export interface DarvinAbortResponse {
  aborted: boolean;
  sessionId: string;
}

export interface DarvinListSessionsResponse {
  sessions: DarvinSession[];
}

export interface DarvinGetMessagesResponse {
  messages: DarvinMessage[];
}

export interface DarvinCreateSessionResponse {
  session: DarvinSession;
}

export interface DarvinSwitchSessionResponse {
  sessionId: string;
}

export interface DarvinDeleteSessionResponse {
  deleted: boolean;
  // 删除 active session 时 main 自动切到的下一个 session（可能为 null：
  // 这是最后一个 session 被删的情况，UI 退回"无 session"空态）。
  nextActiveSessionId: string | null;
}

export interface DarvinActiveSessionResponse {
  sessionId: string | null;
}

export interface DarvinLLMConfig {
  provider: 'anthropic';
  apiKey: string;
  baseUrl: string;
}

// 重启 Go 子进程的反馈：UI 端用它判定是否要切到 "Restarting…"
export interface DarvinSetLLMConfigResponse {
  saved: boolean;
  restarted: boolean;
}

export type DarvinLocale = 'zh' | 'en';

export interface DarvinLocaleResponse {
  locale: DarvinLocale;
}

export const DarvinPushEvent = {
  SessionsChanged: 'darvin:push:sessions-changed',
  ActiveSessionChanged: 'darvin:push:active-session-changed',
  SessionEvent: 'darvin:push:session-event',
} as const;
export type DarvinPushEvent = typeof DarvinPushEvent[keyof typeof DarvinPushEvent];

export interface DarvinApi {
  createSession(req?: { title?: string }): Promise<DarvinCreateSessionResponse>;
  listSessions(): Promise<DarvinListSessionsResponse>;
  switchSession(sessionId: string): Promise<DarvinSwitchSessionResponse>;
  deleteSession(sessionId: string): Promise<DarvinDeleteSessionResponse>;
  getActiveSession(): Promise<DarvinActiveSessionResponse>;
  onSessionsChanged(handler: (sessions: DarvinSession[]) => void): () => void;
  onActiveSessionChanged(handler: (sessionId: string | null) => void): () => void;

  getMessages(sessionId: string): Promise<DarvinGetMessagesResponse>;

  prompt(req: DarvinPromptRequest): Promise<DarvinPromptResponse>;
  abort(): Promise<DarvinAbortResponse>;

  onEvent(handler: (e: DarvinEvent) => void): () => void;

  /** 返回当前生效的 LLM 配置（来自用户级 yaml，未配置时 apiKey 为空串）。 */
  getLLMConfig(): Promise<DarvinLLMConfig>;
  /** 写入用户级 yaml + 重启 Go 子进程，subprocess 重启后才会用到新 key。 */
  setLLMConfig(req: { apiKey: string; baseUrl?: string }): Promise<DarvinSetLLMConfigResponse>;

  /** 读取持久化的 UI 语言；首次启动或未写入时回落到 'zh'。 */
  getLocale(): Promise<DarvinLocaleResponse>;
  /** 写入用户级 yaml；renderer 收到返回值后再 `setLang()`，组件因 ref 自动 re-render。 */
  setLocale(req: { locale: DarvinLocale }): Promise<void>;

  /** 走 IPC 异步查询：sendSync 会阻塞 renderer 线程，不用。 */
  status(): Promise<DarvinRuntimeStatus>;
}