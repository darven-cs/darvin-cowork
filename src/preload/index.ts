/**
 * preload：contextBridge 把 DarvinApi 桥到 renderer。
 *
 * 实现全部走 ipcRenderer —— 主进程持有 SQLite (SessionStore) + WS
 * 客户端 (AgentClient)，preload 只负责转发，不碰协议细节。
 *
 * renderer 拿到的 sessions / activeSessionId / streaming event 全部
 * 由 main 单向 push；renderer 不直接接触任何持久化状态。
 */

import { contextBridge, ipcRenderer } from 'electron';
import type {
  DarvinAbortResponse,
  DarvinApi,
  DarvinCompactContextResponse,
  DarvinCreateArtifactPreviewSessionResponse,
  DarvinCreateSessionResponse,
  DarvinDeleteSessionResponse,
  DarvinDestroyArtifactPreviewSessionResponse,
  DarvinSwitchSessionResponse,
  DarvinActiveSessionResponse,
  DarvinEvent,
  DarvinGetMessagesResponse,
  DarvinGetSessionUsageResponse,
  DarvinImportedFile,
  DarvinImportFilesResponse,
  DarvinInvokeSkillRequest,
  DarvinInvokeSkillResponse,
  DarvinListImportedFilesResponse,
  DarvinListSessionsResponse,
  DarvinListSkillsResponse,
  DarvinListToolsResponse,
  DarvinListWorkspaceFilesResponse,
  DarvinOpenWorkspaceFileResponse,
  DarvinLLMConfig,
  DarvinLocalServiceInfo,
  DarvinLocaleResponse,
  DarvinMcpConnectionChangedEvent,
  DarvinMcpServer,
  DarvinMcpServerCreate,
  DarvinMcpServerPatch,
  DarvinModelInfo,
  DarvinPermissionResponse,
  DarvinPickAttachmentsResponse,
  DarvinPickSkillFolderResponse,
  DarvinPromptRequest,
  DarvinPromptResponse,
  DarvinReadFileDataUrlResponse,
  DarvinReadWorkspaceFileResponse,
  DarvinRemoveImportedFileResponse,
  DarvinRenameSessionResponse,
  DarvinRuntimeStatus,
  DarvinSearchSessionsResponse,
  DarvinSetLLMConfigResponse,
  DarvinSetSkillEnabledRequest,
  DarvinSetSkillEnabledResponse,
  DarvinSetWorkspaceResult,
  DarvinSession,
  DarvinSkillSummary,
  DarvinSubagentGetMessagesResponse,
  DarvinSubagentListResponse,
  DarvinSubagentReadResultResponse,
  DarvinMcpResourcesListResponse,
  DarvinMcpResourceReadResponse,
  DarvinMcpPromptsListResponse,
  DarvinMcpPromptGetResponse,
  DarvinMcpLogsResponse,
  DarvinTestMcpConnectionRequest,
  DarvinTestMcpConnectionResponse,
  DarvinWorkspaceInfoResponse,
  DarvinWorkspaceRootResult,
  DarvinWorkspace,
  DarvinListWorkspacesResponse,
  DarvinCreateWorkspaceResponse,
  DarvinActiveWorkspaceResponse,
  DarvinSetActiveWorkspaceResponse,
  DarvinDeleteWorkspaceResponse,
  DarvinBindSessionWorkspaceResponse,
  DarvinAgent,
} from '../shared/darvin-api';
import { DarvinPushEvent } from '../shared/darvin-api';

