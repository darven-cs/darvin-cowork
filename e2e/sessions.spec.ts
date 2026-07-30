import { test, expect, launchApp, closeApp, type DarvinBridgeGlobal } from './helpers';

/**
 * session 管理（data owner = main）的 e2e。
 *
 * 不依赖 LLM key：走 IPC 直接验证 create / switch / delete / getActive
 * 的语义。SessionStore 是 main 端的 SQLite，sidebar / ChatPane 不再
 * 自己持有 state —— 这组 spec 把"main 是数据唯一所有者"落到端到端。
 */

interface SessionsApi {
  createSession(req?: { title?: string }): Promise<{ session: { id: string; title: string } }>;
  listSessions(): Promise<{ sessions: Array<{ id: string; title: string }> }>;
  switchSession(id: string): Promise<{ sessionId: string | null }>;
  deleteSession(id: string): Promise<{ deleted: boolean; nextActiveSessionId: string | null }>;
  getActiveSession(): Promise<{ sessionId: string | null }>;
  getMessages(id: string): Promise<{ messages: unknown[] }>;
}

function asSessionsApi(g: DarvinBridgeGlobal): SessionsApi {
  return g.darvin as unknown as SessionsApi;
}

test('@core session create / list / switch round-trip', async () => {
  const { app, window } = await launchApp();
  try {
    const created = await window.evaluate(async () => {
      const api = (globalThis as unknown as DarvinBridgeGlobal).darvin as unknown as SessionsApi;
      const a = await api.createSession({ title: 'A' });
      const b = await api.createSession({ title: 'B' });
      const list = await api.listSessions();
      return { a: a.session, b: b.session, list: list.sessions };
    });
    expect(created.a.id).not.toEqual(created.b.id);
    // 排序按 updated_at desc，b 后创建应排在前
    expect(created.list[0]?.id).toBe(created.b.id);

    // 创建后 b 应当是 active（main 把最近 created 设为 active）
    const active = await window.evaluate(async () => {
      const api = (globalThis as unknown as DarvinBridgeGlobal).darvin as unknown as SessionsApi;
      return api.getActiveSession();
    });
    expect(active.sessionId).toBe(created.b.id);

    // 切到 a
    const switched = await window.evaluate(async (id) => {
      const api = (globalThis as unknown as DarvinBridgeGlobal).darvin as unknown as SessionsApi;
      return api.switchSession(id);
    }, created.a.id);
    expect(switched.sessionId).toBe(created.a.id);
  } finally {
    await closeApp(app);
  }
});

test('@core session delete falls back to next when active is removed', async () => {
  const { app, window } = await launchApp();
  try {
    const result = await window.evaluate(async () => {
      const api = (globalThis as unknown as DarvinBridgeGlobal).darvin as unknown as SessionsApi;
      await api.createSession({ title: 'A' });
      const b = await api.createSession({ title: 'B' });
      // 当前 active 是 b
      const r = await api.deleteSession(b.session.id);
      const list = await api.listSessions();
      const active = await api.getActiveSession();
      return {
        deleted: r.deleted,
        nextActiveSessionId: r.nextActiveSessionId,
        listIds: list.sessions.map((s) => s.id),
        activeId: active.sessionId,
      };
    });
    expect(result.deleted).toBe(true);
    expect(result.nextActiveSessionId).toBe(result.listIds[0] ?? null);
    expect(result.activeId).toBe(result.nextActiveSessionId);
  } finally {
    await closeApp(app);
  }
});

test('@core getMessages returns empty array for unknown session', async () => {
  const { app, window } = await launchApp();
  try {
    const result = await window.evaluate(async () => {
      const api = (globalThis as unknown as DarvinBridgeGlobal).darvin as unknown as SessionsApi;
      return api.getMessages('nonexistent-session-id');
    });
    expect(Array.isArray(result.messages)).toBe(true);
    expect(result.messages.length).toBe(0);
  } finally {
    await closeApp(app);
  }
});

test('@core session list survives app restart (TS SessionStore)', async () => {
  const first = await launchApp();
  let firstId = '';
  try {
    firstId = await first.window.evaluate(async () => {
      const api = (globalThis as unknown as DarvinBridgeGlobal).darvin as unknown as SessionsApi;
      const r = await api.createSession({ title: 'persistent' });
      return r.session.id;
    });
  } finally {
    await closeApp(first.app);
  }

  const second = await launchApp();
  try {
    const list = await second.window.evaluate(async () => {
      const api = (globalThis as unknown as DarvinBridgeGlobal).darvin as unknown as SessionsApi;
      return api.listSessions();
    });
    const ids = list.sessions.map((s) => s.id);
    expect(ids).toContain(firstId);
  } finally {
    await closeApp(second.app);
  }
});

void asSessionsApi;