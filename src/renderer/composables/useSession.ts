import { ref, watch } from 'vue';
import type { DarvinSession } from '../../shared/darvin-api';
import { mockSessions } from '../services/mock-data';

const KEY = 'darvin.session.current';

function newId(): string {
  return `s-${Math.random().toString(36).slice(2, 10)}`;
}

const sessions = ref<DarvinSession[]>([...mockSessions]);

function readCurrentId(): string {
  if (typeof localStorage === 'undefined') return sessions.value[0]?.id ?? '';
  const stored = localStorage.getItem(KEY);
  if (stored && sessions.value.some((s) => s.id === stored)) return stored;
  return sessions.value[0]?.id ?? '';
}

const currentSessionId = ref<string>(readCurrentId());

watch(currentSessionId, (v) => {
  if (typeof localStorage !== 'undefined') {
    localStorage.setItem(KEY, v);
  }
});

export function useSession() {
  function switchSession(id: string) {
    if (sessions.value.some((s) => s.id === id)) {
      currentSessionId.value = id;
    }
  }
  function createSession(): string {
    const id = newId();
    const now = Date.now();
    const session: DarvinSession = { id, title: '新建会话', updatedAt: now };
    sessions.value = [session, ...sessions.value];
    currentSessionId.value = id;
    return id;
  }
  return { sessions, currentSessionId, switchSession, createSession };
}