const api: DarvinApi = {
  async createSession(req?: { title?: string; workspaceId?: string; systemPrompt?: string; identity?: string; agentId?: string }): Promise<DarvinCreateSessionResponse> {
    return ipcRenderer.invoke('darvin:create_session', req);
  },
  async listSessions(workspaceId?: string): Promise<DarvinListSessionsResponse> {
    return ipcRenderer.invoke('darvin:list_sessions', workspaceId);
  },
  async switchSession(sessionId: string): Promise<DarvinSwitchSessionResponse> {
    return ipcRenderer.invoke('darvin:switch_session', sessionId);
  },
  async deleteSession(sessionId: string): Promise<DarvinDeleteSessionResponse> {
    return ipcRenderer.invoke('darvin:delete_session', sessionId);
  },
  async renameSession(sessionId: string, title: string): Promise<DarvinRenameSessionResponse> {
    return ipcRenderer.invoke('darvin:rename_session', sessionId, title);
  },
  async getActiveSession(): Promise<DarvinActiveSessionResponse> {
    return ipcRenderer.invoke('darvin:get_active_session');
  },
  async searchSessions(query: string): Promise<DarvinSearchSessionsResponse> {
    return ipcRenderer.invoke('darvin:search_sessions', query);
  },
  onSessionsChanged(handler: (sessions: DarvinSession[]) => void): () => void {
    const wrap = (_e: unknown, sessions: DarvinSession[]) => handler(sessions);
    ipcRenderer.on(DarvinPushEvent.SessionsChanged, wrap);
    return () => {
      ipcRenderer.off(DarvinPushEvent.SessionsChanged, wrap);
    };
  },
  onActiveSessionChanged(handler: (sessionId: string | null) => void): () => void {
    const wrap = (_e: unknown, sessionId: string | null) => handler(sessionId);
    ipcRenderer.on(DarvinPushEvent.ActiveSessionChanged, wrap);
    return () => {
      ipcRenderer.off(DarvinPushEvent.ActiveSessionChanged, wrap);
    };
  },

  async getMessages(sessionId: string): Promise<DarvinGetMessagesResponse> {
    return ipcRenderer.invoke('darvin:get_messages', sessionId);
  },

  async getSessionUsage(sessionId: string): Promise<DarvinGetSessionUsageResponse> {
    return ipcRenderer.invoke('darvin:get_session_usage', sessionId);
  },

  async prompt(req: DarvinPromptRequest): Promise<DarvinPromptResponse> {
    return ipcRenderer.invoke('darvin:prompt', req);
  },
  async invokeSkill(req: DarvinInvokeSkillRequest): Promise<DarvinInvokeSkillResponse> {
    return ipcRenderer.invoke('darvin:invoke_skill', req);
  },
  async abort(): Promise<DarvinAbortResponse> {
    return ipcRenderer.invoke('darvin:abort');
  },
  async compactContext(sessionId: string): Promise<DarvinCompactContextResponse> {
    return ipcRenderer.invoke('darvin:compact_context', sessionId);
  },

  onEvent(handler: (e: DarvinEvent) => void): () => void {
    const wrap = (_e: unknown, ev: DarvinEvent) => handler(ev);
    ipcRenderer.on(DarvinPushEvent.SessionEvent, wrap);
    return () => {
      ipcRenderer.off(DarvinPushEvent.SessionEvent, wrap);
    };
  },

  async getLLMConfig(): Promise<DarvinLLMConfig> {
    return ipcRenderer.invoke('darvin:get_llm_config');
  },
  async getLLMModels(): Promise<DarvinModelInfo[]> {
    return ipcRenderer.invoke('darvin:get_llm_models');
  },
  async setLLMConfig(req): Promise<DarvinSetLLMConfigResponse> {
    return ipcRenderer.invoke('darvin:set_llm_config', req);
  },

  async getAppPreferences() {
    return ipcRenderer.invoke('darvin:get_app_preferences');
  },
  async setAppPreferences(patch) {
    return ipcRenderer.invoke('darvin:set_app_preferences', patch);
  },
  async getAppInfo() {
    return ipcRenderer.invoke('darvin:get_app_info');
  },

  async getLocale(): Promise<DarvinLocaleResponse> {
    return ipcRenderer.invoke('darvin:get_locale');
  },
  async setLocale(req: { locale: 'zh' | 'en' }): Promise<void> {
    return ipcRenderer.invoke('darvin:set_locale', req);
  },

  async status(): Promise<DarvinRuntimeStatus> {
    return ipcRenderer.invoke('darvin:status');
  },

  async importFiles(): Promise<DarvinImportFilesResponse> {
    return ipcRenderer.invoke('darvin:import_files');
  },
  async listImportedFiles(): Promise<DarvinListImportedFilesResponse> {
    return ipcRenderer.invoke('darvin:list_imported_files');
  },
  async removeImportedFile(relativePath: string): Promise<DarvinRemoveImportedFileResponse> {
    return ipcRenderer.invoke('darvin:remove_imported_file', relativePath);
  },
  async getWorkspaceInfo(): Promise<DarvinWorkspaceInfoResponse> {
    return ipcRenderer.invoke('darvin:get_workspace_info');
  },
  async revealWorkspace(): Promise<void> {
    return ipcRenderer.invoke('darvin:reveal_workspace');
  },
  async listWorkspaceFiles(): Promise<DarvinListWorkspaceFilesResponse> {
    return ipcRenderer.invoke('darvin:list_workspace_files');
  },
  async readWorkspaceFile(relativePath: string): Promise<DarvinReadWorkspaceFileResponse> {
    return ipcRenderer.invoke('darvin:read_workspace_file', relativePath);
  },
  async revealWorkspaceFile(relativePath: string): Promise<void> {
    return ipcRenderer.invoke('darvin:reveal_workspace_file', relativePath);
  },
  async openWorkspaceFile(relativePath: string): Promise<DarvinOpenWorkspaceFileResponse> {
    return ipcRenderer.invoke('darvin:open_workspace_file', relativePath);
  },
  async openExternal(url: string): Promise<{ success: boolean }> {
    return ipcRenderer.invoke('darvin:open_external', url);
  },
  async listLocalServices(urls: string[]): Promise<{ services: DarvinLocalServiceInfo[] }> {
    return ipcRenderer.invoke('local_services:list', urls);
  },
  onWorkspaceChanged(handler: (info: { sessionId: string; files: DarvinImportedFile[] }) => void): () => void {
    const wrap = (_e: unknown, info: { sessionId: string; files: DarvinImportedFile[] }) => handler(info);
    ipcRenderer.on(DarvinPushEvent.WorkspaceChanged, wrap);
    return () => {
      ipcRenderer.off(DarvinPushEvent.WorkspaceChanged, wrap);
    };
  },

  async createArtifactPreviewSession(relativePath: string): Promise<DarvinCreateArtifactPreviewSessionResponse> {
    return ipcRenderer.invoke('darvin:artifact:create_preview_session', relativePath);
  },
  async destroyArtifactPreviewSession(sessionId: string): Promise<DarvinDestroyArtifactPreviewSessionResponse> {
    return ipcRenderer.invoke('darvin:artifact:destroy_preview_session', sessionId);
  },

  async respondPermission(r: DarvinPermissionResponse): Promise<void> {
    return ipcRenderer.invoke('darvin:permission_response', r);
  },
  async pickAttachments(): Promise<DarvinPickAttachmentsResponse> {
    return ipcRenderer.invoke('darvin:pick_attachments');
  },
  async pickSkillFolder(): Promise<DarvinPickSkillFolderResponse> {
    return ipcRenderer.invoke('darvin:pick_skill_folder');
  },
  async readFileAsDataUrl(filePath: string): Promise<DarvinReadFileDataUrlResponse> {
    return ipcRenderer.invoke('darvin:read_file_data_url', filePath);
  },
  async setWorkspaceRoot(): Promise<DarvinSetWorkspaceResult> {
    return ipcRenderer.invoke('darvin:set_workspace_root');
  },
  async setWorkspaceRootTo(path: string): Promise<DarvinSetWorkspaceResult> {
    return ipcRenderer.invoke('darvin:set_workspace_root_to', path);
  },
  async getWorkspaceRoot(): Promise<DarvinWorkspaceRootResult> {
    return ipcRenderer.invoke('darvin:get_workspace_root');
  },
  async listWorkspaces(): Promise<DarvinListWorkspacesResponse> {
    return ipcRenderer.invoke('darvin:list_workspaces');
  },
  async createWorkspace(req?: { name?: string; rootPath?: string }): Promise<DarvinCreateWorkspaceResponse> {
    return ipcRenderer.invoke('darvin:create_workspace', req);
  },
  async getActiveWorkspace(): Promise<DarvinActiveWorkspaceResponse> {
    return ipcRenderer.invoke('darvin:get_active_workspace');
  },
  async setActiveWorkspace(workspaceId: string): Promise<DarvinSetActiveWorkspaceResponse> {
    return ipcRenderer.invoke('darvin:set_active_workspace', workspaceId);
  },
  async deleteWorkspace(workspaceId: string, opts?: { force?: boolean }): Promise<DarvinDeleteWorkspaceResponse> {
    return ipcRenderer.invoke('darvin:delete_workspace', { workspaceId, force: opts?.force === true });
  },
  async renameWorkspace(req: { workspaceId: string; name: string }): Promise<DarvinCreateWorkspaceResponse> {
    return ipcRenderer.invoke('darvin:rename_workspace', req);
  },
  async updateWorkspaceRoot(req: { workspaceId: string; rootPath: string }): Promise<DarvinCreateWorkspaceResponse> {
    return ipcRenderer.invoke('darvin:update_workspace_root', req);
  },
  async bindSessionWorkspace(sessionId: string, workspaceId: string): Promise<DarvinBindSessionWorkspaceResponse> {
    return ipcRenderer.invoke('darvin:bind_session_workspace', { sessionId, workspaceId });
  },
  onWorkspacesChanged(handler: (workspaces: DarvinWorkspace[]) => void): () => void {
    const wrap = (_e: unknown, workspaces: DarvinWorkspace[]) => handler(workspaces);
    ipcRenderer.on(DarvinPushEvent.WorkspacesChanged, wrap);
    return () => {
      ipcRenderer.off(DarvinPushEvent.WorkspacesChanged, wrap);
    };
  },

  async listSkills(): Promise<DarvinListSkillsResponse> {
    return ipcRenderer.invoke('darvin:list_skills');
  },
  async setSkillEnabled(req: DarvinSetSkillEnabledRequest): Promise<DarvinSetSkillEnabledResponse> {
    return ipcRenderer.invoke('darvin:set_skill_enabled', req);
  },
  onSkillsChanged(handler: (skills: DarvinSkillSummary[]) => void): () => void {
    const wrap = (_e: unknown, skills: DarvinSkillSummary[]) => handler(skills);
    ipcRenderer.on(DarvinPushEvent.SkillsChanged, wrap);
    return () => {
      ipcRenderer.off(DarvinPushEvent.SkillsChanged, wrap);
    };
  },

  // install / uninstall / upgrade / getDetails 的 main 端目前是占位，
  // renderer 走 IPC 路径已经跑通。
  async installSkill(req): Promise<{ skill: DarvinSkillSummary; riskLevel: 'safe' | 'low' | 'medium' | 'high' | 'critical' }> {
    return ipcRenderer.invoke('darvin:install_skill', req);
  },
  async uninstallSkill(req: { skillId: string }): Promise<{ ok: boolean }> {
    return ipcRenderer.invoke('darvin:uninstall_skill', req);
  },
  async upgradeSkill(req: { skillId: string }): Promise<{ skill: DarvinSkillSummary }> {
    return ipcRenderer.invoke('darvin:upgrade_skill', req);
  },
  async getSkillDetails(req: { skillId: string }): Promise<{ skill: DarvinSkillSummary; body: string; scripts?: Array<{ path: string; content: string }> }> {
    return ipcRenderer.invoke('darvin:get_skill_details', req);
  },

  // MCP server 命名空间。renderer 不直接持有 server 状态,
  // 走 IPC 调 main 端 mcpManager,后者是 SQLite + Go RPC 的中转。
  async listMcpServers(): Promise<{ servers: DarvinMcpServer[] }> {
    return ipcRenderer.invoke('mcp:list');
  },
  async createMcpServer(req: DarvinMcpServerCreate): Promise<{ server: DarvinMcpServer }> {
    return ipcRenderer.invoke('mcp:create', req);
  },
  async updateMcpServer(req: { id: string; patch: DarvinMcpServerPatch }): Promise<{ server: DarvinMcpServer }> {
    return ipcRenderer.invoke('mcp:update', req);
  },
  async deleteMcpServer(req: { id: string }): Promise<{ ok: boolean }> {
    return ipcRenderer.invoke('mcp:delete', req);
  },
  async setMcpServerEnabled(req: { id: string; enabled: boolean }): Promise<{ ok: boolean }> {
    return ipcRenderer.invoke('mcp:set_enabled', req);
  },
  async testMcpConnection(req: DarvinTestMcpConnectionRequest): Promise<DarvinTestMcpConnectionResponse> {
    return ipcRenderer.invoke('mcp:test', req);
  },
  async retryMcpLaunchResolution(req: { id: string }): Promise<{ ok: boolean }> {
    return ipcRenderer.invoke('mcp:retry_resolution', req);
  },
  async listMcpResources(id: string): Promise<DarvinMcpResourcesListResponse> {
    return ipcRenderer.invoke('mcp:resources_list', { id });
  },
  async readMcpResource(id: string, uri: string): Promise<DarvinMcpResourceReadResponse> {
    return ipcRenderer.invoke('mcp:resource_read', { id, uri });
  },
  async listMcpPrompts(id: string): Promise<DarvinMcpPromptsListResponse> {
    return ipcRenderer.invoke('mcp:prompts_list', { id });
  },
  async getMcpPrompt(id: string, name: string, args?: Record<string, unknown>): Promise<DarvinMcpPromptGetResponse> {
    return ipcRenderer.invoke('mcp:prompt_get', { id, name, arguments: args });
  },
  async getMcpLogs(id: string): Promise<DarvinMcpLogsResponse> {
    return ipcRenderer.invoke('mcp:logs_get', { id });
  },
  onMcpServersChanged(handler: (servers: DarvinMcpServer[]) => void): () => void {
    const wrap = (_e: unknown, servers: DarvinMcpServer[]) => handler(servers);
    ipcRenderer.on(DarvinPushEvent.McpServersChanged, wrap);
    return () => {
      ipcRenderer.off(DarvinPushEvent.McpServersChanged, wrap);
    };
  },
  onMcpConnectionChanged(handler: (e: DarvinMcpConnectionChangedEvent) => void): () => void {
    const wrap = (_e: unknown, ev: DarvinMcpConnectionChangedEvent) => handler(ev);
    ipcRenderer.on(DarvinPushEvent.McpConnectionChanged, wrap);
    return () => {
      ipcRenderer.off(DarvinPushEvent.McpConnectionChanged, wrap);
    };
  },

  // 工具面合并视图（内置 + skill + mcp），直连 Go RPC。
  async listTools(): Promise<DarvinListToolsResponse> {
    return ipcRenderer.invoke('tools:list');
  },

  // Subagents artifact tab 数据源。
  subagentList(parentSessionId: string): Promise<DarvinSubagentListResponse> {
    return ipcRenderer.invoke('subagent:list', parentSessionId);
  },
  subagentGetMessages(runId: string): Promise<DarvinSubagentGetMessagesResponse> {
    return ipcRenderer.invoke('subagent:get_messages', runId);
  },
  subagentAbort(runId: string): Promise<{ ok: boolean }> {
    return ipcRenderer.invoke('subagent:abort', runId);
  },
  subagentReadResult(runId: string, offsetBytes: number, limitBytes: number): Promise<DarvinSubagentReadResultResponse> {
    return ipcRenderer.invoke('subagent:read_result', runId, offsetBytes, limitBytes);
  },

  async listAgents(workspaceId: string): Promise<{ agents: DarvinAgent[] }> {
    return ipcRenderer.invoke('agents:list', workspaceId);
  },
  async getAgent(agentId: string): Promise<{ agent: DarvinAgent }> {
    return ipcRenderer.invoke('agents:get', agentId);
  },
  async createAgent(req: {
    workspaceId: string;
    name: string;
    description?: string;
    nameEn?: string;
    descriptionEn?: string;
    identity?: string;
    identityEn?: string;
    systemPrompt?: string;
    systemPromptEn?: string;
    icon?: string;
    color?: string;
    skillIds?: string[];
    fromPresetId?: string;
  }): Promise<{ agent: DarvinAgent }> {
    return ipcRenderer.invoke('agents:create', req);
  },
  async updateAgent(agentId: string, patch: Partial<DarvinAgent>): Promise<{ agent: DarvinAgent }> {
    return ipcRenderer.invoke('agents:update', { agentId, patch });
  },
  async deleteAgent(agentId: string): Promise<{ deleted: boolean }> {
    return ipcRenderer.invoke('agents:delete', agentId);
  },
  async updateDefaultAgent(req: { workspaceId: string; defaultAgentId: string }): Promise<{ workspace: DarvinWorkspace }> {
    return ipcRenderer.invoke('agents:update_default', req);
  },
  onAgentsChanged(handler: (agents: DarvinAgent[]) => void): () => void {
    const listener = (_e: unknown, agents: DarvinAgent[]): void => handler(agents);
    ipcRenderer.on(DarvinPushEvent.AgentsChanged, listener);
    return () => ipcRenderer.off(DarvinPushEvent.AgentsChanged, listener);
  },
  scheduleList(req: { workspaceId: string }) {
    return ipcRenderer.invoke('schedule:list', req);
  },
  scheduleGet(req: { workspaceId: string; scheduleId: string }) {
    return ipcRenderer.invoke('schedule:get', req);
  },
  scheduleCreate(req: Parameters<DarvinApi['scheduleCreate']>[0]) {
    return ipcRenderer.invoke('schedule:create', req);
  },
  scheduleUpdate(req: Parameters<DarvinApi['scheduleUpdate']>[0]) {
    return ipcRenderer.invoke('schedule:update', req);
  },
  scheduleDelete(req: { workspaceId: string; scheduleId: string }) {
    return ipcRenderer.invoke('schedule:delete', req);
  },
  scheduleToggle(req: Parameters<DarvinApi['scheduleToggle']>[0]) {
    return ipcRenderer.invoke('schedule:toggle', req);
  },
  scheduleRunNow(req: { workspaceId: string; scheduleId: string }) {
    return ipcRenderer.invoke('schedule:run_now', req);
  },
  scheduleAbort(req: Parameters<DarvinApi['scheduleAbort']>[0]) {
    return ipcRenderer.invoke('schedule:abort', req);
  },
  scheduleListRuns(req: Parameters<DarvinApi['scheduleListRuns']>[0]) {
    return ipcRenderer.invoke('schedule:list_runs', req);
  },
  scheduleListAllRuns(req: Parameters<DarvinApi['scheduleListAllRuns']>[0]) {
    return ipcRenderer.invoke('schedule:list_all_runs', req);
  },
  onSchedulesChanged(handler: (payload: { workspaceId: string }) => void): () => void {
    const listener = (_e: unknown, payload: { workspaceId: string }): void => handler(payload);
    ipcRenderer.on(DarvinPushEvent.SchedulesChanged, listener);
    return () => ipcRenderer.off(DarvinPushEvent.SchedulesChanged, listener);
  },
  onScheduleRunsChanged(handler: (payload: { scheduleId: string; runId: string }) => void): () => void {
    const listener = (_e: unknown, payload: { scheduleId: string; runId: string }): void => handler(payload);
    ipcRenderer.on(DarvinPushEvent.ScheduleRunsChanged, listener);
    return () => ipcRenderer.off(DarvinPushEvent.ScheduleRunsChanged, listener);
  },
  onScheduleFired(handler: (payload: { scheduleId: string; runId: string; triggeredAt: number }) => void): () => void {
    const listener = (_e: unknown, payload: { scheduleId: string; runId: string; triggeredAt: number }): void => handler(payload);
    ipcRenderer.on(DarvinPushEvent.ScheduleFired, listener);
    return () => ipcRenderer.off(DarvinPushEvent.ScheduleFired, listener);
  },
  imList(req: { workspaceId?: string }) {
    return ipcRenderer.invoke('im:list', req);
  },
  imGet(req: { instanceId: string }) {
    return ipcRenderer.invoke('im:get', req);
  },
  imCreate(req: Parameters<DarvinApi['imCreate']>[0]) {
    return ipcRenderer.invoke('im:create', req);
  },
  imUpdate(req: Parameters<DarvinApi['imUpdate']>[0]) {
    return ipcRenderer.invoke('im:update', req);
  },
  imDelete(req: { instanceId: string }) {
    return ipcRenderer.invoke('im:delete', req);
  },
  imSetEnabled(req: Parameters<DarvinApi['imSetEnabled']>[0]) {
    return ipcRenderer.invoke('im:set_enabled', req);
  },
  imTest(req: Parameters<DarvinApi['imTest']>[0]) {
    return ipcRenderer.invoke('im:test', req);
  },
  imLoginStart(req: Parameters<DarvinApi['imLoginStart']>[0]) {
    return ipcRenderer.invoke('im:login_start', req);
  },
  imLoginPoll(req: Parameters<DarvinApi['imLoginPoll']>[0]) {
    return ipcRenderer.invoke('im:login_poll', req);
  },
  onImChanged(handler: () => void): () => void {
    const listener = (): void => handler();
    ipcRenderer.on(DarvinPushEvent.ImChanged, listener);
    return () => ipcRenderer.off(DarvinPushEvent.ImChanged, listener);
  },
  onImStatusChanged(handler: () => void): () => void {
    const listener = (): void => handler();
    ipcRenderer.on(DarvinPushEvent.ImStatusChanged, listener);
    return () => ipcRenderer.off(DarvinPushEvent.ImStatusChanged, listener);
  },
};

contextBridge.exposeInMainWorld('darvin', api);