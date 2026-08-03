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

/** spec 12 — 附件路径引用：只记原始绝对路径，不复制进工作区。 */
export interface DarvinAttachmentRef {
  path: string;
  name: string;
  size: number;
}

/**
 * spec 13 — 图片附件：路径 + base64 dataUrl。渲染层读为 dataUrl 后随 prompt
 * 发给 Go，由 Go 转 image content block 供模型真正看到图。
 */
export interface DarvinImageRef {
  path: string;
  name: string;
  size: number;
  dataUrl: string;
}

/** spec 13 — main 把本地文件读为 base64 dataUrl 的结果（>10MB 返回 error）。 */
export interface DarvinReadFileDataUrlResponse {
  success: boolean;
  dataUrl?: string;
  error?: string;
}

export type DarvinDangerLevel = 'safe' | 'caution' | 'destructive';

/** spec 12 — Go → renderer 的权限审批事件 payload。 */
export interface DarvinPermissionRequest {
  requestId: string;
  toolName: string;
  toolInput: unknown;
  dangerLevel: DarvinDangerLevel;
  reason: string;
}

export type DarvinPermissionBehavior = 'allow' | 'deny';

/** spec 12 — renderer → Go 的审批响应。 */
export interface DarvinPermissionResponse {
  sessionId: string;
  requestId: string;
  behavior: DarvinPermissionBehavior;
  /** allow 时可编辑的入参；deny 时忽略。 */
  updatedInput?: unknown;
  /** deny 时附带的拒绝消息（会回传给 agent）。 */
  message?: string;
  interrupt?: boolean;
  /** allow 时勾选「记住此会话」→ Go 侧记录规则，后续同操作自动放行。 */
  remember?: boolean;
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
      type: 'permission_request';
      sessionId: string;
      requestId: string;
      toolName: string;
      toolInput: unknown;
      dangerLevel: 'safe' | 'caution' | 'destructive';
      reason: string;
    }
  | {
      type: 'artifact';
      sessionId: string;
      artifactId: string;
      kind: DarvinArtifactKind;
      name?: string;
      content: string;
      /** html 引用 workspace 内文件时携带（相对 workspace 根）；走本地预览服务。 */
      filePath?: string;
      /** 产出该 artifact 的 assistant 消息 id；聊天消息内卡片组按它挂载（向后兼容，可缺省）。 */
      messageId?: string;
      createdAt: number;
    };

