/**
 * Darvin API — 渲染层 ↔ preload ↔ Go agent 之间的契约。
 *
 * 单一职责：
 * - 定义 `DarvinEvent` union（事件流）
 * - 定义请求/响应类型
 * - 定义 `DarvinApi` 接口（contextBridge 暴露给 renderer）
 *
 * 修改这里 = 改契约。
 */

export type DarvinRuntimeStatus = 'ready' | 'offline' | 'no-binary' | 'online';

export type DarvinModelId =
  | 'claude-sonnet-4-5'
  | 'claude-opus-4-5'
  | 'gpt-4o';

export interface DarvinSession {
  id: string;
  title: string;
  updatedAt: number;
}

export interface DarvinMessage {
  id: string;
  sessionId: string;
  role: 'user' | 'assistant';
  content: string;
  done: boolean;
  error?: string;
  createdAt: number;
}

// ── Event 流（从 agent → renderer）───────────────────────────────────
export type DarvinEvent =
  | { type: 'text_delta'; messageId: string; delta: string }
  | { type: 'thinking_delta'; messageId: string; delta: string }
  | { type: 'tool_start'; messageId: string; tool: string; input: unknown }
  | { type: 'tool_end'; messageId: string; tool: string; output: unknown }
  | { type: 'done'; messageId: string }
  | { type: 'error'; messageId: string; message: string }
  | { type: 'agent_end' };

// ── Request / Response ────────────────────────────────────────────────
export interface DarvinPromptRequest {
  content: string;
  sessionId: string;
  model?: DarvinModelId;
}

export interface DarvinPromptResponse {
  sessionId: string;
  messageId: string;
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

// ── contextBridge 暴露给 renderer 的 API ─────────────────────────────
export interface DarvinApi {
  prompt(req: DarvinPromptRequest): Promise<DarvinPromptResponse>;
  abort(sessionId: string): Promise<DarvinAbortResponse>;
  listSessions(): Promise<DarvinListSessionsResponse>;
  getMessages(sessionId: string): Promise<DarvinGetMessagesResponse>;
  onEvent(handler: (e: DarvinEvent) => void): () => void;
  status(): DarvinRuntimeStatus;
}
