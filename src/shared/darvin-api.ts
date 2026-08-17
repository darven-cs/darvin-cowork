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
  | 'claude-sonnet-4-6'
  | 'claude-sonnet-4-5'
  | 'claude-opus-4-7'
  | 'claude-opus-4-6'
  | 'claude-opus-4-5'
  | 'gpt-5.5'
  | 'gpt-5.4'
  | 'gpt-4o';

export type DarvinSessionStatus = 'active' | 'archived';

/**
 * session 的 renderer 视图：id 由 main 端 uuidv4 生成，renderer 不参与生成。
 */
export interface DarvinSession {
  id: string;
  title: string;
  /** 所属 workspace id；历史/迁移前会话可能缺失。 */
  workspaceId?: string;
  createdAt: number;
  updatedAt: number;
  status?: DarvinSessionStatus;
  claudeSessionId?: string | null;
  /** 会话级系统提示词；空 = 未设置。 */
  systemPrompt?: string;
  /** 会话级人设；空 = 未注入 <IDENTITY>。 */
  identity?: string;
}

/** 工具种类。兜底 `string & { __brand?: never }` 允许自定义工具名。 */
export type DarvinToolKind =
  | 'bash' | 'read' | 'write' | 'edit' | 'todowrite'
  | 'web_search' | 'web_fetch' | 'image_gen' | 'video_gen'
  | (string & { __brand?: never });

/** artifact 渲染种类（10 种渲染器）。 */
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

/** 附件路径引用：只记原始绝对路径，不复制进工作区。 */
export interface DarvinAttachmentRef {
  path: string;
  name: string;
  size: number;
}

/**
 * 图片附件：路径 + base64 dataUrl。渲染层读为 dataUrl 后随 prompt
 * 发给 Go，由 Go 转 image content block 供模型真正看到图。
 */
export interface DarvinImageRef {
  path: string;
  name: string;
  size: number;
  dataUrl: string;
}

/** main 把本地文件读为 base64 dataUrl 的结果（>10MB 返回 error）。 */
export interface DarvinReadFileDataUrlResponse {
  success: boolean;
  dataUrl?: string;
  error?: string;
}

export type DarvinDangerLevel = 'safe' | 'caution' | 'destructive';

/** Go → renderer 的权限审批事件 payload。 */
export interface DarvinPermissionRequest {
  requestId: string;
  toolName: string;
  toolInput: unknown;
  dangerLevel: DarvinDangerLevel;
  reason: string;
}

export type DarvinPermissionBehavior = 'allow' | 'deny';

/** renderer → Go 的审批响应。 */
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

/** 上下文占用 5 态（圆环 + 压缩联动）。 */
export type DarvinContextUsageStatus =
  | 'unknown' | 'normal' | 'warning' | 'danger' | 'compacting';

/**
 * 单 session 的上下文用量快照。main / Go 按 session 推送，renderer 以
 * contextUsageBySessionId 维护。
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
  | {
      type: 'tool_start';
      sessionId?: string;
      runId?: string;
      messageId: string;
      toolUseId?: string;
      tool: string;
      /** 工具来源（builtin / skill / mcp）；旧事件不带此字段。 */
      toolKind?: DarvinToolKind;
      /** toolKind='skill' 时非空。 */
      skillId?: string;
      /** toolKind='mcp' 时非空。 */
      mcpServerId?: string;
      input: unknown;
    }
  | {
      type: 'tool_end';
      sessionId?: string;
      runId?: string;
      messageId: string;
      toolUseId?: string;
      tool: string;
      toolKind?: DarvinToolKind;
      skillId?: string;
      mcpServerId?: string;
      output: unknown;
    }
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
      /** local-service 等带 URL 的 artifact 的访问地址。 */
      url?: string;
      /** 产出该 artifact 的 assistant 消息 id；聊天消息内卡片组按它挂载（向后兼容，可缺省）。 */
      messageId?: string;
      createdAt: number;
    };

