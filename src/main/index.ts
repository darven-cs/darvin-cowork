import { app, BrowserWindow, ipcMain } from 'electron';
import path from 'node:path';
import { installAppMenu } from './menu';
import { RuntimeMgr, resolveAgentBinaryPath } from './runtime/manager';
import { AgentClient, DEFAULT_SESSION_ID } from './runtime/client';
import type {
  DarvinEvent,
  DarvinPromptRequest,
  DarvinRuntimeStatus,
} from '../shared/darvin-api';

declare const MAIN_WINDOW_VITE_DEV_SERVER_URL: string | undefined;
declare const MAIN_WINDOW_VITE_NAME: string;

if (require('electron-squirrel-startup')) {
  app.quit();
}

app.setName('Darvin');

const mgr = new RuntimeMgr();
const client = new AgentClient({ logger: console });
let shuttingDown = false;

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

// ── IPC：renderer(preload) → main → Go agent ──────────────────────────
//
// sessionId 一律归一到 DEFAULT_SESSION_ID：Agent v0 只跑单 session，
// gateway 会把其它 id 判成 -32602。renderer 侧仍用自己的本地 session id
// 做展示，两者在这里解耦。
ipcMain.handle('darvin:prompt', async (_e, req: DarvinPromptRequest) =>
  client.prompt({ ...req, sessionId: DEFAULT_SESSION_ID }),
);

ipcMain.handle('darvin:abort', async () =>
  client.abort({ sessionId: DEFAULT_SESSION_ID }),
);

// no-binary 用二进制是否存在判定，而不是 mgr 是否 resolved：子进程 crash
// 之后 resolvedPort 会被清掉，那种情况应该报 offline（可重启）而不是
// no-binary（要重新编译），两者对用户的下一步动作完全不同。
ipcMain.handle('darvin:status', (): DarvinRuntimeStatus => {
  if (!resolveAgentBinaryPath()) return 'no-binary';
  if (!client.isConnected()) return 'offline';
  return 'online';
});

// 事件单向下推到所有窗口，renderer 无需 ack
client.onEvent((ev: DarvinEvent) => {
  for (const win of BrowserWindow.getAllWindows()) {
    if (!win.isDestroyed()) win.webContents.send('darvin:event', ev);
  }
});

mgr.on('exit', ({ code, signal }: { code: number | null; signal: string | null }) => {
  console.error(`[runtime] darvin-agent exited code=${code} signal=${signal}`);
});

// ── 启动时序：spawn → WS 连 + 订阅 → 开窗 ─────────────────────────────
//
// 子进程起不来不阻塞开窗：窗口照常打开，badge 显示 no-binary / offline，
// 用户至少能看到界面和错误提示，而不是一个黑屏。
app.whenReady().then(async () => {
  installAppMenu();

  try {
    const resolved = await mgr.start();
    await client.connect(resolved.port);
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
