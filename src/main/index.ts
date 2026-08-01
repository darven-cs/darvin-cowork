/**
 * 主进程入口：
 * - Electron 生命周期 + BrowserWindow
 * - 启动 darvin-agent 子进程
 * - session / 消息 / active session 数据所有权归 Go（merge-databases
 *   refactor 后 main 端不再持有 SQLite，全部经 JSON-RPC 走 Go 侧）
 * - backend 事件 → renderer 路由（EventRouter，纯转发不落库）
 *
 * 数据所有权归 Go：renderer 是纯 UI，所有 session / message 读写都走
 * IPC 进 main，main 再透传 JSON-RPC 给 Go 的 sessions.db。Go 离线时
 * main 用进程内 in-memory 缓存兜底（FR-8），保证最近一次视图可见。
 */

import { app, BrowserWindow, ipcMain } from 'electron';
import path from 'node:path';
import { randomUUID } from 'node:crypto';
import { readUserSettingsYAML, writeUserSettingsYAML } from './libs/user-settings';
import { installAppMenu } from './menu';
import { RuntimeMgr, resolveAgentBinaryPath } from './runtime/manager';
import { AgentClient } from './runtime/client';
import { EventRouter } from './store/EventRouter';
import type {
  DarvinActiveSessionResponse,
  DarvinCreateSessionResponse,
  DarvinDeleteSessionResponse,
  DarvinGetMessagesResponse,
  DarvinListSessionsResponse,
  DarvinLLMConfig,
  DarvinLocale,
  DarvinLocaleResponse,
  DarvinMessage,
  DarvinPromptRequest,
  DarvinPromptResponse,
  DarvinRenameSessionResponse,
  DarvinRuntimeStatus,
  DarvinSearchSessionsResponse,
  DarvinSession,
  DarvinSetLLMConfigResponse,
  DarvinSwitchSessionResponse,
} from '../shared/darvin-api';
import { DarvinPushEvent } from '../shared/darvin-api';

declare const MAIN_WINDOW_VITE_DEV_SERVER_URL: string | undefined;
declare const MAIN_WINDOW_VITE_NAME: string;

if (require('electron-squirrel-startup')) {
  app.quit();
}

app.setName('darvin-cowork');

if (!app.isPackaged) {
  app.commandLine.appendSwitch('remote-debugging-port', '9222')
  // 必须放开跨域，否则 Playwright 连不上调试端口
  app.commandLine.appendSwitch('remote-allow-origins', '*')
}

const mgr = new RuntimeMgr();
const client = new AgentClient({ logger: console });

// 每个 session 当前在跑的 runId；abort 时按 (sessionId, runId) 精确停。
// prompt 时写入；session 删时清掉。
const currentRunIdBySessionId = new Map<string, string>();
let shuttingDown = false;

/**
 * FR-8：main 端 in-memory 缓存，Go 离线时兜底最近一次视图。缓存是只读
 * fallback，写操作必须等 RPC 成功后才更新。
 */
interface CacheState {
  sessions: DarvinSession[] | null;              // 最近一次 list_sessions
  activeSessionId: string | null | undefined;    // 最近一次 get_active_session；undefined = 还没查过
  messagesBySession: Map<string, DarvinMessage[]>; // 最近一次 get_messages(sid)
}

const cache: CacheState = {
  sessions: null,
  activeSessionId: undefined,
  messagesBySession: new Map(),
};

/**
 * FR-10：sessionId → title 的 main 进程级缓存，`client.list_sessions` 成功
 * 时填；EventRouter 收 done 事件后 notifyIfHidden 用它补通知标题。
 */
const sessionTitles = new Map<string, string>();

function updateCacheFromListSessions(sessions: DarvinSession[]): void {
  cache.sessions = sessions;
  for (const s of sessions) {
    sessionTitles.set(s.id, s.title);
  }
}

function updateCacheFromGetMessages(sid: string, msgs: DarvinMessage[]): void {
  cache.messagesBySession.set(sid, msgs);
}

