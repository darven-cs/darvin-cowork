/**
 * preload：contextBridge 把 DarvinApi 桥到 renderer。
 *
 * 实现全部走 ipcRenderer —— 主进程持有 WS 客户端，preload 只负责转发，
 * 不碰协议细节。renderer 侧签名与 S1 契约一致，唯一变化是 `status()`
 * 改成异步（sendSync 会阻塞 renderer 线程）。
 */

import { contextBridge, ipcRenderer } from 'electron';
import type {
  DarvinAbortResponse,
  DarvinApi,
  DarvinEvent,
  DarvinGetMessagesResponse,
  DarvinListSessionsResponse,
  DarvinPromptRequest,
  DarvinPromptResponse,
  DarvinRuntimeStatus,
} from '../shared/darvin-api';

const api: DarvinApi = {
  async prompt(req: DarvinPromptRequest): Promise<DarvinPromptResponse> {
    return ipcRenderer.invoke('darvin:prompt', req);
  },
  async abort(sessionId: string): Promise<DarvinAbortResponse> {
    return ipcRenderer.invoke('darvin:abort', { sessionId });
  },
  // listSessions / getMessages 的真 RPC 在 S6（agent.list_sessions /
  // agent.get_messages）落地。这里返空而不是返 mock 数据：假的会话列表
  // 会让人误判持久化已经通了。
  async listSessions(): Promise<DarvinListSessionsResponse> {
    return { sessions: [] };
  },
  async getMessages(): Promise<DarvinGetMessagesResponse> {
    return { messages: [] };
  },
  onEvent(handler: (e: DarvinEvent) => void): () => void {
    const wrap = (_e: unknown, ev: DarvinEvent) => handler(ev);
    ipcRenderer.on('darvin:event', wrap);
    return () => {
      ipcRenderer.off('darvin:event', wrap);
    };
  },
  async status(): Promise<DarvinRuntimeStatus> {
    return ipcRenderer.invoke('darvin:status');
  },
};

contextBridge.exposeInMainWorld('darvin', api);
