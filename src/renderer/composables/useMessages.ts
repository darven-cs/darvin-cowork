import { ref } from 'vue';
import type { DarvinEvent } from '../../shared/darvin-api';

export interface Message {
  id: string;
  sessionId: string;
  role: 'user' | 'assistant';
  content: string;
  done: boolean;
  error?: string;
  createdAt: number;
}

const list = ref<Message[]>([]);

function newId(): string {
  return `m-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 6)}`;
}

export function useMessages() {
  function appendUserMessage(sessionId: string, content: string, id?: string): string {
    const mid = id ?? newId();
    list.value.push({
      id: mid, sessionId, role: 'user', content, done: true, createdAt: Date.now(),
    });
    return mid;
  }
  function startAssistantMessage(sessionId: string, id?: string): string {
    const mid = id ?? newId();
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
  return { list, appendUserMessage, startAssistantMessage, appendEvent, reset };
}