function broadcastSessions(): void {
  const list = cache.sessions ?? [];
  for (const win of BrowserWindow.getAllWindows()) {
    if (!win.isDestroyed()) {
      win.webContents.send(DarvinPushEvent.SessionsChanged, list);
    }
  }
}

function broadcastActiveSession(): void {
  const active = cache.activeSessionId ?? null;
  for (const win of BrowserWindow.getAllWindows()) {
    if (!win.isDestroyed()) {
      win.webContents.send(DarvinPushEvent.ActiveSessionChanged, active);
    }
  }
}

// 把 main 端持有的 active / list 状态同步回 Go（删除后 active 推进、
// switch 后 updated_at 排序等，都以 Go 的响应为准），再刷新缓存 + broadcast。
async function refreshSessionsAndBroadcast(): Promise<void> {
  if (!client.isConnected()) return;
  try {
    const r = await client.listSessions();
    updateCacheFromListSessions(r.sessions);
    broadcastSessions();
  } catch (e) {
    console.warn(`[main] refresh sessions failed: ${(e as Error).message}`);
  }
}

// 把所有已知 session 订阅到 backend；多 session 时每个 session 自己一条
// 事件流。Go 侧 EventLedger 按 sessionId 维护订阅集合。
async function subscribeAllSessions(): Promise<void> {
  if (!client.isConnected()) return;
  let sessions: DarvinSession[] = [];
  try {
    const r = await client.listSessions();
    updateCacheFromListSessions(r.sessions);
    sessions = r.sessions;
  } catch (e) {
    console.warn(`[main] list sessions failed: ${(e as Error).message}`);
    return;
  }
  for (const s of sessions) {
    try {
      await client.subscribeEvents(s.id);
    } catch (e) {
      console.warn(`[main] subscribe failed for ${s.id}: ${(e as Error).message}`);
    }
  }
}

const eventRouter = new EventRouter({
  client,
  getWindows: () => BrowserWindow.getAllWindows(),
  getTitle: (sessionId) => sessionTitles.get(sessionId),
});

const createWindow = (): void => {
  const mainWindow = new BrowserWindow({
    height: 800,
    width: 1200,
    webPreferences: {
      preload: path.join(__dirname, '../preload/index.js'),
    },
  });

  mainWindow.webContents.on('before-input-event', (_event, input) => {
    if (
      input.key === 'F12' ||
      ((input.control || input.meta) && input.shift && input.key.toLowerCase() === 'i')
    ) {
      mainWindow.webContents.toggleDevTools();
    }
  });

  if (process.env.NODE_ENV !== 'production') {
    mainWindow.loadURL(MAIN_WINDOW_VITE_DEV_SERVER_URL!);
  } else {
    mainWindow.loadFile(
      path.join(__dirname, `../renderer/${MAIN_WINDOW_VITE_NAME}/index.html`),
    );
  }
};

ipcMain.handle(
  'darvin:create_session',
  async (_e, req?: { title?: string }): Promise<DarvinCreateSessionResponse> => {
    if (!client.isConnected()) throw new Error('agent offline');
    const r = await client.request<DarvinCreateSessionResponse>(
      'agent.create_session',
      { title: req?.title },
    );
    cache.activeSessionId = r.session.id;
    // 给新 session 在 backend 留一条事件通道
    if (client.isConnected()) {
      try {
        await client.subscribeEvents(r.session.id);
      } catch (e) {
        console.warn(`[main] subscribe failed for new session: ${(e as Error).message}`);
      }
    }
    await refreshSessionsAndBroadcast();
    broadcastActiveSession();
    return r;
  },
);

ipcMain.handle(
  'darvin:list_sessions',
  async (): Promise<DarvinListSessionsResponse> => {
    if (!client.isConnected()) return { sessions: cache.sessions ?? [] };
    const r = await client.listSessions();
    updateCacheFromListSessions(r.sessions);
    return r;
  },
);