export interface DarvinPromptRequest {
  content: string;
  /** 本条消息的 provider key + model 覆盖（仅当次生效；缺省用 session 默认）。 */
  provider?: string;
  model?: string;
  /** 本条消息暂存的附件（绝对路径）：「附加即授权」，agent 可免审批读取。 */
  attachments?: string[];
  /** 本条消息的图片附件（base64 dataUrl），Go 转 image content block。 */
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

/** `/skill-name args` 用户显式触发 skill。 */
export interface DarvinInvokeSkillRequest {
  sessionId: string;
  skillId: string;
  /** 命令中 skill 名之后的部分（可为空串）。 */
  args?: string;
  /** 用户原始输入 `/skill-name args`（可选；缺失时 Go 端按 skillId+args 重建）。 */
  content?: string;
}

/** agent.skill.invoke_user 的返回；shape 对齐 DarvinPromptResponse 以便 renderer 起 assistant bubble。 */
export interface DarvinInvokeSkillResponse {
  ok: boolean;
  sessionId: string;
  messageId: string;
  runId?: string;
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

/** 持久化的 per-session usage 快照,RPC agent.get_session_usage 返回。 */
export interface DarvinSessionUsage {
  /** 最近一次 LLM turn 的 prompt token 数。 */
  lastPromptTokens: number;
  /** 最近一次 LLM turn 的 completion token 数。 */
  lastCompletionTokens: number;
  /** lastPromptTokens + lastCompletionTokens — renderer 据此算 context fill。 */
  lastUsedTokens: number;
  /** 最近一次 turn 的 cache hit token 数。 */
  lastCacheReadTokens: number;
  /** 最近一次 turn 的 cache write token 数。 */
  lastCacheWriteTokens: number;
  /** 最近一次 turn 的 model context window(Go 端 model registry 缓存的值)。 */
  lastContextTokens: number;
  /** 最近一次 turn 的 fill percent(0-100),与 live context_usage 事件同源。 */
  lastPercent: number;
  /** 最近一次 turn 用的 model id;renderer 用它查 context window 算 percent。 */
  lastModel?: string;
  /** 该 session 累计调 LLM 次数。 */
  requestCount: number;
  /** session 累计 prompt token。 */
  totalPromptTokens: number;
  /** session 累计 completion token。 */
  totalCompletionTokens: number;
  /** 快照落盘 unix ms。 */
  updatedAt: number;
}

export interface DarvinGetSessionUsageResponse {
  /** 全零字段表示该 session 没有快照(renderer 走空态分支)。 */
  usage: DarvinSessionUsage;
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

/** 设置面板可编辑的 LLM provider key（完整预设见 src/shared/providers.ts）。 */
export type DarvinModelProvider = string;

/** Go ModelRegistry 暴露给 renderer 的模型元数据（无凭据 / 成本）。 */
export interface DarvinModelInfo {
  id: string;
  name: string;
  /** provider preset key（anthropic / openai / gemini / ...）。 */
  provider: string;
  /** wire 格式：anthropic-messages | openai-completions | ... */
  apiKind: string;
  contextWindow: number;
  maxTokens: number;
  reasoning: boolean;
  input: string[];
}

/** providers.<key> 的独立凭据块（api_format 由共享目录推导，写入 yaml）。 */
export interface DarvinProviderConfig {
  apiFormat?: string;
  apiKey: string;
  baseUrl: string;
  defaultModel: string;
}

export interface DarvinLLMConfig {
  /** UI 当前编辑的 provider（不一定 runtime active）。 */
  provider: DarvinModelProvider;
  /** 当前运行时实际生效的 provider key（来自 yaml llm.provider）。 */
  activeProvider: string;
  apiKey: string;
  baseUrl: string;
  defaultModel: string;
  /** 各 provider 的独立凭据（key 见 src/shared/providers.ts 目录）。 */
  providers: Record<string, DarvinProviderConfig>;
  /** Go 侧实际注册的 provider wire 格式名（anthropic / openai）。 */
  registeredProviders: string[];
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

/** 设置会话工作目录的结果。canceled=true 表示用户取消或路径无效。 */
export interface DarvinSetWorkspaceResult {
  canceled: boolean;
  rootPath?: string;
  label?: string;
  error?: string;
}

/** 当前会话工作目录（renderer 只读视图；绝对路径本地下发给 FolderPicker 展示）。 */
export interface DarvinWorkspaceRootResult {
  rootPath: string | null;
  label: string | null;
}

/** Go agent.set_workspace 的响应：返回重锚后的绝对 rootPath。 */
export interface DarvinSetWorkspaceResponse {
  rootPath: string;
}

/** workspace 的 renderer 视图。label 为展示名（name 或 rootPath basename）；rootPath 本地透传供 FolderPicker 使用。 */
export interface DarvinWorkspace {
  id: string;
  name: string;
  label: string;
  rootPath: string;
  sessionCount: number;
  createdAt: number;
  updatedAt: number;
}

export interface DarvinListWorkspacesResponse {
  workspaces: DarvinWorkspace[];
}

export interface DarvinCreateWorkspaceResponse {
  workspace: DarvinWorkspace;
}

export interface DarvinActiveWorkspaceResponse {
  workspaceId: string | null;
}

export interface DarvinSetActiveWorkspaceResponse {
  workspaceId: string;
  /** 该 workspace 最近更新的会话（可能为 null：空工作区）。 */
  activeSessionId: string | null;
}

export interface DarvinDeleteWorkspaceResponse {
  deleted: boolean;
  nextActiveWorkspaceId: string | null;
  /** force 级联删除时移除的会话数；非 force 删除为 0。 */
  deletedSessionCount: number;
}

export interface DarvinBindSessionWorkspaceResponse {
  bound: boolean;
}

/** 单 skill 的 renderer 视图。`path` 仅 main 用，renderer 不展示。 */
export interface DarvinSkillSummary {
  id: string;
  name: string;
  description: string;
  version?: string;
  enabled: boolean;
  /** 是否可被 `/skill-name` 显式触发（SKILL.md invocation.userInvocable）。 */
  userInvocable: boolean;
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

/** agent.tools.list 返回的单个工具描述（内置 + skill + mcp 合并视图）。 */
export interface DarvinToolDescriptor {
  name: string;
  kind: string;
  description: string;
  inputSchema?: unknown;
  /** kind='skill' 时含 skillID；kind='mcp' 时含 mcpServerID + mcpToolName。 */
  metadata?: Record<string, unknown>;
}

export interface DarvinListToolsResponse {
  tools: DarvinToolDescriptor[];
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
  /** 本地 skill 目录绝对路径（含 SKILL.md，可选 references/、scripts/）
   * 或 GitHub owner/repo 或 https URL。 */
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

/** MCP transport 种类。stdio 走子进程 + Content-Length,
 * sse/http 走 streamable-HTTP。 */
export type DarvinMcpTransportType = 'stdio' | 'sse' | 'http';

/** resolver 优化后的 launch 状态。strings 对齐 Go 端
 * ResolutionStatus(IPC 协议字段)。 */
export type DarvinMcpLaunchStatus =
  | 'pending' | 'installing' | 'ready' | 'failed' | 'unsupported';

/** 实际 MCP 连接状态。connecting / connected / error 由
 * Go 端 registry 推 connection_changed;main 端只做转发。 */
export type DarvinMcpConnectionStatus =
  | 'disconnected' | 'connecting' | 'connected' | 'error';

/** 单个 tool 暴露给 agent / renderer 的视图。 */
export interface DarvinMcpServerExposedTool {
  name: string;
  description: string;
  inputSchema: Record<string, unknown>;
  /** MCP 工具安全注解（2025-03-26 协议）；缺省表示 server 未声明。 */
  readOnlyHint?: boolean;
  destructiveHint?: boolean;
  openWorldHint?: boolean;
}

/** 单 MCP server 的 renderer 视图;spec / status 合一。 */
export interface DarvinMcpServer {
  id: string;
  name: string;
  description: string;
  enabled: boolean;
  transportType: DarvinMcpTransportType;
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  url?: string;
  headers?: Record<string, string>;
  isBuiltIn: boolean;
  githubUrl?: string;
  registryId?: string;
  /** 工具调用权限策略："trusted" 放行、"ask"(默认) 对非只读工具弹确认。 */
  trustLevel?: 'trusted' | 'ask';
  createdAt: number;
  updatedAt: number;
  launchStatus?: DarvinMcpLaunchStatus;
  launchError?: string;
  /** 失败详情（来自 resolution_changed）：阶段 / 耗时(ms) / stderr 摘要。 */
  launchStage?: string;
  launchElapsedMs?: number;
  launchStderr?: string;
  connectionStatus?: DarvinMcpConnectionStatus;
  connectionError?: string;
  exposedTools?: DarvinMcpServerExposedTool[];
}

export interface DarvinMcpServerCreate {
  name: string;
  description?: string;
  enabled?: boolean;
  transportType: DarvinMcpTransportType;
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  url?: string;
  headers?: Record<string, string>;
  trustLevel?: 'trusted' | 'ask';
}

export interface DarvinMcpServerPatch {
  name?: string;
  description?: string;
  enabled?: boolean;
  transportType?: DarvinMcpTransportType;
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  url?: string;
  headers?: Record<string, string>;
  trustLevel?: 'trusted' | 'ask';
}

export interface DarvinListMcpServersResponse {
  servers: DarvinMcpServer[];
}

export interface DarvinCreateMcpServerResponse {
  server: DarvinMcpServer;
}

export interface DarvinUpdateMcpServerResponse {
  server: DarvinMcpServer;
}

export interface DarvinDeleteMcpServerResponse {
  ok: boolean;
}

export interface DarvinSetMcpServerEnabledRequest {
  id: string;
  enabled: boolean;
}

export interface DarvinSetMcpServerEnabledResponse {
  ok: boolean;
}

export interface DarvinTestMcpConnectionRequest {
  id: string;
}

export interface DarvinTestMcpConnectionResponse {
  ok: boolean;
  error?: string;
  tools?: DarvinMcpServerExposedTool[];
  /** 认证诊断三态：none / possible / required（Go 端 DiagnoseAuth 输出）。 */
  authStatus?: 'none' | 'possible' | 'required';
  /** 可操作建议文案（如「服务器需要 OAuth 授权」）。 */
  authSuggestion?: string;
}

export interface DarvinRetryMcpLaunchResolutionRequest {
  id: string;
}

export interface DarvinRetryMcpLaunchResolutionResponse {
  ok: boolean;
}

/** MCP resources/prompts（Go 端 resources.go 的 wire 形状）。 */
export interface DarvinMcpResource {
  uri: string;
  name?: string;
  description?: string;
  mimeType?: string;
}

export interface DarvinMcpResourceContent {
  uri: string;
  mimeType?: string;
  text?: string;
}

export interface DarvinMcpPromptArgument {
  name: string;
  description?: string;
  required?: boolean;
}

export interface DarvinMcpPrompt {
  name: string;
  description?: string;
  arguments?: DarvinMcpPromptArgument[];
}

export interface DarvinMcpPromptMessage {
  role: string;
  content?: string;
}

export interface DarvinMcpResourcesListResponse {
  resources: DarvinMcpResource[];
}

export interface DarvinMcpResourceReadResponse {
  contents: DarvinMcpResourceContent[];
}

export interface DarvinMcpPromptsListResponse {
  prompts: DarvinMcpPrompt[];
}

export interface DarvinMcpPromptGetResponse {
  messages: DarvinMcpPromptMessage[];
}

export interface DarvinMcpLogsResponse {
  lines: string[];
}

export interface DarvinMcpConnectionChangedEvent {
  id: string;
  status: DarvinMcpConnectionStatus;
  error?: string;
}

/** Go → main 的 resolution 推送。`updatedAt` 是 unix ms。 */
export interface DarvinMcpLaunchResolution {
  serverId: string;
  resolverKind: 'npx' | 'uvx' | 'go' | 'raw';
  sourceFingerprint: string;
  status: DarvinMcpLaunchStatus;
  packageName: string | null;
  requestedVersion: string | null;
  resolvedVersion: string | null;
  installDir: string | null;
  command: string | null;
  args: string[];
  env: Record<string, string>;
  error: string | null;
  /** 失败阶段（launch / initialize / tools-list 等）+ 耗时(ms) + stderr 摘要。 */
  failureStage?: string;
  failureElapsedMs?: number;
  failureStderr?: string;
  installedAt: number | null;
  resolvedAt: number | null;
  updatedAt: number;
}

export interface DarvinMcpResolutionChangedEvent {
  serverId: string;
  resolution: DarvinMcpLaunchResolution;
}

/** 选取待附加文件（只记路径，不复制）。 */
export interface DarvinPickAttachmentsResponse {
  attachments: DarvinAttachmentRef[];
}

/** 选取 skill 目录（必须含 SKILL.md，可选 references/、scripts/）。
 * 取消或失败时 canceled=true；成功时 path 是绝对路径。 */
export interface DarvinPickSkillFolderResponse {
  canceled: boolean;
  path?: string;
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

/** 本地服务探测结果（HTTP GET + <title> 提取，700ms 超时）。 */
export interface DarvinLocalServiceInfo {
  url: string;
  title: string;
  host: string;
  port: number;
  online: boolean;
}

export const DarvinPushEvent = {
  SessionsChanged: 'darvin:push:sessions-changed',
  ActiveSessionChanged: 'darvin:push:active-session-changed',
  SessionEvent: 'darvin:push:session-event',
  WorkspaceChanged: 'darvin:push:workspace-changed',
  WorkspacesChanged: 'darvin:push:workspaces-changed',
  SkillsChanged: 'darvin:push:skills-changed',
  McpServersChanged: 'darvin:push:mcp-servers-changed',
  McpConnectionChanged: 'darvin:push:mcp-connection-changed',
} as const;
export type DarvinPushEvent = typeof DarvinPushEvent[keyof typeof DarvinPushEvent];

/** 单个 sub-agent run 的 renderer 视图（Go agent.subagent.list wire）。 */
export interface SubagentRun {
  id: string;
  parentId: string;
  status: 'pending' | 'running' | 'done' | 'error' | 'aborted' | 'timeout';
  prompt: string;
  description: string;
  scope: string[];
  model: string;
  toolCallId?: string;
  startedAt: number;
  endedAt: number;
  toolCalls: number;
  errorMsg?: string;
  durationMs: number;
  resultText?: string;
  resultTruncated?: boolean;
}

/** sub-agent 会话里的一条消息（agent.subagent.get_messages wire）。 */
export interface SubagentMessage {
  id: string;
  sessionId: string;
  role: 'user' | 'assistant' | 'tool_use' | 'tool_result' | 'system';
  content: string;
  toolCalls?: string;
  createdAt: number;
  done?: boolean;
  error?: string | null;
  toolLabel?: string | null;
}

export interface DarvinSubagentListResponse {
  subagents: SubagentRun[];
}

export interface DarvinSubagentGetMessagesResponse {
  messages: SubagentMessage[];
}

export interface DarvinSubagentReadResultResponse {
  text: string;
}

export interface DarvinApi {
  createSession(req?: { title?: string; workspaceId?: string; systemPrompt?: string; identity?: string }): Promise<DarvinCreateSessionResponse>;
  listSessions(workspaceId?: string): Promise<DarvinListSessionsResponse>;
  switchSession(sessionId: string): Promise<DarvinSwitchSessionResponse>;
  deleteSession(sessionId: string): Promise<DarvinDeleteSessionResponse>;
  renameSession(sessionId: string, title: string): Promise<DarvinRenameSessionResponse>;
  getActiveSession(): Promise<DarvinActiveSessionResponse>;
  searchSessions(query: string): Promise<DarvinSearchSessionsResponse>;
  onSessionsChanged(handler: (sessions: DarvinSession[]) => void): () => void;
  onActiveSessionChanged(handler: (sessionId: string | null) => void): () => void;

