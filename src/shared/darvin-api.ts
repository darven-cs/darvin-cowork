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

/**
 * 穷尽检查兜底：switch 覆盖全部 union 成员后 default 分支调用它，编译期保证
 * 新增成员时必须显式处理，否则落进这个 throw。
 */
export function assertNever(x: never): never {
  throw new Error(`unhandled union member: ${JSON.stringify(x)}`);
}

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

/** 工具种类。兜底 `string & { __brand?: never }` 允许自定义工具名。 */
export type DarvinToolKind =
  | 'bash' | 'read' | 'write' | 'edit' | 'todowrite'
  | 'web_search' | 'web_fetch' | 'image_gen' | 'video_gen'
  | (string & { __brand?: never });

/** artifact 渲染种类（spec 05 artifact-panel 的 10 种渲染器）。 */
export type DarvinArtifactKind =
  | 'html' | 'svg' | 'image' | 'video' | 'mermaid'
  | 'code' | 'markdown' | 'text' | 'document' | 'local-service'
  | (string & { __brand?: never });

/**
 * user 消息附件：image 用 base64 / dataURL（src），file 用相对 workspace 路径。
 */
export interface DarvinAttachment {
  id: string;
  kind: 'image' | 'file';
  name: string;
  size: number;
  mimeType: string | null;
  src: string;
}

/**
 * discriminated union：渲染层按 `type` 分发（ConversationTurn / ToolCallGroup /
 * ArtifactRenderer）。user / assistant 成员保留 done / error / toolLabel 作为
 * 老 Go wire 的向后兼容超集；tool_use / tool_result / system 为协议层新增形态。
 */
export type DarvinMessage =
  | {
      id: string;
      sessionId: string;
      type: 'user';
      content: string;
      done: boolean;
      error?: string;
      createdAt: number;
      attachments?: DarvinAttachment[];
    }
  | {
      id: string;
      sessionId: string;
      type: 'assistant';
      content: string;
      isStreaming: boolean;
      isThinking?: boolean;
      done: boolean;
      error?: string;
      toolLabel?: string;
      usage?: DarvinUsage;
      model?: string;
      createdAt: number;
    }
  | {
      id: string;
      sessionId: string;
      type: 'tool_use';
      toolUseId: string;
      tool: string;
      toolKind: DarvinToolKind;
      input: unknown;
      createdAt: number;
    }
  | {
      id: string;
      sessionId: string;
      type: 'tool_result';
      toolUseId: string;
      tool: string;
      output: unknown;
      isError: boolean;
      createdAt: number;
    }
  | {
      id: string;
      sessionId: string;
      type: 'system';
      content: string;
      createdAt: number;
    };

export interface DarvinUsage {
  inputTokens: number;
  outputTokens: number;
  cacheReadTokens?: number;
  cacheWriteTokens?: number;
  totalTokens: number;
}

/** 上下文占用 5 态（spec 03 / 04 的圆环 + 压缩联动）。 */
export type DarvinContextUsageStatus =
  | 'unknown' | 'normal' | 'warning' | 'danger' | 'compacting';

/**
 * 单 session 的上下文用量快照。main / Go 按 session 推送，renderer 以
 * contextUsageBySessionId 维护（spec 03）。
 */
export interface DarvinContextUsage {
  sessionId: string;
  usedTokens?: number;
  contextTokens?: number;
  percent?: number;
  status: DarvinContextUsageStatus;
  compactionCount?: number;
  latestCompactionAt?: number;
  latestCompactionReason?: string;
  /** 当前正在进行的压缩来源（手动 / 自动），圆环 tooltip 区分展示。 */
  compactionReason?: 'manual' | 'auto';
  model?: string;
  updatedAt: number;
}

/**
 * 归一化 helper：老 Go 的扁平 wire（role 在顶层，无 type）与新 union 都收敛
 * 成 renderer 能直接消费的 role / content。协议切换期两种 shape 并存。
 */