ipcMain.handle(
  'darvin:switch_session',
  async (_e, sessionId: string): Promise<DarvinSwitchSessionResponse> => {
    if (!client.isConnected()) throw new Error('agent offline');
    const r = await client.request<DarvinSwitchSessionResponse>(
      'agent.set_active_session',
      { sessionId },
    );
    cache.activeSessionId = r.sessionId;
    // active 切换后顺手 touch list 的 updatedAt，让 sidebar 排序更新
    await refreshSessionsAndBroadcast();
    broadcastActiveSession();
    return r;
  },
);

ipcMain.handle(
  'darvin:delete_session',
  async (_e, sessionId: string): Promise<DarvinDeleteSessionResponse> => {
    if (!client.isConnected()) throw new Error('agent offline');
    // 删之前先 abort 该 session 当前正在跑的 turn（若有）
    const runId = currentRunIdBySessionId.get(sessionId);
    if (runId && client.isConnected()) {
      try {
        await client.abort({ sessionId, runId });
      } catch {
        /* session 可能从没 prompt 过；忽略 */
      }
    }
    currentRunIdBySessionId.delete(sessionId);
    const r = await client.request<DarvinDeleteSessionResponse>(
      'agent.delete_session',
      { sessionId },
    );
    cache.activeSessionId = r.nextActiveSessionId;
    await refreshSessionsAndBroadcast();
    broadcastActiveSession();
    return r;
  },
);

ipcMain.handle(
  'darvin:rename_session',
  async (_e, sessionId: string, title: string): Promise<DarvinRenameSessionResponse> => {
    if (!client.isConnected()) throw new Error('agent offline');
    // 空 title 的 '新建会话' fallback 由 Go 端 handler 处理
    const r = await client.request<DarvinRenameSessionResponse>(
      'agent.rename_session',
      { sessionId, title },
    );
    await refreshSessionsAndBroadcast();
    return r;
  },
);

ipcMain.handle(
  'darvin:search_sessions',
  async (_e, query: string): Promise<DarvinSearchSessionsResponse> => {
    const q = (query ?? '').trim();
    if (!q) return { sessions: [], messages: [] };
    if (!client.isConnected()) {
      // 离线回退：只在 title 缓存里做子串匹配，消息命中暂不提供
      const ql = q.toLowerCase();
      const sessions = (cache.sessions ?? []).filter((s) =>
        s.title.toLowerCase().includes(ql),
      );
      return { sessions, messages: [] };
    }
    return client.request<DarvinSearchSessionsResponse>(
      'agent.search_sessions',
      { query: q },
    );
  },
);

ipcMain.handle(
  'darvin:get_active_session',
  async (): Promise<DarvinActiveSessionResponse> => {
    if (!client.isConnected()) return { sessionId: cache.activeSessionId ?? null };
    const r = await client.request<DarvinActiveSessionResponse>('agent.get_active_session', {});
    cache.activeSessionId = r.sessionId;
    return r;
  },
);

ipcMain.handle(
  'darvin:get_messages',
  async (_e, sessionId: string): Promise<DarvinGetMessagesResponse> => {
    if (!client.isConnected()) {
      return { messages: cache.messagesBySession.get(sessionId) ?? [] };
    }
    const r = await client.getMessages(sessionId);
    updateCacheFromGetMessages(sessionId, r.messages);
    return r;
  },
);

ipcMain.handle(
  'darvin:prompt',
  async (_e, req: DarvinPromptRequest): Promise<DarvinPromptResponse> => {
    let sessionId: string;
    try {
      const a = await client.request<DarvinActiveSessionResponse>('agent.get_active_session', {});
      if (a.sessionId === null) throw new Error('no active session');
      sessionId = a.sessionId;
    } catch (e) {
      if (!client.isConnected()) throw new Error('agent offline');
      throw e;
    }
    const runId = randomUUID();
    currentRunIdBySessionId.set(sessionId, runId);
    // user message 落库由 Go 端 persistUserMessage hook 做（FR-4），main 不再写
    try {
      const r = await client.prompt({
        content: req.content,
        sessionId,
        runId,
        model: req.model,
      });
      return { sessionId, messageId: r.messageId, runId: r.runId ?? runId };
    } catch (e) {
      currentRunIdBySessionId.delete(sessionId);
      throw e;
    }
  },
);