  getMessages(sessionId: string): Promise<DarvinGetMessagesResponse>;
  /** 读该 session 持久化的 usage 快照。切换会话 / 冷启动时由 renderer 主动拉取。 */
  getSessionUsage(sessionId: string): Promise<DarvinGetSessionUsageResponse>;

  prompt(req: DarvinPromptRequest): Promise<DarvinPromptResponse>;
  /** `/skill-name args` 显式触发 skill；校验失败以 RPC error 返回。 */
  invokeSkill(req: DarvinInvokeSkillRequest): Promise<DarvinInvokeSkillResponse>;
  abort(): Promise<DarvinAbortResponse>;

  /** 手动触发当前 session 上下文压缩。 */
  compactContext(sessionId: string): Promise<DarvinCompactContextResponse>;

  onEvent(handler: (e: DarvinEvent) => void): () => void;

  /** 返回当前生效的 LLM 配置（来自用户级 yaml，未配置时 apiKey 为空串）。 */
  getLLMConfig(): Promise<DarvinLLMConfig>;
  /** 返回 Go 侧模型目录（agent.llm.list_models；Go 离线返回 []）。 */
  getLLMModels(): Promise<DarvinModelInfo[]>;
  /** 写入用户级 yaml 并重启 Go 子进程使新 provider / key 生效。 */
  setLLMConfig(req: { provider: string; apiKey: string; baseUrl?: string; defaultModel?: string; apiFormat?: string }): Promise<DarvinSetLLMConfigResponse>;

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
  /** 用系统默认浏览器打开外部 URL（http(s)/mailto/tel 白名单，其余返回 false）。 */
  openExternal(url: string): Promise<{ success: boolean }>;
  /** 批量探测本地服务在线状态 + <title>（main 端 HTTP GET，700ms 超时）。 */
  listLocalServices(urls: string[]): Promise<{ services: DarvinLocalServiceInfo[] }>;
  /** workspace 内容变更 push（import / remove 后 main 广播）。 */
  onWorkspaceChanged(handler: (info: { sessionId: string; files: DarvinImportedFile[] }) => void): () => void;

