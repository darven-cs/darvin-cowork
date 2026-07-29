/**
 * preload：contextBridge 暴露 mock 版的 DarvinApi。
 *
 * 真实 IPC 客户端仅替换 prompt / onEvent / status 三个方法的实现，
 * 签名保持不变（renderer 侧契约 0 改动）。
 */

import { contextBridge } from 'electron';
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
import { mockMessages, mockSessions } from '../renderer/services/mock-data';
import { mockPrompt } from '../renderer/services/mock-agent';

const eventTarget = new EventTarget();
const SUBS = new Set<(e: DarvinEvent) => void>();

async function pump(content: string, sessionId?: string): Promise<{ sessionId: string; messageId: string }> {
  const r = await mockPrompt(content, sessionId);
  // 异步把 events 推到 EventTarget（renderer 侧 onEvent handler 消费）
  (async () => {
    try {
      for await (const ev of r.events) {
        eventTarget.dispatchEvent(new CustomEvent('darvin', { detail: ev }));
      }
    } catch (err) {
      // 静默：mock 流抛错时不再上抛，错误事件由上游 agent_end 携带
      void err;
    }
  })();
  return { sessionId: r.sessionId, messageId: r.messageId };
}

const api: DarvinApi = {
  async prompt(req: DarvinPromptRequest): Promise<DarvinPromptResponse> {
    return pump(req.content, req.sessionId);
  },
  async abort(sessionId: string): Promise<DarvinAbortResponse> {
    return { aborted: true, sessionId };
  },
  async listSessions(): Promise<DarvinListSessionsResponse> {
    return { sessions: mockSessions };
  },
  async getMessages(sessionId: string): Promise<DarvinGetMessagesResponse> {
    return { messages: mockMessages[sessionId] ?? [] };
  },
  onEvent(handler: (e: DarvinEvent) => void): () => void {
    const wrap = (e: Event) => {
      const detail = (e as CustomEvent<DarvinEvent>).detail;
      handler(detail);
    };
    eventTarget.addEventListener('darvin', wrap);
    SUBS.add(handler);
    return () => {
      eventTarget.removeEventListener('darvin', wrap);
      SUBS.delete(handler);
    };
  },
  status(): DarvinRuntimeStatus {
    return 'online';
  },
};

contextBridge.exposeInMainWorld('darvin', api);
