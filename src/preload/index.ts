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
  DarvinCreateSessionResponse,
  DarvinDeleteSessionResponse,
  DarvinSwitchSessionResponse,
  DarvinActiveSessionResponse,
  DarvinEvent,
  DarvinGetMessagesResponse,
  DarvinListSessionsResponse,
  DarvinLLMConfig,
  DarvinLocaleResponse,
  DarvinPromptRequest,
  DarvinPromptResponse,
  DarvinRuntimeStatus,
  DarvinSetLLMConfigResponse,
  DarvinSession,
} from '../shared/darvin-api';
import { DarvinPushEvent } from '../shared/darvin-api';

const api: DarvinApi = {
  // session 管理
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
  async getActiveSession(): Promise<DarvinActiveSessionResponse> {
    return ipcRenderer.invoke('darvin:get_active_session');
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

  // 消息查询
  async getMessages(sessionId: string): Promise<DarvinGetMessagesResponse> {
    return ipcRenderer.invoke('darvin:get_messages', sessionId);
  },

  // prompt / abort：main 知道当前 active session，renderer 不传 sessionId
  async prompt(req: DarvinPromptRequest): Promise<DarvinPromptResponse> {
    return ipcRenderer.invoke('darvin:prompt', req);
  },
  async abort(): Promise<DarvinAbortResponse> {
    return ipcRenderer.invoke('darvin:abort');
  },

  // streaming 事件订阅：main 已按 activeSessionId 路由过
  onEvent(handler: (e: DarvinEvent) => void): () => void {
    const wrap = (_e: unknown, ev: DarvinEvent) => handler(ev);
    ipcRenderer.on(DarvinPushEvent.SessionEvent, wrap);
    return () => {
      ipcRenderer.off(DarvinPushEvent.SessionEvent, wrap);
    };
  },

  // LLM 配置
  async getLLMConfig(): Promise<DarvinLLMConfig> {
    return ipcRenderer.invoke('darvin:get_llm_config');
  },
  async setLLMConfig(req): Promise<DarvinSetLLMConfigResponse> {
    return ipcRenderer.invoke('darvin:set_llm_config', req);
  },

  // locale
  async getLocale(): Promise<DarvinLocaleResponse> {
    return ipcRenderer.invoke('darvin:get_locale');
  },
  async setLocale(req: { locale: 'zh' | 'en' }): Promise<void> {
    return ipcRenderer.invoke('darvin:set_locale', req);
  },

  // 运行时状态
  async status(): Promise<DarvinRuntimeStatus> {
    return ipcRenderer.invoke('darvin:status');
  },
};

contextBridge.exposeInMainWorld('darvin', api);