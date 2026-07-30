/**
 * useMessages — renderer 端消息状态薄视图。
 *
 * 数据所有权归 main：`messages` 是当前 session 的 in-memory 缓存，由
 * `loadMessages(sessionId)` 从 main 拉一次，streaming 事件由 main 推
 * 过来 `appendEvent` 增量更新。
 *
 * `appendEvent` 只处理已按 activeSessionId 路由过的事件；不再需要
 * 自行判断 `ev.sessionId === currentSessionId` —— main 已经做完了。
 */

import { ref } from 'vue';
import type { DarvinEvent, DarvinMessage } from '../../shared/darvin-api';

export interface Message {
  id: string;
  sessionId: string;
  role: 'user' | 'assistant';
  content: string;
  done: boolean;
  error?: string;
  toolLabel?: string;
  createdAt: number;
}

const list = ref<Message[]>([]);

function toMessage(m: DarvinMessage): Message {
  return {
    id: m.id,
    sessionId: m.sessionId,
    role: m.role,
    content: m.content,
    done: m.done,
    error: m.error,
    toolLabel: m.toolLabel,
    createdAt: m.createdAt,
  };
}

export function useMessages() {
  /**
   * 从 main 拉指定 session 的历史消息。调用前应清空 `list`，否则新旧
   * session 的消息会叠在一起。
   */
  async function loadMessages(sessionId: string): Promise<void> {
    if (typeof window === 'undefined' || !window.darvin) return;
    try {
      const r = await window.darvin.getMessages(sessionId);
      list.value = r.messages.map(toMessage);
    } catch {
      list.value = [];
    }
  }

  function appendUserMessage(sessionId: string, content: string, id?: string): string {
    const mid = id ?? `m-${Math.random().toString(36).slice(2, 10)}`;
    list.value.push({
      id: mid, sessionId, role: 'user', content, done: true, createdAt: Date.now(),
    });
    return mid;
  }

  function startAssistantMessage(sessionId: string, id?: string): string {
    const mid = id ?? `m-${Math.random().toString(36).slice(2, 10)}`;
    list.value.push({
      id: mid, sessionId, role: 'assistant', content: '', done: false, createdAt: Date.now(),
    });
    return mid;
  }

  function appendEvent(e: DarvinEvent) {
    if (e.type === 'text_delta' || e.type === 'thinking_delta') {
      const msg = list.value.find((m) => m.id === e.messageId);
      if (msg) msg.content += e.delta;
    } else if (e.type === 'done') {
      const msg = list.value.find((m) => m.id === e.messageId);
      if (msg) msg.done = true;
    } else if (e.type === 'error') {
      const msg = list.value.find((m) => m.id === e.messageId);
      if (msg) {
        msg.done = true;
        msg.error = e.message;
      }
    }
  }

  function reset() {
    list.value = [];
  }

  return { list, loadMessages, appendUserMessage, startAssistantMessage, appendEvent, reset };
}