  /** 为 file-based HTML artifact 起本地预览会话（relativePath 相对 workspace 根）。 */
  createArtifactPreviewSession(relativePath: string): Promise<DarvinCreateArtifactPreviewSessionResponse>;
  /** 销毁本地预览会话（renderer 卸载 iframe 时调用）。 */
  destroyArtifactPreviewSession(sessionId: string): Promise<DarvinDestroyArtifactPreviewSessionResponse>;

  /** 响应 Go 的权限审批请求（allow / deny）。 */
  respondPermission(r: DarvinPermissionResponse): Promise<void>;
  /** 弹文件选择框选取待附加文件（只记路径，不复制进工作区）。 */
  pickAttachments(): Promise<DarvinPickAttachmentsResponse>;
  /** 弹目录选择框让用户挑一个含 SKILL.md 的 skill 文件夹。 */
  pickSkillFolder(): Promise<DarvinPickSkillFolderResponse>;
  /** 读本地文件为 base64 dataUrl（>10MB 返回 error）。 */
  readFileAsDataUrl(path: string): Promise<DarvinReadFileDataUrlResponse>;
  /** 弹目录选择框设置当前会话工作目录。 */
  setWorkspaceRoot(): Promise<DarvinSetWorkspaceResult>;
  /** 把当前会话工作目录设为指定路径（最近目录 / 测试）。 */
  setWorkspaceRootTo(path: string): Promise<DarvinSetWorkspaceResult>;
  /** 读取当前会话工作目录（绝对路径 + basename）。 */
  getWorkspaceRoot(): Promise<DarvinWorkspaceRootResult>;

