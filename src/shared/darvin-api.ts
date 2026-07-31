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
 * `claudeSessionId` 等 Go 后端真支持多 session 时再回填（v1 占位）。
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

// ── Event 流（从 agent → main → renderer）─────────────────────────────
//
// 后端（Go agent）v0 单 session 时，所有 backend event 被 EventRouter
// 路由到 main.activeSessionId 对应的视图，再推给 renderer。renderer
// 拿到的事件 `sessionId` 字段就是当前 main 激活的 session，不是 backend
// 的原始 sessionId。
export interface DarvinUsage {
  inputTokens: number;
  outputTokens: number;
  totalTokens: number;
}

export type DarvinEvent =
  | { type: 'text_delta'; messageId: string; delta: string }
  | { type: 'thinking_delta'; messageId: string; delta: string }
  | { type: 'tool_start'; messageId: string; tool: string; input: unknown }
  | { type: 'tool_end'; messageId: string; tool: string; output: unknown }
  | { type: 'done'; messageId: string; usage?: DarvinUsage }
  | { type: 'error'; messageId: string; message: string }
  | { type: 'agent_end' };

// ── Request / Response ────────────────────────────────────────────────
export interface DarvinPromptRequest {
  content: string;
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

// ── LLM 配置（设置面板读写主进程侧的用户级 yaml）──────────────────
export interface DarvinLLMConfig {
  provider: 'anthropic';
  apiKey: string;          // 当前值；空串表示尚未配置
  baseUrl: string;
}

// 重启 Go 子进程的反馈：UI 端用它判定是否要切到 "Restarting…"
export interface DarvinSetLLMConfigResponse {
  saved: boolean;
  restarted: boolean;
}

// ── locale（renderer i18n 语言选择）──────────────────────────────────
//
// 持久化到与 LLM 配置同一个用户级 yaml；主进程只管存 / 取，不做重启。
// renderer 拿到值后调 `setLang()`，组件因 `currentLang` 是 Vue ref
// 自动 re-render，无需 reload。
export type DarvinLocale = 'zh' | 'en';

export interface DarvinLocaleResponse {
  locale: DarvinLocale;
}

// ── main → renderer push 事件名 ───────────────────────────────────────
//
// main 用 `webContents.send` 单向下推，renderer 用 `onSessionsChanged`
// / `onActiveSessionChanged` 订阅。push 载荷只带最小可序列化形状。
export const DarvinPushEvent = {
  SessionsChanged: 'darvin:push:sessions-changed',
  ActiveSessionChanged: 'darvin:push:active-session-changed',
  SessionEvent: 'darvin:push:session-event',
} as const;
export type DarvinPushEvent = typeof DarvinPushEvent[keyof typeof DarvinPushEvent];

// ── contextBridge 暴露给 renderer 的 API ─────────────────────────────
export interface DarvinApi {
  // session 管理（data owner = main）
  createSession(req?: { title?: string }): Promise<DarvinCreateSessionResponse>;
  listSessions(): Promise<DarvinListSessionsResponse>;
  switchSession(sessionId: string): Promise<DarvinSwitchSessionResponse>;
  deleteSession(sessionId: string): Promise<DarvinDeleteSessionResponse>;
  getActiveSession(): Promise<DarvinActiveSessionResponse>;
  onSessionsChanged(handler: (sessions: DarvinSession[]) => void): () => void;
  onActiveSessionChanged(handler: (sessionId: string | null) => void): () => void;

  // 消息查询
  getMessages(sessionId: string): Promise<DarvinGetMessagesResponse>;

  // prompt / abort（不传 sessionId：main 知道当前 active 是哪个）
  prompt(req: DarvinPromptRequest): Promise<DarvinPromptResponse>;
  abort(): Promise<DarvinAbortResponse>;

  // streaming 事件订阅（main 已经按 activeSessionId 过滤完）
  onEvent(handler: (e: DarvinEvent) => void): () => void;

  // LLM 配置
  /** 返回当前生效的 LLM 配置（来自用户级 yaml，未配置时 apiKey 为空串）。 */
  getLLMConfig(): Promise<DarvinLLMConfig>;
  /** 写入用户级 yaml + 重启 Go 子进程，subprocess 重启后才会用到新 key。 */
  setLLMConfig(req: { apiKey: string; baseUrl?: string }): Promise<DarvinSetLLMConfigResponse>;

  // locale
  /** 读取持久化的 UI 语言；首次启动或未写入时回落到 'zh'。 */
  getLocale(): Promise<DarvinLocaleResponse>;
  /** 写入用户级 yaml；renderer 收到返回值后再 `setLang()`，组件因 ref 自动 re-render。 */
  setLocale(req: { locale: DarvinLocale }): Promise<void>;

  // 运行时状态
  /** 走 IPC 异步查询：sendSync 会阻塞 renderer 线程，不用。 */
  status(): Promise<DarvinRuntimeStatus>;
}