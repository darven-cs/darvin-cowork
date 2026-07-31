/**
 * useSession — renderer 端 session 状态的薄视图。
 *
 * 数据所有权归 main（SessionStore in `src/main/store/SessionStore.ts`）。
 * renderer 不持任何持久化状态：所有读写都过 IPC；active session id
 * 由 main push 给 renderer（不是 renderer 自己切完再 sync 回去）。
 *
 * composable 内部：
 * - 初始化时 `getActiveSession` + `listSessions` 拉一次当前快照
 * - 订阅 main push 的 `sessions:changed` / `active-session:changed`
 * - 暴露 `createSession` / `switchSession` / `deleteSession` 命令入口
 */

import { ref } from 'vue';
import type { DarvinSession } from '../../shared/darvin-api';

const sessions = ref<DarvinSession[]>([]);
const activeSessionId = ref<string | null>(null);
let initialized = false;

function ensureSubscribed(): void {
  if (initialized || typeof window === 'undefined' || !window.darvin) return;
  initialized = true;

  window.darvin.onSessionsChanged((list) => {
    sessions.value = list;
  });
  window.darvin.onActiveSessionChanged((id) => {
    activeSessionId.value = id;
  });

  void (async () => {
    try {
      const [s, a] = await Promise.all([
        window.darvin.listSessions(),
        window.darvin.getActiveSession(),
      ]);
      sessions.value = s.sessions;
      activeSessionId.value = a.sessionId;
    } catch {
      // agent offline: badge carries the user-visible state.
    }
  })();
}

export function useSession() {
  ensureSubscribed();

  async function createSession(title?: string): Promise<DarvinSession> {
    const r = await window.darvin.createSession({ title });
    if (!sessions.value.some((s) => s.id === r.session.id)) {
      sessions.value = [r.session, ...sessions.value];
    }
    activeSessionId.value = r.session.id;
    return r.session;
  }

  async function switchSession(id: string): Promise<void> {
    if (activeSessionId.value === id) return;
    await window.darvin.switchSession(id);
    activeSessionId.value = id;
  }

  async function deleteSession(id: string): Promise<void> {
    await window.darvin.deleteSession(id);
  }

  return {
    sessions,
    activeSessionId,
    createSession,
    switchSession,
    deleteSession,
  };
}