ipcMain.handle('darvin:abort', async () => {
  const sessionId = cache.activeSessionId ?? null;
  if (sessionId === null) return { aborted: false, sessionId: '' };
  const runId = currentRunIdBySessionId.get(sessionId);
  if (runId === undefined) {
    // session 没在跑；返回 aborted=true 让 renderer 不要重试
    return { aborted: true, sessionId };
  }
  try {
    return await client.abort({ sessionId, runId });
  } catch {
    // abort 失败（例如 session 已结束）；当作 aborted 成功
    currentRunIdBySessionId.delete(sessionId);
    return { aborted: true, sessionId };
  }
});

ipcMain.handle('darvin:status', (): DarvinRuntimeStatus => {
  if (!resolveAgentBinaryPath()) return 'no-binary';
  if (!client.isConnected()) return 'offline';
  return 'online';
});

ipcMain.handle('darvin:get_llm_config', async (): Promise<DarvinLLMConfig> => {
  const cfg = await readUserSettingsYAML();
  return {
    provider: 'anthropic',
    apiKey: cfg?.llm?.api_key ?? '',
    baseUrl: cfg?.llm?.base_url ?? '',
  };
});

ipcMain.handle(
  'darvin:set_llm_config',
  async (
    _e,
    req: { apiKey: string; baseUrl?: string },
  ): Promise<DarvinSetLLMConfigResponse> => {
    await writeUserSettingsYAML({ llm: { api_key: req.apiKey, base_url: req.baseUrl ?? '' } });
    const restarted = await restartGoSubprocess();
    return { saved: true, restarted };
  },
);

ipcMain.handle(
  'darvin:get_locale',
  async (): Promise<DarvinLocaleResponse> => {
    const cfg = await readUserSettingsYAML();
    return { locale: cfg?.locale ?? 'zh' };
  },
);

ipcMain.handle(
  'darvin:set_locale',
  async (_e, req: { locale: DarvinLocale }): Promise<void> => {
    await writeUserSettingsYAML({ locale: req.locale });
  },
);

mgr.on('exit', ({ code, signal }: { code: number | null; signal: string | null }) => {
  console.error(`[runtime] darvin-agent exited code=${code} signal=${signal}`);
});

/**
 * 重启 Go 子进程：用于 set_llm_config 等需要冷启动以加载新配置的场景。
 *
 * 返回值表示是否真的拉起了一个新子进程。binary 缺失或 restart 失败
 * 时返 false，caller 决定如何 surface 给 UI（toast 即可，不阻塞写盘）。
 */
async function restartGoSubprocess(): Promise<boolean> {
  if (!resolveAgentBinaryPath()) return false;
  eventRouter.stop();
  try {
    await client.disconnect();
  } catch {
    /* 可能本就没连上 */
  }
  try {
    await mgr.stop();
  } catch {
    /* 已退出 */
  }
  try {
    const resolved = await mgr.start();
    await client.connect(resolved.port);
    await subscribeAllSessions();
    eventRouter.start();
    return true;
  } catch (e) {
    console.error(`[runtime] restart failed: ${(e as Error).message}`);
    return false;
  }
}

app.whenReady().then(async () => {
  installAppMenu();

  try {
    const resolved = await mgr.start();
    await client.connect(resolved.port);
    await subscribeAllSessions();
    eventRouter.start();
  } catch (e) {
    console.error(`[runtime] ${(e as Error).message}`);
  }

  createWindow();
});

app.on('before-quit', (e) => {
  if (shuttingDown) return;
  e.preventDefault();
  shuttingDown = true;
  void (async () => {
    try {
      eventRouter.stop();
      await client.disconnect();
    } catch {
      /* 已断开 */
    }
    try {
      await mgr.stop();
    } catch {
      /* 已退出 */
    }
    app.quit();
  })();
});

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') {
    app.quit();
  }
});

app.on('activate', () => {
  if (BrowserWindow.getAllWindows().length === 0) {
    createWindow();
  }
});
