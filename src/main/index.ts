/**
 * 主进程入口：
 * - Electron 生命周期 + BrowserWindow
 * - 启动 darvin-agent 子进程
 * - session / 消息 / active session 数据所有权（SessionStore）
 * - backend 事件 → renderer 路由（EventRouter）
 *
 * 数据所有权归 main：renderer 是纯 UI，所有 session / message 读写都
 * 走 IPC 进 main 端的 SQLite。backend (Go agent) 单 session 时，main
 * 把多个 renderer session 的 prompt 都路由到同一个 backend session。
 */

import { app, BrowserWindow, ipcMain } from 'electron';
import path from 'node:path';
import { randomUUID } from 'node:crypto';
import { readUserSettingsYAML, writeUserSettingsYAML } from './libs/user-settings';
import { installAppMenu } from './menu';
import { RuntimeMgr, resolveAgentBinaryPath } from './runtime/manager';
import { AgentClient, BACKEND_DEFAULT_SESSION_ID } from './runtime/client';
import { SessionStore, defaultSessionStorePath } from './store/SessionStore';
import { EventRouter } from './store/EventRouter';
import type {
  DarvinLLMConfig,
  DarvinLocale,
  DarvinLocaleResponse,
  DarvinPromptRequest,
  DarvinPromptResponse,
  DarvinRuntimeStatus,
  DarvinSetLLMConfigResponse,
  DarvinSession,
} from '../shared/darvin-api';
import { DarvinPushEvent } from '../shared/darvin-api';

declare const MAIN_WINDOW_VITE_DEV_SERVER_URL: string | undefined;
declare const MAIN_WINDOW_VITE_NAME: string;

if (require('electron-squirrel-startup')) {
  app.quit();
}

app.setName('Darvin-Cowork');

if (!app.isPackaged) {
  // 固定调试端口 9222
  app.commandLine.appendSwitch('remote-debugging-port', '9222')
  // 允许任意客户端跨域连接调试端口（必须加，否则 Playwright 连不上）
  app.commandLine.appendSwitch('remote-allow-origins', '*')
}

const mgr = new RuntimeMgr();
const client = new AgentClient({ logger: console });
const store = new SessionStore(defaultSessionStorePath(app.getPath('userData')));
const eventRouter = new EventRouter({
  store,
  client,
  getWindows: () => BrowserWindow.getAllWindows(),
});
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

// ── IPC：session 管理（data owner = main）────────────────────────────
ipcMain.handle(
  'darvin:create_session',
  (_e, req?: { title?: string }): { session: DarvinSession } => {
    const session = store.createSession(req?.title);
    // 新创建的 session 自动成为 active，避免 renderer 多走一步
    store.setActive(session.id);
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
  (_e, sessionId: string): { deleted: boolean; nextActiveSessionId: string | null } => {
    const r = store.deleteSession(sessionId);
    broadcastSessions();
    broadcastActiveSession();
    return r;
  },
);

ipcMain.handle('darvin:get_active_session', () => ({ sessionId: store.getActive() }));

ipcMain.handle(
  'darvin:get_messages',
  (_e, sessionId: string): { messages: ReturnType<SessionStore['listMessages']> } => {
    return { messages: store.listMessages(sessionId) };
  },
);

// ── IPC：prompt / abort ───────────────────────────────────────────────
//
// sessionId 由 main 自己管 —— 调用方不传，main 从 store.getActive()
// 拿到当前 session，先把 user message 落库 + 再调 backend prompt。
// backend 单 session：所有 main 的 active session 都路由到同一个
// backend "default" session。
ipcMain.handle(
  'darvin:prompt',
  async (_e, req: DarvinPromptRequest): Promise<DarvinPromptResponse> => {
    const sessionId = store.getActive();
    if (sessionId === null) {
      throw new Error('no active session');
    }
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
        sessionId: BACKEND_DEFAULT_SESSION_ID,
        model: req.model,
      });
      // 把 assistant message 也落库 —— streaming 文本由 EventRouter 累加
      store.appendMessage({
        id: r.messageId,
        sessionId,
        role: 'assistant',
        content: '',
        done: false,
      });
      return { sessionId, messageId: r.messageId };
    } catch (e) {
      // prompt 失败时把 user message 标红提示用户，assistant 占位由
      // renderer 端 ChatPane 自己用 error message 补。
      store.markMessageError(userMessageId, (e as Error).message);
      throw e;
    }
  },
);

ipcMain.handle('darvin:abort', async () => {
  const sessionId = store.getActive();
  if (sessionId === null) return { aborted: false, sessionId: '' };
  return client.abort({ sessionId: BACKEND_DEFAULT_SESSION_ID });
});

// ── IPC：runtime status ───────────────────────────────────────────────
//
// no-binary 用二进制是否存在判定，而不是 mgr 是否 resolved：子进程 crash
// 之后 resolvedPort 会被清掉，那种情况应该报 offline（可重启）而不是
// no-binary（要重新编译），两者对用户的下一步动作完全不同。
ipcMain.handle('darvin:status', (): DarvinRuntimeStatus => {
  if (!resolveAgentBinaryPath()) return 'no-binary';
  if (!client.isConnected()) return 'offline';
  return 'online';
});

// ── IPC：LLM 配置（spec FR-5.2）──────────────────────────────────────
//
// get 走 user-level yaml；set 写盘 + 重启 Go 子进程，子进程重启后才用
// 新 key（冷启动语义，避免热更新 LLM）。
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

// ── IPC：locale（renderer i18n）──────────────────────────────────────
//
// locale 不需要重启 Go 子进程；renderer 拿到 set 返回值后自己 setLang。
// 写入只 patch locale 字段，由 writeUserSettingsYAML merge 保留 llm 配置。
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

// EventRouter 在启动序列里显式调 start()/stop()，不需要单独的兜底订阅。

mgr.on('exit', ({ code, signal }: { code: number | null; signal: string | null }) => {
  console.error(`[runtime] darvin-agent exited code=${code} signal=${signal}`);
});

/**
 * 重启 Go 子进程：用于 set_llm_config 等需要冷启动以加载新配置的场景。
 * 顺序：client.disconnect → mgr.stop → mgr.start → client.connect →
 *        subscribe_events → eventRouter.start。
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
    // backend 单 session：重新订阅 "default" 让 EventRouter 能继续收事件
    await client.subscribeEvents(BACKEND_DEFAULT_SESSION_ID);
    eventRouter.start();
    return true;
  } catch (e) {
    console.error(`[runtime] restart failed: ${(e as Error).message}`);
    return false;
  }
}

// ── 启动时序：spawn → WS 连 + 订阅 → EventRouter → 开窗 ──────────────
//
// 子进程起不来不阻塞开窗：窗口照常打开，badge 显示 no-binary / offline，
// 用户至少能看到界面和错误提示，而不是一个黑屏。
app.whenReady().then(async () => {
  installAppMenu();

  // main 端兜底一个 session：first-run 时 db 空，用户进来点 "+" 才正式创建；
  // 已有数据时 bootstrapActiveSession 选最近 updated 的那一个。
  store.bootstrapActiveSession();

  try {
    const resolved = await mgr.start();
    await client.connect(resolved.port);
    await client.subscribeEvents(BACKEND_DEFAULT_SESSION_ID);
    eventRouter.start();
  } catch (e) {
    console.error(`[runtime] ${(e as Error).message}`);
  }

  createWindow();
});

// ── 关闭时序：before-quit 是唯一能保证 graceful 的钩子 ────────────────
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