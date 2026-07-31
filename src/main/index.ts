/**
 * 主进程入口：
 * - Electron 生命周期 + BrowserWindow
 * - 启动 darvin-agent 子进程
 * - session / 消息 / active session 数据所有权（SessionStore）
 * - backend 事件 → renderer 路由（EventRouter）
 *
 * 数据所有权归 main：renderer 是纯 UI，所有 session / message 读写都
 * 走 IPC 进 main 端的 SQLite。每个 main session 映射到一个 backend session
 * —— 切换 session 不中断后台 session 的运行；每次 prompt 生成 runId，
 * abort 按 (sessionId, runId) 精确停某一次 turn。
 */

import { app, BrowserWindow, ipcMain } from 'electron';
import path from 'node:path';
import { randomUUID } from 'node:crypto';
import { readUserSettingsYAML, writeUserSettingsYAML } from './libs/user-settings';
import { installAppMenu } from './menu';
import { RuntimeMgr, resolveAgentBinaryPath } from './runtime/manager';
import { AgentClient } from './runtime/client';
import { SessionStore } from './store/SessionStore';
import { sessionStorePath } from './libs/user-paths';
import { EventRouter } from './store/EventRouter';
import type {
  DarvinLLMConfig,
  DarvinLocale,
  DarvinLocaleResponse,
  DarvinPromptRequest,
  DarvinPromptResponse,
  DarvinRuntimeStatus,
  DarvinSearchSessionsResponse,
  DarvinSetLLMConfigResponse,
  DarvinSession,
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
const store = new SessionStore(sessionStorePath());
const eventRouter = new EventRouter({
  store,
  client,
  getWindows: () => BrowserWindow.getAllWindows(),
});

// 每个 session 当前在跑的 runId；abort 时按 (sessionId, runId) 精确停。
// prompt 时写入；session 删时清掉。
const currentRunIdBySessionId = new Map<string, string>();
let shuttingDown = false;

function broadcastSessions(): void {
  const list = store.listSessions();
  for (const win of BrowserWindow.getAllWindows()) {
    if (!win.isDestroyed()) {
      win.webContents.send(DarvinPushEvent.SessionsChanged, list);
    }
  }
}

function broadcastActiveSession(): void {
  const active = store.getActive();
  for (const win of BrowserWindow.getAllWindows()) {
    if (!win.isDestroyed()) {
      win.webContents.send(DarvinPushEvent.ActiveSessionChanged, active);
    }
  }
}

// 把所有已知 session 订阅到 backend；多 session 时每个 session 自己一条
// 事件流。Go 侧 EventLedger 按 sessionId 维护订阅集合。
async function subscribeAllSessions(): Promise<void> {
  if (!client.isConnected()) return;
  for (const s of store.listSessions()) {
    try {
      await client.subscribeEvents(s.id);
    } catch (e) {
      console.warn(`[main] subscribe failed for ${s.id}: ${(e as Error).message}`);
    }
  }
}

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
  async (_e, req?: { title?: string }): Promise<{ session: DarvinSession }> => {
    const session = store.createSession(req?.title);
    store.setActive(session.id);
    // 给新 session 在 backend 留一条事件通道
    if (client.isConnected()) {
      try {
        await client.subscribeEvents(session.id);
      } catch (e) {
        console.warn(`[main] subscribe failed for new session: ${(e as Error).message}`);
      }
    }
    broadcastSessions();
    broadcastActiveSession();
    return { session };
  },
);

ipcMain.handle('darvin:list_sessions', () => ({ sessions: store.listSessions() }));

ipcMain.handle(
  'darvin:switch_session',
  (_e, sessionId: string): { sessionId: string | null } => {
    const ok = store.setActive(sessionId);
    if (!ok) {
      // 不抛错，让 caller 决定是否 surface — 通常说明 client 拿到了过时列表
      return { sessionId: store.getActive() };
    }
    broadcastActiveSession();
    // active 切换后顺手 touch list 的 updatedAt，让 sidebar 排序更新
    broadcastSessions();
    return { sessionId: store.getActive() };
  },
);

ipcMain.handle(
  'darvin:delete_session',
  async (
    _e,
    sessionId: string,
  ): Promise<{ deleted: boolean; nextActiveSessionId: string | null }> => {
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
    const r = store.deleteSession(sessionId);
    broadcastSessions();
    broadcastActiveSession();
    return r;
  },
);

ipcMain.handle(
  'darvin:rename_session',
  (_e, sessionId: string, title: string): { session: DarvinSession } => {
    const safe = title.trim() === '' ? '新建会话' : title.trim();
    const ok = store.updateTitle(sessionId, safe);
    if (!ok) throw new Error('session not found');
    broadcastSessions();
    return { session: store.getSession(sessionId)! };
  },
);

ipcMain.handle(
  'darvin:search_sessions',
  (_e, query: string): DarvinSearchSessionsResponse => {
    const q = (query ?? '').trim();
    if (!q) return { sessions: [], messages: [] };
    return { sessions: store.searchSessions(q), messages: store.searchMessages(q) };
  },
);

ipcMain.handle('darvin:get_active_session', () => ({ sessionId: store.getActive() }));

ipcMain.handle(
  'darvin:get_messages',
  (_e, sessionId: string): { messages: ReturnType<SessionStore['listMessages']> } => {
    return { messages: store.listMessages(sessionId) };
  },
);

ipcMain.handle(
  'darvin:prompt',
  async (_e, req: DarvinPromptRequest): Promise<DarvinPromptResponse> => {
    const sessionId = store.getActive();
    if (sessionId === null) {
      throw new Error('no active session');
    }
    const runId = randomUUID();
    currentRunIdBySessionId.set(sessionId, runId);
    const userMessageId = `m-${randomUUID()}`;
    store.appendMessage({
      id: userMessageId,
      sessionId,
      role: 'user',
      content: req.content,
      done: true,
    });
    try {
      const r = await client.prompt({
        content: req.content,
        sessionId,
        runId,
        model: req.model,
      });
      store.appendMessage({
        id: r.messageId,
        sessionId,
        role: 'assistant',
        content: '',
        done: false,
      });
      return { sessionId, messageId: r.messageId, runId: r.runId ?? runId };
    } catch (e) {
      currentRunIdBySessionId.delete(sessionId);
      store.markMessageError(userMessageId, (e as Error).message);
      throw e;
    }
  },
);

ipcMain.handle('darvin:abort', async () => {
  const sessionId = store.getActive();
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

  store.bootstrapActiveSession();

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
    try {
      store.close();
    } catch (e) {
      console.error(`[store] close failed: ${(e as Error).message}`);
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