  /** 列出全部 workspace（含会话数，label 供展示）。 */
  listWorkspaces(): Promise<DarvinListWorkspacesResponse>;
  /** 新建 workspace（rootPath 为工作目录绝对路径；缺省由 main 计算默认目录）。 */
  createWorkspace(req?: { name?: string; rootPath?: string }): Promise<DarvinCreateWorkspaceResponse>;
  /** 读取 active workspace id（无则为 null）。 */
  getActiveWorkspace(): Promise<DarvinActiveWorkspaceResponse>;
  /** 把 workspace 设为 active，并同步 active session 到该工作区最近会话。 */
  setActiveWorkspace(workspaceId: string): Promise<DarvinSetActiveWorkspaceResponse>;
  /** 删除 workspace。opts.force=true 时连同其下所有会话级联删除。 */
  deleteWorkspace(workspaceId: string, opts?: { force?: boolean }): Promise<DarvinDeleteWorkspaceResponse>;
  /** 重命名 workspace。name 不能为空也不能与其它 workspace 重名。 */
  renameWorkspace(req: { workspaceId: string; name: string }): Promise<DarvinCreateWorkspaceResponse>;
  /** 改 workspace 目录。rootPath 必须绝对且未被其它 workspace 占用。 */
  updateWorkspaceRoot(req: { workspaceId: string; rootPath: string }): Promise<DarvinCreateWorkspaceResponse>;
  /** 把已有 session 绑定到 workspace（存量数据迁移用）。 */
  bindSessionWorkspace(sessionId: string, workspaceId: string): Promise<DarvinBindSessionWorkspaceResponse>;
  /** 订阅 workspace 列表变更（创建 / 删除 / 改名后 main 广播）。 */
  onWorkspacesChanged(handler: (workspaces: DarvinWorkspace[]) => void): () => void;

