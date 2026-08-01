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
  DarvinListImportedFilesResponse,
  DarvinListSessionsResponse,
  DarvinLLMConfig,
  DarvinLocaleResponse,
  DarvinPromptRequest,
  DarvinPromptResponse,
  DarvinRemoveImportedFileResponse,
  DarvinRenameSessionResponse,
  DarvinRuntimeStatus,
  DarvinSearchSessionsResponse,
  DarvinSetLLMConfigResponse,
  DarvinSession,
  DarvinWorkspaceInfoResponse,
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
};

contextBridge.exposeInMainWorld('darvin', api);