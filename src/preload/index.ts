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
  DarvinLocaleResponse,
  DarvinMcpConnectionChangedEvent,
  DarvinMcpServer,
  DarvinMcpServerCreate,
  DarvinMcpServerPatch,
  DarvinPermissionResponse,
  DarvinPickAttachmentsResponse,
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
  DarvinTestMcpConnectionRequest,
  DarvinTestMcpConnectionResponse,
  DarvinWorkspaceInfoResponse,
  DarvinWorkspaceRootResult,
} from '../shared/darvin-api';
import { DarvinPushEvent } from '../shared/darvin-api';

const api: DarvinApi = {
  async createSession(req?: { title?: string }): Promise<DarvinCreateSessionResponse> {
    return ipcRenderer.invoke('darvin:create_session', req);
  },
  async listSessions(): Promise<DarvinListSessionsResponse> {
    return ipcRenderer.invoke('darvin:list_sessions');
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

  // spec 33 — install / uninstall / upgrade / getDetails 的 main 端目前是占位
  // （另一个 spec 才真接 scanner + 物理 install），renderer 走 IPC 路径已经
  // 跑通，落地后只需把 main 端 handler 实现替换为真接 scanner 即可。
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

  // spec 36 — MCP server 命名空间。renderer 不直接持有 server 状态,
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

  // spec 38 — 工具面合并视图（内置 + skill + mcp），直连 Go RPC。
  async listTools(): Promise<DarvinListToolsResponse> {
    return ipcRenderer.invoke('tools:list');
  },
};

contextBridge.exposeInMainWorld('darvin', api);