  /** 列出当前已知 skill（bundled + user，enabled 状态来自 main 端 SQLite）。 */
  listSkills(): Promise<DarvinListSkillsResponse>;
  /** 切换 skill 启用状态。main 写 SQLite 后通过 agent.skills.set_enabled 同步到 Go。 */
  setSkillEnabled(req: DarvinSetSkillEnabledRequest): Promise<DarvinSetSkillEnabledResponse>;
  /** 订阅 skills 列表变更（bootstrap 完成、fs watcher 触发、Go 端 emit changed）。 */
  onSkillsChanged(handler: (skills: DarvinSkillSummary[]) => void): () => void;

  /** 装一个 skill（本地 SKILL.md 或 GitHub URL）。main 端做安全扫描后
   *  返回 riskLevel；medium 时 renderer 弹安全报告 modal 让用户确认。v0 main 端
   *  该方法为占位（另一个 spec 才真接 scanner），本方法目前由 main stub 返回
   *  「规划中」语义——renderer 按 riskLevel='safe' 直接装即可。 */
  installSkill(req: DarvinInstallSkillRequest): Promise<DarvinInstallSkillResponse>;
  /** 卸载一个 user skill。bundled skill 由 main 端拒绝。 */
  uninstallSkill(req: DarvinUninstallSkillRequest): Promise<DarvinUninstallSkillResponse>;
  /** 升级一个 user skill 到新版本（GitHub 源；v0 stub）。 */
  upgradeSkill(req: DarvinUpgradeSkillRequest): Promise<DarvinUpgradeSkillResponse>;
  /** 拉取 skill 详情（SKILL.md body + 同目录脚本），用于详情 modal。 */
  getSkillDetails(req: DarvinGetSkillDetailsRequest): Promise<DarvinGetSkillDetailsResponse>;