export interface DarvinPromptRequest {
  content: string;
  model?: DarvinModelId;
  /** spec 12 — 本条消息暂存的附件（绝对路径）：「附加即授权」，agent 可免审批读取。 */
  attachments?: string[];
  /** spec 13 — 本条消息的图片附件（base64 dataUrl），Go 转 image content block。 */
  images?: DarvinImageRef[];
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

/** 设置面板可编辑的 LLM provider。runtime 是否可用取决于 Go 侧注册情况。 */
export type DarvinModelProvider = 'anthropic' | 'openai' | 'custom';

export interface DarvinLLMConfig {
  /** UI 当前编辑的 provider（不一定 runtime active）。 */
  provider: DarvinModelProvider;
  /** 当前运行时实际生效的 provider（Go 只注册了 anthropic）。 */
  activeProvider: string;
  apiKey: string;
  baseUrl: string;
  defaultModel: string;
  /** 各 provider 的独立凭据（openai / custom 预先存好，待 Go 接入后激活）。 */
  providers: Record<string, { apiKey: string; baseUrl: string; defaultModel: string }>;
}

// 重启 Go 子进程的反馈：UI 端用它判定是否要切到 "Restarting…"
export interface DarvinSetLLMConfigResponse {
  saved: boolean;
  restarted: boolean;
}

/** 通用设置：自动启动（OS 级）+ 通知 / 代理 + 记忆偏好（用户级 yaml）。 */
export interface DarvinAppPreferences {
  autoLaunch: boolean;
  notifications: boolean;
  proxy: string;
  memory: {
    enabled: boolean;
    embeddingProvider: string;
    apiKey: string;
  };
}

export type DarvinAppPreferencesPatch = Partial<{
  autoLaunch: boolean;
  notifications: boolean;
  proxy: string;
  memory: Partial<DarvinAppPreferences['memory']>;
}>;

/** 关于 tab 的运行时信息（版本 / 架构）。 */
export interface DarvinAppInfo {
  version: string;
  electron: string;
  platform: string;
  arch: string;
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

/** spec 12 — 设置会话工作目录的结果。canceled=true 表示用户取消或路径无效。 */
export interface DarvinSetWorkspaceResult {
  canceled: boolean;
  rootPath?: string;
  label?: string;
  error?: string;
}

/** spec 12 — 当前会话工作目录（renderer 只读视图；绝对路径本地下发给 FolderPicker 展示）。 */
export interface DarvinWorkspaceRootResult {
  rootPath: string | null;
  label: string | null;
}

/** spec 32 — 单 skill 的 renderer 视图。`path` 仅 main 用，renderer 不展示。 */
export interface DarvinSkillSummary {
  id: string;
  name: string;
  description: string;
  version?: string;
  enabled: boolean;
  isOfficial: boolean;
  isBuiltIn: boolean;
  path: string;
  source: 'bundled' | 'user' | 'github' | 'npm';
  updatedAt: number;
  riskLevel?: 'safe' | 'low' | 'medium' | 'high' | 'critical';
  riskFindings?: Array<{
    dimension: string;
    severity: 'info' | 'warning' | 'danger' | 'critical';
    message: string;
    file: string;
    line: number;
  }>;
}

export interface DarvinListSkillsResponse {
  skills: DarvinSkillSummary[];
}

export interface DarvinSetSkillEnabledRequest {
  skillId: string;
  enabled: boolean;
}

export interface DarvinSetSkillEnabledResponse {
  ok: boolean;
}

export type DarvinSkillRiskLevel = 'safe' | 'low' | 'medium' | 'high' | 'critical';

export type DarvinSkillFindingSeverity = 'info' | 'warning' | 'danger' | 'critical';

export interface DarvinSkillFinding {
  dimension: string;
  severity: DarvinSkillFindingSeverity;
  message: string;
  file: string;
  line: number;
}

export interface DarvinInstallSkillRequest {
  /** 本地 SKILL.md 绝对路径 或 GitHub owner/repo 或 https URL。 */
  source: string;
}

export interface DarvinInstallSkillResponse {
  skill: DarvinSkillSummary;
  riskLevel: DarvinSkillRiskLevel;
  riskScore?: number;
  riskFindings?: DarvinSkillFinding[];
}

export interface DarvinUninstallSkillRequest {
  skillId: string;
}

export interface DarvinUninstallSkillResponse {
  ok: boolean;
}

export interface DarvinUpgradeSkillRequest {
  skillId: string;
}

export interface DarvinUpgradeSkillResponse {
  skill: DarvinSkillSummary;
}

export interface DarvinGetSkillDetailsRequest {
  skillId: string;
}

export interface DarvinGetSkillDetailsResponse {
  skill: DarvinSkillSummary;
  body: string;
  scripts?: Array<{ path: string; content: string }>;
}

/** spec 12 — 选取待附加文件（只记路径，不复制）。 */
export interface DarvinPickAttachmentsResponse {
  attachments: DarvinAttachmentRef[];
}

/** workspace 目录里单个文件的 renderer 视图（list_workspace_files 返回）。 */
export interface DarvinWorkspaceFileInfo {
  /** 相对 workspace 根（`/` 分隔），绝对路径不下发。 */
  relativePath: string;
  name: string;
  kind: DarvinArtifactKind;
  size: number;
  modifiedAt: number;
}

export interface DarvinListWorkspaceFilesResponse {
  files: DarvinWorkspaceFileInfo[];
}

export interface DarvinReadWorkspaceFileResponse {
  success: boolean;
  content?: string;
  size?: number;
  error?: string;
}

export interface DarvinOpenWorkspaceFileResponse {
  success: boolean;
  error?: string;
}

export const DarvinPushEvent = {
  SessionsChanged: 'darvin:push:sessions-changed',
  ActiveSessionChanged: 'darvin:push:active-session-changed',
  SessionEvent: 'darvin:push:session-event',
  WorkspaceChanged: 'darvin:push:workspace-changed',
  SkillsChanged: 'darvin:push:skills-changed',
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
  /** 写入用户级 yaml；anthropic 会重启 Go 子进程，openai/custom 仅存 providers 块（Go 未接入）。 */
  setLLMConfig(req: { provider: string; apiKey: string; baseUrl?: string; defaultModel?: string }): Promise<DarvinSetLLMConfigResponse>;

  /** 读取通用偏好（自动启动来自 OS，通知 / 代理 / 记忆来自用户级 yaml）。 */
  getAppPreferences(): Promise<DarvinAppPreferences>;
  /** 写入通用偏好 patch：autoLaunch 走 app.setLoginItemSettings，其余写用户级 yaml。 */
  setAppPreferences(patch: DarvinAppPreferencesPatch): Promise<void>;

  /** 读取关于页的版本 / Electron / 平台 / 架构信息。 */
  getAppInfo(): Promise<DarvinAppInfo>;

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
  /** 递归列 workspace 目录文件（深度≤3、文件数≤500，超出静默截断）。 */
  listWorkspaceFiles(): Promise<DarvinListWorkspaceFilesResponse>;
  /** 读 workspace 内文本文件（≤256KB；非文本类型返回 unsupported）。 */
  readWorkspaceFile(relativePath: string): Promise<DarvinReadWorkspaceFileResponse>;
  /** 在系统文件管理器中定位 workspace 内文件。 */
  revealWorkspaceFile(relativePath: string): Promise<void>;
  /** 用系统默认应用打开 workspace 内文件。 */
  openWorkspaceFile(relativePath: string): Promise<DarvinOpenWorkspaceFileResponse>;
  /** workspace 内容变更 push（import / remove 后 main 广播）。 */
  onWorkspaceChanged(handler: (info: { sessionId: string; files: DarvinImportedFile[] }) => void): () => void;

  /** 为 file-based HTML artifact 起本地预览会话（relativePath 相对 workspace 根）。 */
  createArtifactPreviewSession(relativePath: string): Promise<DarvinCreateArtifactPreviewSessionResponse>;
  /** 销毁本地预览会话（renderer 卸载 iframe 时调用）。 */
  destroyArtifactPreviewSession(sessionId: string): Promise<DarvinDestroyArtifactPreviewSessionResponse>;

  /** spec 12 — 响应 Go 的权限审批请求（allow / deny）。 */
  respondPermission(r: DarvinPermissionResponse): Promise<void>;
  /** spec 12 — 弹文件选择框选取待附加文件（只记路径，不复制进工作区）。 */
  pickAttachments(): Promise<DarvinPickAttachmentsResponse>;
  /** spec 13 — 读本地文件为 base64 dataUrl（>10MB 返回 error）。 */
  readFileAsDataUrl(path: string): Promise<DarvinReadFileDataUrlResponse>;
  /** spec 12 — 弹目录选择框设置当前会话工作目录。 */
  setWorkspaceRoot(): Promise<DarvinSetWorkspaceResult>;
  /** spec 12 — 把当前会话工作目录设为指定路径（最近目录 / 测试）。 */
  setWorkspaceRootTo(path: string): Promise<DarvinSetWorkspaceResult>;
  /** spec 12 — 读取当前会话工作目录（绝对路径 + basename）。 */
  getWorkspaceRoot(): Promise<DarvinWorkspaceRootResult>;

  /** spec 32 — 列出当前已知 skill（bundled + user，enabled 状态来自 main 端 SQLite）。 */
  listSkills(): Promise<DarvinListSkillsResponse>;
  /** spec 32 — 切换 skill 启用状态。main 写 SQLite 后通过 agent.skills.set_enabled 同步到 Go。 */
  setSkillEnabled(req: DarvinSetSkillEnabledRequest): Promise<DarvinSetSkillEnabledResponse>;
  /** spec 32 — 订阅 skills 列表变更（bootstrap 完成、fs watcher 触发、Go 端 emit changed）。 */
  onSkillsChanged(handler: (skills: DarvinSkillSummary[]) => void): () => void;

  /** spec 33 — 装一个 skill（本地 SKILL.md 或 GitHub URL）。main 端做安全扫描后
   *  返回 riskLevel；medium 时 renderer 弹安全报告 modal 让用户确认。v0 main 端
   *  该方法为占位（另一个 spec 才真接 scanner），本方法目前由 main stub 返回
   *  「规划中」语义——renderer 按 riskLevel='safe' 直接装即可。 */
  installSkill(req: DarvinInstallSkillRequest): Promise<DarvinInstallSkillResponse>;
  /** spec 33 — 卸载一个 user skill。bundled skill 由 main 端拒绝。 */
  uninstallSkill(req: DarvinUninstallSkillRequest): Promise<DarvinUninstallSkillResponse>;
  /** spec 33 — 升级一个 user skill 到新版本（GitHub 源；v0 stub）。 */
  upgradeSkill(req: DarvinUpgradeSkillRequest): Promise<DarvinUpgradeSkillResponse>;
  /** spec 33 — 拉取 skill 详情（SKILL.md body + 同目录脚本），用于详情 modal。 */
  getSkillDetails(req: DarvinGetSkillDetailsRequest): Promise<DarvinGetSkillDetailsResponse>;
}