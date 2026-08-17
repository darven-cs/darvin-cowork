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

import { ref, watch } from 'vue';
import type { DarvinDeleteSessionResponse, DarvinSession } from '../../shared/darvin-api';

const KEY_PINNED = 'darvin.sidebar.pinned';

function readStoredPinned(): Set<string> {
  if (typeof localStorage === 'undefined') return new Set();
  try {
    const raw = localStorage.getItem(KEY_PINNED);
    return raw ? new Set(JSON.parse(raw) as string[]) : new Set();
  } catch {
    return new Set();
  }
}

const sessions = ref<DarvinSession[]>([]);
const activeSessionId = ref<string | null>(null);
/** 置顶会话 id 集合（localStorage 持久化；会话项 pinned 状态）。 */
const pinnedSessionIds = ref<Set<string>>(readStoredPinned());

watch(pinnedSessionIds, (v) => {
  if (typeof localStorage !== 'undefined') {
    localStorage.setItem(KEY_PINNED, JSON.stringify([...v]));
  }
});
/**
 * compose 态：点了「新建任务」但还没发首条消息。此时 main 端没有新
 * session，active 仍是上一个；UI 侧用该标志隐藏 active 高亮、让 send()
 * 在发首条消息时再真正建会话。
 */
const draftMode = ref(false);
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

  async function createSession(
    title?: string,
    workspaceId?: string,
    systemPrompt?: string,
    identity?: string,
  ): Promise<DarvinSession> {
    const r = await window.darvin.createSession({ title, workspaceId, systemPrompt, identity });
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

  async function renameSession(id: string, title: string): Promise<void> {
    const r = await window.darvin.renameSession(id, title);
    sessions.value = sessions.value.map((s) => (s.id === id ? r.session : s));
  }

  async function deleteSession(id: string): Promise<DarvinDeleteSessionResponse> {
    const r = await window.darvin.deleteSession(id);
    sessions.value = sessions.value.filter((s) => s.id !== id);
    activeSessionId.value = r.nextActiveSessionId;
    if (pinnedSessionIds.value.has(id)) {
      const next = new Set(pinnedSessionIds.value);
      next.delete(id);
      pinnedSessionIds.value = next;
    }
    return r;
  }

  function togglePin(id: string): void {
    const next = new Set(pinnedSessionIds.value);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    pinnedSessionIds.value = next;
  }

  return {
    sessions,
    activeSessionId,
    draftMode,
    pinnedSessionIds,
    createSession,
    switchSession,
    renameSession,
    deleteSession,
    togglePin,
    startNewTask: () => {
      draftMode.value = true;
    },
  };
}
