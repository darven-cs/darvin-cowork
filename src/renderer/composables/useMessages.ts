/**
 * useMessages — renderer 端消息状态按 session 分桶。
 *
 * 数据所有权归 main：本 composable 只持有 in-memory 缓存。
 * - `messagesBySessionId` 每个 session 一条独立数组，事件按 ev.sessionId
 *   分桶写入，不混。
 * - `streamingSessionIds` 来自 `text_delta` / `thinking_delta` 事件 + 由
 *   `done` / `error` / `agent_end` 清除；sidebar 用它显示 "running" 状态。
 * - `unreadSessionIds`：session 不在 active 时收到了非 agent_end 事件 →
 *   标记 unread；切回时清掉。
 *
 * 视图层用 `currentMessages`（基于 activeSessionId 派生）只画当前 session
 * 的气泡；切 session 由 useSession 推 activeSessionId 过来，再 watch
 * 它拉一次 getMessages 历史 + 清 unread。
 */

import { computed, ref, watch } from 'vue';
import type { DarvinEvent, DarvinMessage } from '../../shared/darvin-api';
import { useSession } from './useSession';

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

const messagesBySessionId = ref<Record<string, Message[]>>({});
const streamingSessionIds = ref<Set<string>>(new Set());
const unreadSessionIds = ref<Set<string>>(new Set());

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

function appendToBucket(list: Message[], ev: DarvinEvent): void {
  if (ev.type === 'text_delta' || ev.type === 'thinking_delta') {
    const msg = list.find((m) => m.id === ev.messageId);
    if (msg) msg.content += ev.delta;
  } else if (ev.type === 'done') {
    const msg = list.find((m) => m.id === ev.messageId);
    if (msg) msg.done = true;
  } else if (ev.type === 'error') {
    const msg = list.find((m) => m.id === ev.messageId);
    if (msg) {
      msg.done = true;
      msg.error = ev.message;
    }
  }
}

export function useMessages() {
  const session = useSession();

  const currentMessages = computed<Message[]>(
    () => messagesBySessionId.value[session.activeSessionId.value ?? ''] ?? [],
  );

  /**
   * 从 main 拉指定 session 的历史消息。调用前应清空该 bucket，否则新
   * 事件与历史消息会叠在一起。
   */
  async function loadMessages(sessionId: string): Promise<void> {
    if (typeof window === 'undefined' || !window.darvin) return;
    try {
      const r = await window.darvin.getMessages(sessionId);
      messagesBySessionId.value = {
        ...messagesBySessionId.value,
        [sessionId]: r.messages.map(toMessage),
      };
    } catch {
      messagesBySessionId.value = {
        ...messagesBySessionId.value,
        [sessionId]: [],
      };
    }
  }

  function appendUserMessage(sessionId: string, content: string, id?: string): string {
    const mid = id ?? `m-${Math.random().toString(36).slice(2, 10)}`;
    const bucket = messagesBySessionId.value[sessionId] ?? [];
    bucket.push({
      id: mid, sessionId, role: 'user', content, done: true, createdAt: Date.now(),
    });
    messagesBySessionId.value = { ...messagesBySessionId.value, [sessionId]: bucket };
    return mid;
  }

  function startAssistantMessage(sessionId: string, id?: string): string {
    const mid = id ?? `m-${Math.random().toString(36).slice(2, 10)}`;
    const bucket = messagesBySessionId.value[sessionId] ?? [];
    bucket.push({
      id: mid, sessionId, role: 'assistant', content: '', done: false, createdAt: Date.now(),
    });
    messagesBySessionId.value = { ...messagesBySessionId.value, [sessionId]: bucket };
    return mid;
  }

  /**
   * 推一条 backend event 进正确的 session bucket，副作用是更新
   * streamingSessionIds / unreadSessionIds。event.sessionId 必填：
   * main 已经注入到 payload 上。
   */
  function appendEvent(ev: DarvinEvent): void {
    const sid = ev.sessionId;
    if (!sid) {
      // 老 backend / 缺字段：当作 active session 处理，保持向后兼容
      const active = session.activeSessionId.value;
      if (active === null) return;
      appendEventFor(active, ev);
      return;
    }
    appendEventFor(sid, ev);
  }

  function appendEventFor(sid: string, ev: DarvinEvent): void {
    const bucket = messagesBySessionId.value[sid] ?? [];
    appendToBucket(bucket, ev);
    messagesBySessionId.value = { ...messagesBySessionId.value, [sid]: bucket };

    const active = session.activeSessionId.value;
    const isCurrent = sid === active;

    if (ev.type === 'text_delta' || ev.type === 'thinking_delta') {
      streamingSessionIds.value = new Set([...streamingSessionIds.value, sid]);
    } else if (ev.type === 'done' || ev.type === 'error' || ev.type === 'agent_end') {
      const next = new Set(streamingSessionIds.value);
      next.delete(sid);
      streamingSessionIds.value = next;
    }

    // 后台 session 的非 lifecycle 事件 → unread 红点。agent_end 自身不
    // 触发 unread，避免 sidebar 在 stream 收尾时闪一下。
    if (!isCurrent && ev.type !== 'agent_end') {
      unreadSessionIds.value = new Set([...unreadSessionIds.value, sid]);
    }
  }

  // 切到新 active session 时清 unread —— sidebar 不再显示红点，主区开始
  // 显示该 session 的内容。
  watch(
    () => session.activeSessionId.value,
    (newId, oldId) => {
      if (newId !== null && unreadSessionIds.value.has(newId)) {
        const next = new Set(unreadSessionIds.value);
        next.delete(newId);
        unreadSessionIds.value = next;
      }
      if (newId !== null && newId !== oldId) {
        void loadMessages(newId);
      } else if (newId === null) {
        // active 切空（最后一个 session 被删）：保留所有 bucket 以备 undo
      }
    },
    { immediate: true },
  );

  function reset(): void {
    messagesBySessionId.value = {};
    streamingSessionIds.value = new Set();
    unreadSessionIds.value = new Set();
  }

  return {
    messagesBySessionId,
    streamingSessionIds,
    unreadSessionIds,
    currentMessages,
    loadMessages,
    appendUserMessage,
    startAssistantMessage,
    appendEvent,
    reset,
  };
}