export function darvinMessageRole(m: DarvinMessage): 'user' | 'assistant' {
  const legacy = m as DarvinMessage & { role?: string };
  if (legacy.role !== undefined) return legacy.role === 'user' ? 'user' : 'assistant';
  return m.type === 'user' ? 'user' : 'assistant';
}

export function darvinMessageContent(m: DarvinMessage): string {
  const role = (m as unknown as { role?: string }).role;
  if (role !== undefined) return (m as unknown as { content: string }).content;
  switch (m.type) {
    case 'user':
    case 'assistant':
    case 'system':
      return m.content;
    case 'tool_use':
    case 'tool_result':
      return `[${m.tool}]`;
  }
}

/**
 * 每条事件携带 sessionId + runId，让 renderer 端按 session 路由、上层
 * 派生 running / unread 状态。本期先标 optional（PR 4 起 Go 开始注入，
 * PR 6 起转强类型）——加 optional 不破坏现有可选字段的 union 收紧规则。
 */
export type DarvinEvent =
  | { type: 'text_delta'; sessionId?: string; runId?: string; messageId: string; delta: string }
  | { type: 'thinking_delta'; sessionId?: string; runId?: string; messageId: string; delta: string }
  | { type: 'tool_start'; sessionId?: string; runId?: string; messageId: string; toolUseId?: string; tool: string; input: unknown }
  | { type: 'tool_end'; sessionId?: string; runId?: string; messageId: string; toolUseId?: string; tool: string; output: unknown }
  | { type: 'done'; sessionId?: string; runId?: string; messageId: string; usage?: DarvinUsage }
  | { type: 'error'; sessionId?: string; runId?: string; messageId: string; message: string }
  | { type: 'agent_end'; sessionId?: string; runId?: string }
  | {
      type: 'compaction';
      sessionId: string;
      runId: string;
      reason: 'auto' | 'manual';
      checkpointId: string;
      createdAt: number;
      /** Go CompactionEvent 携带压缩前后 token 数，toast / divider 展示。 */
      beforeTokens?: number;
      afterTokens?: number;
    }
  | { type: 'context_usage'; sessionId: string; usage: DarvinContextUsage }
  | {
      type: 'artifact';
      sessionId: string;
      artifactId: string;
      kind: DarvinArtifactKind;
      name?: string;
      content: string;
      /** html 引用 workspace 内文件时携带（相对 workspace 根）；走本地预览服务。 */
      filePath?: string;
      createdAt: number;
    };

export interface DarvinPromptRequest {
  content: string;
  model?: DarvinModelId;
}

export interface DarvinPromptResponse {
  sessionId: string;
  messageId: string;
  /** 本次 prompt 在 main 端生成的 runId（UUIDv4），abort / 事件路由用 */
  runId?: string;
  /** true 表示该 turn 落在同 session 的 followUpQueue,要等上一条完成才真正起跑 */
  queued?: boolean;
}

export interface DarvinAbortResponse {
  aborted: boolean;
  sessionId: string;
}

/**
 * 手动压缩结果。`accepted:false` 表示 Go 未就绪 / 会话不在可压状态
 * （不进入 compacting 动画、不 toast，避免假压缩）；`accepted:true`
 * 表示压缩管线已启动，最终成败由随后的 `compaction` 事件驱动 UI。
 */