  /** 列所有 MCP server（含 bundled filesystem + user 配置）。 */
  listMcpServers(): Promise<DarvinListMcpServersResponse>;
  /** 新增一个 user MCP server；main 写 SQLite + 调 Go 端 register。 */
  createMcpServer(req: DarvinMcpServerCreate): Promise<DarvinCreateMcpServerResponse>;
  /** 改任意字段（不含 launchStatus / connectionStatus，这两个由 Go 端推）。 */
  updateMcpServer(req: { id: string; patch: DarvinMcpServerPatch }): Promise<DarvinUpdateMcpServerResponse>;
  /** 删除一个 MCP server；cascade 清 launch resolution。 */
  deleteMcpServer(req: { id: string }): Promise<DarvinDeleteMcpServerResponse>;
  /** 切换启用 / 禁用；Go 端关 client / 重连。 */
  setMcpServerEnabled(req: DarvinSetMcpServerEnabledRequest): Promise<DarvinSetMcpServerEnabledResponse>;
  /** 测试当前连接；返回 {ok, error?, tools?}。 */
  testMcpConnection(req: DarvinTestMcpConnectionRequest): Promise<DarvinTestMcpConnectionResponse>;
  /** 重新跑 resolver（适用 npx install 失败后人工点重试）。 */
  retryMcpLaunchResolution(req: DarvinRetryMcpLaunchResolutionRequest): Promise<DarvinRetryMcpLaunchResolutionResponse>;
  /** 列 server 的资源（resources/list 缓存，连接后异步拉取）。 */
  listMcpResources(id: string): Promise<DarvinMcpResourcesListResponse>;
  /** 读单个资源内容。 */
  readMcpResource(id: string, uri: string): Promise<DarvinMcpResourceReadResponse>;
  /** 列 server 的提示模板（prompts/list 缓存）。 */
  listMcpPrompts(id: string): Promise<DarvinMcpPromptsListResponse>;
  /** 渲染一个提示模板。 */
  getMcpPrompt(id: string, name: string, args?: Record<string, unknown>): Promise<DarvinMcpPromptGetResponse>;
  /** 读 server 最近运行时日志（stdio stderr 环形缓冲）。 */
  getMcpLogs(id: string): Promise<DarvinMcpLogsResponse>;
  /** 订阅 server 列表变更（create/update/delete/setEnabled 之后 main 推）。 */
  onMcpServersChanged(handler: (servers: DarvinMcpServer[]) => void): () => void;
  /** 订阅单 server 连接状态变更（Go → main → renderer push）。 */
  onMcpConnectionChanged(handler: (e: DarvinMcpConnectionChangedEvent) => void): () => void;
  /** 订阅 resolver 输出（npx 装包进度 / 失败原因），main 端落 SQLite。 */
  onMcpResolutionChanged(handler: (e: DarvinMcpResolutionChangedEvent) => void): () => void;

  /** 列合并后的工具面（内置 + skill + mcp），renderer / 调试用。 */
  listTools(): Promise<DarvinListToolsResponse>;

  /** 拉取某主 session 派生出的 sub-agent runs（Subagents artifact tab）。 */
  subagentList(parentSessionId: string): Promise<DarvinSubagentListResponse>;
  /** 拉取某个 sub-agent run 的会话消息（按 run id 分桶）。 */
  subagentGetMessages(runId: string): Promise<DarvinSubagentGetMessagesResponse>;
  /** 取消一个 running 的 sub-agent run。 */
  subagentAbort(runId: string): Promise<{ ok: boolean }>;
  /** 字节偏移分页读 sub-agent 结果。 */
  subagentReadResult(runId: string, offsetBytes: number, limitBytes: number): Promise<DarvinSubagentReadResultResponse>;
}