export interface DarvinCompactContextResponse {
  accepted: boolean;
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

export interface DarvinRenameSessionResponse {
  session: DarvinSession;
}

export interface DarvinActiveSessionResponse {
  sessionId: string | null;
}

/** 搜索结果里的一条消息命中：携带所属会话信息，供搜索页分组展示。 */
export interface DarvinSearchHit {
  sessionId: string;
  sessionTitle: string;
  message: DarvinMessage;
}

export interface DarvinSearchSessionsResponse {
  sessions: DarvinSession[];
  messages: DarvinSearchHit[];
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

/** 用户导入到 session workspace 的文件（renderer 视图）。 */
export interface DarvinImportedFile {
  id: string;
  originalName: string; // basename
  relativePath: string;
  size: number;
  mimeType: string | null;
  sha256: string;
  importedAt: number;
}

export type DarvinImportErrorCode =
  | 'too_large'
  | 'unsupported_type'
  | 'workspace_full'
  | 'source_unreadable'
  | 'copy_failed'
  | 'name_conflict';

export interface DarvinImportFilesResponse {
  imported: DarvinImportedFile[];
  skipped: Array<{ sourcePath: string; reason: DarvinImportErrorCode; message: string }>;
}

export interface DarvinListImportedFilesResponse {
  files: DarvinImportedFile[];
  workspaceBytes: number;
}

export interface DarvinCreateArtifactPreviewSessionResponse {
  success: boolean;
  sessionId?: string;
  url?: string;
  error?: string;
}

export interface DarvinDestroyArtifactPreviewSessionResponse {
  success: boolean;
}

export interface DarvinRemoveImportedFileResponse {
  removed: boolean;
}

export interface DarvinWorkspaceInfoResponse {
  workspaceBytes: number;
  /** workspace 根的展示名（basename）；绝对路径不下发。 */
  label?: string;
}

export const DarvinPushEvent = {
  SessionsChanged: 'darvin:push:sessions-changed',
  ActiveSessionChanged: 'darvin:push:active-session-changed',
  SessionEvent: 'darvin:push:session-event',
  WorkspaceChanged: 'darvin:push:workspace-changed',
} as const;
export type DarvinPushEvent = typeof DarvinPushEvent[keyof typeof DarvinPushEvent];

export interface DarvinApi {
  createSession(req?: { title?: string }): Promise<DarvinCreateSessionResponse>;
  listSessions(): Promise<DarvinListSessionsResponse>;
  switchSession(sessionId: string): Promise<DarvinSwitchSessionResponse>;
  deleteSession(sessionId: string): Promise<DarvinDeleteSessionResponse>;
  renameSession(sessionId: string, title: string): Promise<DarvinRenameSessionResponse>;
  getActiveSession(): Promise<DarvinActiveSessionResponse>;
  searchSessions(query: string): Promise<DarvinSearchSessionsResponse>;
  onSessionsChanged(handler: (sessions: DarvinSession[]) => void): () => void;
  onActiveSessionChanged(handler: (sessionId: string | null) => void): () => void;

  getMessages(sessionId: string): Promise<DarvinGetMessagesResponse>;

  prompt(req: DarvinPromptRequest): Promise<DarvinPromptResponse>;
  abort(): Promise<DarvinAbortResponse>;

  /** 手动触发当前 session 上下文压缩。 */
  compactContext(sessionId: string): Promise<DarvinCompactContextResponse>;

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

  /** 弹系统文件选择框导入文件到当前 session workspace；返回导入/跳过明细。 */
  importFiles(): Promise<DarvinImportFilesResponse>;
  /** 列出当前 session workspace 已导入文件 + 已用字节数。 */
  listImportedFiles(): Promise<DarvinListImportedFilesResponse>;
  /** 从 workspace 移除一个已导入文件（含 DB 行与磁盘文件）。 */
  removeImportedFile(relativePath: string): Promise<DarvinRemoveImportedFileResponse>;
  /** 查询当前 session workspace 已用字节数（不透传 workspace 绝对路径）。 */
  getWorkspaceInfo(): Promise<DarvinWorkspaceInfoResponse>;
  /** 在系统文件管理器中打开 workspace 目录（main 端执行）。 */
  revealWorkspace(): Promise<void>;
  /** workspace 内容变更 push（import / remove 后 main 广播）。 */
  onWorkspaceChanged(handler: (info: { sessionId: string; files: DarvinImportedFile[] }) => void): () => void;

  /** 为 file-based HTML artifact 起本地预览会话（relativePath 相对 workspace 根）。 */
  createArtifactPreviewSession(relativePath: string): Promise<DarvinCreateArtifactPreviewSessionResponse>;
  /** 销毁本地预览会话（renderer 卸载 iframe 时调用）。 */
  destroyArtifactPreviewSession(sessionId: string): Promise<DarvinDestroyArtifactPreviewSessionResponse>;
}