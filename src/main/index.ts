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

import { app, BrowserWindow, dialog, ipcMain, net, shell } from 'electron';
import path from 'node:path';
import fs from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import { readUserSettingsYAML, writeUserSettingsYAML } from './libs/user-settings';
import { installAppMenu } from './menu';
import { RuntimeMgr, resolveAgentBinaryPath } from './runtime/manager';
import { AgentClient } from './runtime/client';
import { EventRouter } from './store/EventRouter';
import { runImport } from './libs/importFiles';
import { ensureWorkspaceRoot, getSkillsRoot, userDataDir, type WorkspaceLocation } from './libs/user-paths';
import { readWorkspaceMap, writeWorkspaceMap } from './libs/workspace-map';
import { readWorkspaceTextFile, resolveWorkspacePath, walkWorkspace } from './libs/workspaceFiles';
import { artifactPreviewServer } from './services/artifact-preview-server';
import { SkillManager } from './libs/skillManager';
import { installSkillFromFolder, uninstallSkill } from './libs/skillInstall';
import { McpManager } from './libs/mcpManager';
import { McpStore } from './libs/mcpStore';
import { createScheduleProxy } from './libs/scheduleProxy';
import { createIMProxy } from './libs/imProxy';
import type {
  DarvinActiveSessionResponse,
  DarvinAppInfo,
  DarvinAttachmentRef,
  DarvinAppPreferences,
  DarvinAppPreferencesPatch,
  DarvinCompactContextResponse,
  DarvinCreateArtifactPreviewSessionResponse,
  DarvinCreateSessionResponse,
  DarvinDeleteSessionResponse,
  DarvinDestroyArtifactPreviewSessionResponse,
  DarvinGetMessagesResponse,
  DarvinGetSessionUsageResponse,
  DarvinGetSkillDetailsResponse,
  DarvinImportFilesResponse,
  DarvinInstallSkillResponse,
  DarvinInvokeSkillRequest,
  DarvinInvokeSkillResponse,
  DarvinListImportedFilesResponse,
  DarvinListSessionsResponse,
  DarvinListSkillsResponse,
  DarvinListToolsResponse,
  DarvinListWorkspaceFilesResponse,
  DarvinLocalServiceInfo,
  DarvinMcpServerCreate,
  DarvinMcpServerPatch,
  DarvinListMcpServersResponse,
  DarvinCreateMcpServerResponse,
  DarvinUpdateMcpServerResponse,
  DarvinDeleteMcpServerResponse,
  DarvinSetMcpServerEnabledRequest,
  DarvinSetMcpServerEnabledResponse,
  DarvinTestMcpConnectionRequest,
  DarvinTestMcpConnectionResponse,
  DarvinRetryMcpLaunchResolutionResponse,
  DarvinOpenWorkspaceFileResponse,
  DarvinLLMConfig,
  DarvinLocale,
  DarvinModelInfo,
  DarvinLocaleResponse,
  DarvinMessage,
  DarvinPermissionResponse,
  DarvinPickAttachmentsResponse,
  DarvinPickSkillFolderResponse,
  DarvinPromptRequest,
  DarvinPromptResponse,
  DarvinReadFileDataUrlResponse,
  DarvinReadWorkspaceFileResponse,
  DarvinRemoveImportedFileResponse,
  DarvinRenameSessionResponse,
  DarvinRuntimeStatus,
  DarvinSearchSessionsResponse,
  DarvinSession,
  DarvinSessionUsage,
  DarvinSetLLMConfigResponse,
  DarvinSetSkillEnabledRequest,
  DarvinSetSkillEnabledResponse,
  DarvinSetWorkspaceResult,
  DarvinSubagentGetMessagesResponse,
  DarvinSubagentListResponse,
  DarvinSubagentReadResultResponse,
  DarvinSwitchSessionResponse,
  DarvinUninstallSkillResponse,
  DarvinUpgradeSkillResponse,
  DarvinWorkspace,
  DarvinListWorkspacesResponse,
  DarvinCreateWorkspaceResponse,
  DarvinActiveWorkspaceResponse,
  DarvinSetActiveWorkspaceResponse,
  DarvinDeleteWorkspaceResponse,
  DarvinBindSessionWorkspaceResponse,
  DarvinWorkspaceInfoResponse,
  DarvinWorkspaceRootResult,
  DarvinAgent,
  DarvinApi,
} from '../shared/darvin-api';
import { DarvinPushEvent } from '../shared/darvin-api';
import { DARVIN_PROVIDERS, darvinProviderPreset } from '../shared/providers';

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
// main 端 skills 状态管理器。启动期调 bootstrap()，restart 路径
// 通过 restartGoSubprocess 重置后再 bootstrap。
const skillManager = new SkillManager({ client, logger: console });
// main 端 mcp 状态管理器。SQLite 独立，bundled filesystem
// 启动期幂等插入；list 走本地缓存（source of truth），增 / 删 / 改 /
// 启停走 SQLite + Go RPC；connection / resolution 变更由 Go → main 推回。
const mcpStore = new McpStore();
const mcpManager = new McpManager({ client, store: mcpStore, logger: console });

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
  usageBySession: Map<string, DarvinSessionUsage>; // 最近一次 get_session_usage(sid)
}

const cache: CacheState = {
  sessions: null,
  activeSessionId: undefined,
  messagesBySession: new Map(),
  usageBySession: new Map(),
};

/**
 * FR-10：sessionId → title 的 main 进程级缓存，`client.list_sessions` 成功
 * 时填；EventRouter 收 done 事件后 notifyIfHidden 用它补通知标题。
 */
const sessionTitles = new Map<string, string>();

/**
 * 受控 workspace 根。workspace 是一等实体：workspaceLoc.workspaceId 是
 * workspaces 表的真实 id（不再是 session id）。Go agent 的 fsSandbox.root
 * 通过 DARVIN_AGENT_WORKSPACE 与之对齐；所有 import/remove/list 都走它。
 */
let workspaceLoc: WorkspaceLocation | null = null;

/** workspaces 表缓存：Go 端是 source of truth，main 端维护 id→记录快照。 */
const workspaceCache = new Map<string, DarvinWorkspace>();
let activeWorkspaceId: string | null = null;

/** 拉取全量 workspace 列表刷新缓存；可用于启动期与任何变更后的同步。 */
async function refreshWorkspaceCache(): Promise<void> {
  if (!client.isConnected()) return;
  try {
    const r = await client.request<DarvinListWorkspacesResponse>('agent.list_workspaces', {});
    workspaceCache.clear();
    for (const w of r.workspaces) workspaceCache.set(w.id, w);
  } catch (e) {
    console.warn(`[main] workspace list refresh failed: ${(e as Error).message}`);
  }
}

/** 从缓存解析 workspace 根；未命中时刷新一次缓存再查。缺失返回 null。 */
async function resolveWorkspaceRoot(workspaceId: string): Promise<WorkspaceLocation | null> {
  const hit = workspaceCache.get(workspaceId);
  if (hit) return { rootPath: hit.rootPath, workspaceId };
  await refreshWorkspaceCache();
  const w = workspaceCache.get(workspaceId);
  return w ? { rootPath: w.rootPath, workspaceId } : null;
}

/** workspace 列表变更后广播给所有窗口（创建 / 删除 / 切换 active）。 */
function broadcastWorkspacesChanged(): void {
  const workspaces = [...workspaceCache.values()];
  for (const win of BrowserWindow.getAllWindows()) {
    if (!win.isDestroyed()) {
      win.webContents.send(DarvinPushEvent.WorkspacesChanged, workspaces);
    }
  }
}

/** 单个 session 的 workspace 导入文件列表变更后广播（import / remove 后）。 */
function broadcastWorkspaceChanged(sessionId: string): void {
  void (async () => {
    try {
      const r = await client.request<DarvinListImportedFilesResponse>(
        'agent.list_imported_files',
        { sessionId },
      );
      for (const win of BrowserWindow.getAllWindows()) {
        if (!win.isDestroyed()) {
          win.webContents.send(DarvinPushEvent.WorkspaceChanged, { sessionId, files: r.files });
        }
      }
    } catch (e) {
      console.warn(`[main] workspace changed broadcast failed: ${(e as Error).message}`);
    }
  })();
}

/**
 * 把受控 workspace 重锚到指定 workspace：更新 workspaceLoc + 建目录 +
 * 调 Go 端 agent.set_workspace 运行时重锚沙箱。相同 workspace 直接跳过。
 * 相比旧的 restartGoSubprocess，切换不再重启 Go 子进程，保留其它 session 的
 * in-memory 上下文与在途流式。
 */
async function followActiveWorkspace(workspaceId: string | null): Promise<void> {
  if (!workspaceId) {
    workspaceLoc = null;
    return;
  }
  if (workspaceLoc && workspaceLoc.workspaceId === workspaceId) return;
  const loc = await resolveWorkspaceRoot(workspaceId);
  if (!loc) {
    workspaceLoc = null;
    return;
  }
  await ensureWorkspaceRoot(loc);
  workspaceLoc = loc;
  activeWorkspaceId = workspaceId;
  if (client.isConnected()) {
    await client.setWorkspace(loc.rootPath);
  } else {
    await restartGoSubprocess(loc.rootPath);
  }
}

/**
 * 把受控 workspace 重锚到指定 session 所属的 workspace。session 可能是
 * 迁移前遗留（无 workspaceId），此时触发迁移为该 session 补建 workspace。
 */
async function followActiveWorkspaceOfSession(sessionId: string): Promise<string | null> {
  const sess = cache.sessions?.find((s) => s.id === sessionId);
  let workspaceId = sess?.workspaceId ?? null;
  if (!workspaceId && client.isConnected()) {
    await migrateLegacySessions();
    const refreshed = cache.sessions?.find((s) => s.id === sessionId);
    workspaceId = refreshed?.workspaceId ?? null;
  }
  if (workspaceId) await followActiveWorkspace(workspaceId);
  return workspaceId;
}

/** 会话删除后清空的工作区：无剩余 session 时删行 + 回收默认目录。 */
async function cleanupEmptyWorkspace(workspaceId: string): Promise<void> {
  if (!client.isConnected()) return;
  const remaining = await client.listSessions(workspaceId);
  if (remaining.sessions.length > 0) return;
  const w = workspaceCache.get(workspaceId);
  if (!w) return;
  const defaultRoot = path.join(userDataDir(), 'workspaces');
  const isDefault = path.resolve(w.rootPath).startsWith(path.resolve(defaultRoot) + path.sep);
  try {
    await client.request<DarvinDeleteWorkspaceResponse>('agent.delete_workspace', { workspaceId });
    if (isDefault) await fs.rm(w.rootPath, { recursive: true, force: true });
  } catch (e) {
    console.warn(`[main] workspace cleanup failed for ${workspaceId}: ${(e as Error).message}`);
  }
  workspaceCache.delete(workspaceId);
  if (activeWorkspaceId === workspaceId) activeWorkspaceId = null;
  broadcastWorkspacesChanged();
}

/**
 * 存量数据迁移：为没有 workspace 归属的 session 反建 workspace（目录沿用旧
 * 路径：workspace-mapping.json 自定义目录，否则 userData/workspaces/<sid>），
 * 并绑定。幂等：已迁移的 session 跳过。
 */
async function migrateLegacySessions(): Promise<void> {
  if (!client.isConnected()) return;
  const sessions = await client.listSessions();
  const legacy = sessions.sessions.filter((s) => !s.workspaceId);
  if (legacy.length === 0) return;
  const map = readWorkspaceMap();
  for (const s of legacy) {
    const mapped = map[s.id];
    const root = mapped ? path.resolve(mapped) : path.join(userDataDir(), 'workspaces', s.id);
    try {
      const created = await client.request<DarvinCreateWorkspaceResponse>('agent.create_workspace', {
        name: s.title,
        rootPath: root,
      });
      await client.request<DarvinBindSessionWorkspaceResponse>('agent.bind_session_workspace', {
        sessionId: s.id,
        workspaceId: created.workspace.id,
      });
    } catch (e) {
      console.warn(`[main] legacy session migration failed for ${s.id}: ${(e as Error).message}`);
    }
  }
  await refreshWorkspaceCache();
  await refreshSessionsAndBroadcast();
}

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
  onSessionsChanged: () => {
    void refreshSessionsAndBroadcast();
  },
  onWorkspacesChanged: () => {
    void refreshWorkspaceCache().then(() => broadcastWorkspacesChanged());
  },
});

/** 允许交给系统浏览器 / 外部 handler 的协议白名单。 */
const SAFE_EXTERNAL_PROTOCOL_RE = /^(https?|mailto|tel):/i;

function isSafeExternalUrl(url: string): boolean {
  try {
    return SAFE_EXTERNAL_PROTOCOL_RE.test(new URL(url).protocol);
  } catch {
    return false;
  }
}

/** 允许主窗口内部导航的 URL：应用自身 origin（dev server / file:）。其余一律拦截。 */
function isAllowedAppNavigation(url: string): boolean {
  if (url === 'about:blank') return true;
  if (url.startsWith('file:')) return true;
  if (MAIN_WINDOW_VITE_DEV_SERVER_URL) {
    try {
      if (url.startsWith(new URL(MAIN_WINDOW_VITE_DEV_SERVER_URL).origin)) return true;
    } catch {
      // ignore malformed dev server URL
    }
  }
  return false;
}

const LOCAL_SERVICE_PROBE_TIMEOUT_MS = 700;

/** HTTP GET 探测本地服务：要求 text/html 并尝试提取 <title>，超时/非 HTML 视为 offline。 */
async function probeLocalService(host: string, port: number): Promise<{ online: boolean; title?: string }> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), LOCAL_SERVICE_PROBE_TIMEOUT_MS);
  try {
    const res = await net.fetch(`http://${host}:${port}`, { signal: controller.signal });
    const contentType = res.headers.get('content-type') ?? '';
    if (!contentType.includes('text/html')) return { online: false };
    const body = await res.text();
    const title = body.match(/<title[^>]*>([^<]*)<\/title>/i)?.[1]?.trim();
    return { online: true, title: title || undefined };
  } catch {
    return { online: false };
  } finally {
    clearTimeout(timer);
  }
}

const createWindow = (): void => {
  const mainWindow = new BrowserWindow({
    height: 800,
    width: 1200,
    webPreferences: {
      preload: path.join(__dirname, '../preload/index.js'),
      webviewTag: true,
    },
  });

  // 主窗口导航守卫：任何点击 / window.open 都不允许把主窗口导航离开应用。
  mainWindow.webContents.setWindowOpenHandler(({ url }) => {
    if (isSafeExternalUrl(url)) void shell.openExternal(url);
    return { action: 'deny' };
  });
  mainWindow.webContents.on('will-navigate', (event, url) => {
    if (isAllowedAppNavigation(url)) return;
    event.preventDefault();
    if (isSafeExternalUrl(url)) void shell.openExternal(url);
  });
  // Browser tab 的 <webview> 加固：禁 node 能力、强制沙箱、独立 partition。
  mainWindow.webContents.on('will-attach-webview', (event, webPreferences, params) => {
    delete webPreferences.preload;
    webPreferences.nodeIntegration = false;
    webPreferences.nodeIntegrationInSubFrames = false;
    webPreferences.contextIsolation = true;
    webPreferences.sandbox = true;
    webPreferences.webSecurity = true;
    webPreferences.plugins = false;
    webPreferences.devTools = !app.isPackaged;
    webPreferences.partition = 'persist:artifact-browser';
    params.partition = 'persist:artifact-browser';
    params.allowpopups = 'false';
    if ((params.src ?? '').startsWith('javascript:')) event.preventDefault();
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
  async (_e, req?: { title?: string; workspaceId?: string; systemPrompt?: string; identity?: string; agentId?: string }): Promise<DarvinCreateSessionResponse> => {
    if (!client.isConnected()) throw new Error('agent offline');
    // 新建会话必须落在 active workspace；调用方不传时用 activeWorkspaceId。
    const workspaceId = req?.workspaceId ?? activeWorkspaceId;
    const r = await client.request<DarvinCreateSessionResponse>(
      'agent.create_session',
      { title: req?.title, workspaceId, systemPrompt: req?.systemPrompt, identity: req?.identity, agentId: req?.agentId },
    );
    cache.activeSessionId = r.session.id;
    // 重锚 workspace 到 active workspace（新会话落在那）
    await followActiveWorkspace(workspaceId);
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
    broadcastWorkspaceChanged(r.session.id);
    return r;
  },
);

ipcMain.handle(
  'darvin:list_sessions',
  async (_e, workspaceId?: string): Promise<DarvinListSessionsResponse> => {
    if (!client.isConnected()) return { sessions: cache.sessions ?? [] };
    const r = await client.listSessions(workspaceId);
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
    // 重锚 workspace 到新 active session 所属的工作区；若无归属先迁移。
    const workspaceId = await followActiveWorkspaceOfSession(r.sessionId);
    if (workspaceId && client.isConnected()) {
      try {
        await client.request<DarvinSetActiveWorkspaceResponse>('agent.set_active_workspace', { workspaceId });
      } catch (e) {
        console.warn(`[main] sync active workspace failed: ${(e as Error).message}`);
      }
    }
    await refreshSessionsAndBroadcast();
    broadcastActiveSession();
    broadcastWorkspaceChanged(r.sessionId);
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
    cache.messagesBySession.delete(sessionId);
    cache.usageBySession.delete(sessionId);
    const prevWorkspaceId = cache.sessions?.find((s) => s.id === sessionId)?.workspaceId ?? null;
    const r = await client.request<DarvinDeleteSessionResponse>(
      'agent.delete_session',
      { sessionId },
    );
    // 清理该 session 的旧自定义目录映射（workspace-mapping.json 兼容迁移）
    const map = readWorkspaceMap();
    if (map[sessionId]) {
      delete map[sessionId];
      writeWorkspaceMap(map);
    }
    cache.activeSessionId = r.nextActiveSessionId;
    if (r.nextActiveSessionId) {
      const wid = await followActiveWorkspaceOfSession(r.nextActiveSessionId);
      if (prevWorkspaceId && prevWorkspaceId !== wid) await cleanupEmptyWorkspace(prevWorkspaceId);
      broadcastWorkspaceChanged(r.nextActiveSessionId);
    } else {
      if (prevWorkspaceId) await cleanupEmptyWorkspace(prevWorkspaceId);
      workspaceLoc = null;
      activeWorkspaceId = null;
    }
    await refreshSessionsAndBroadcast();
    broadcastActiveSession();
    broadcastWorkspacesChanged();
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
  'darvin:get_session_usage',
  async (_e, sessionId: string): Promise<DarvinGetSessionUsageResponse> => {
    if (!client.isConnected()) {
      return { usage: cache.usageBySession.get(sessionId) ?? emptySessionUsage() };
    }
    const r = await client.getSessionUsage(sessionId);
    cache.usageBySession.set(sessionId, r.usage);
    return r;
  },
);

// session_usages 行缺失时返全零,renderer 用 lastUsedTokens === 0 &&
// totalPromptTokens === 0 判定"无数据"。
function emptySessionUsage(): DarvinSessionUsage {
  return {
    lastPromptTokens: 0,
    lastCompletionTokens: 0,
    lastUsedTokens: 0,
    lastCacheReadTokens: 0,
    lastCacheWriteTokens: 0,
    requestCount: 0,
    totalPromptTokens: 0,
    totalCompletionTokens: 0,
    updatedAt: 0,
  };
}

ipcMain.handle(
  'darvin:import_files',
  async (): Promise<DarvinImportFilesResponse> => {
    if (!client.isConnected()) throw new Error('agent offline');
    if (!workspaceLoc) throw new Error('workspace not ready');
    await ensureWorkspaceRoot(workspaceLoc);
    const win = BrowserWindow.getFocusedWindow() ?? BrowserWindow.getAllWindows()[0];
    if (!win) return { imported: [], skipped: [] };
    const result = await dialog.showOpenDialog(win, {
      title: '选择要导入的文件',
      properties: ['openFile', 'multiSelections'],
    });
    if (result.canceled || result.filePaths.length === 0) {
      return { imported: [], skipped: [] };
    }
    if (!workspaceLoc) return { imported: [], skipped: [] };
    const sessionId = cache.activeSessionId;
    if (!sessionId) return { imported: [], skipped: [] };
    const res = await runImport(workspaceLoc, result.filePaths, sessionId, client);
    if (res.imported.length > 0) {
      broadcastWorkspaceChanged(sessionId);
    }
    return res;
  },
);

ipcMain.handle(
  'darvin:list_imported_files',
  async (): Promise<DarvinListImportedFilesResponse> => {
    if (!workspaceLoc || !client.isConnected() || !cache.activeSessionId) return { files: [], workspaceBytes: 0 };
    return client.request<DarvinListImportedFilesResponse>('agent.list_imported_files', {
      sessionId: cache.activeSessionId,
    });
  },
);

ipcMain.handle(
  'darvin:remove_imported_file',
  async (_e, relPath: string): Promise<DarvinRemoveImportedFileResponse> => {
    if (!workspaceLoc) throw new Error('workspace not ready');
    if (!client.isConnected()) throw new Error('agent offline');
    // 防 path traversal：realpath 后必须仍在 workspace 根内。
    const realRoot = await fs.realpath(workspaceLoc.rootPath);
    const realAbs = await fs.realpath(path.join(workspaceLoc.rootPath, relPath));
    if (realAbs !== realRoot && !realAbs.startsWith(realRoot + path.sep)) {
      throw new Error('path escapes workspace');
    }
    await fs.unlink(realAbs);
    const sessionId = cache.activeSessionId;
    if (!sessionId) return { removed: false };
    const r = await client.request<DarvinRemoveImportedFileResponse>('agent.remove_imported_file', {
      sessionId,
      relPath,
    });
    try {
      await client.request('agent.save_message', {
        sessionId,
        content: `[系统] 文件已从工作区移除：${relPath}`,
        meta: { tag: 'workspace_event' },
      });
    } catch {
      /* system note 是 best-effort */
    }
    broadcastWorkspaceChanged(sessionId);
    return r;
  },
);

ipcMain.handle(
  'darvin:get_workspace_info',
  async (): Promise<DarvinWorkspaceInfoResponse> => {
    if (!workspaceLoc || !client.isConnected() || !cache.activeSessionId) {
      return { workspaceBytes: 0, label: workspaceLoc ? path.basename(workspaceLoc.rootPath) : undefined };
    }
    const r = await client.request<DarvinWorkspaceInfoResponse>('agent.get_workspace_info', {
      sessionId: cache.activeSessionId,
    });
    return { ...r, label: path.basename(workspaceLoc.rootPath) };
  },
);

ipcMain.handle('darvin:reveal_workspace', async (): Promise<void> => {
  if (!workspaceLoc) return;
  await ensureWorkspaceRoot(workspaceLoc);
  shell.showItemInFolder(workspaceLoc.rootPath);
});

ipcMain.handle(
  'darvin:list_workspace_files',
  async (): Promise<DarvinListWorkspaceFilesResponse> => {
    if (!workspaceLoc) return { files: [] };
    await ensureWorkspaceRoot(workspaceLoc);
    return { files: await walkWorkspace(workspaceLoc.rootPath) };
  },
);

ipcMain.handle(
  'darvin:read_workspace_file',
  async (_e, relativePath: string): Promise<DarvinReadWorkspaceFileResponse> => {
    if (!workspaceLoc) return { success: false, error: 'workspace not ready' };
    return readWorkspaceTextFile(workspaceLoc.rootPath, relativePath);
  },
);

ipcMain.handle('darvin:reveal_workspace_file', async (_e, relativePath: string): Promise<void> => {
  if (!workspaceLoc) return;
  const abs = await resolveWorkspacePath(workspaceLoc.rootPath, relativePath);
  if (abs) shell.showItemInFolder(abs);
});

ipcMain.handle(
  'darvin:open_workspace_file',
  async (_e, relativePath: string): Promise<DarvinOpenWorkspaceFileResponse> => {
    if (!workspaceLoc) return { success: false, error: 'workspace not ready' };
    const abs = await resolveWorkspacePath(workspaceLoc.rootPath, relativePath);
    if (!abs) return { success: false, error: 'invalid_path' };
    const err = await shell.openPath(abs);
    return err ? { success: false, error: err } : { success: true };
  },
);

ipcMain.handle(
  'darvin:open_external',
  async (_e, url: string): Promise<{ success: boolean }> => {
    if (!isSafeExternalUrl(url)) return { success: false };
    try {
      await shell.openExternal(url);
      return { success: true };
    } catch {
      return { success: false };
    }
  },
);

ipcMain.handle(
  'local_services:list',
  async (_e, urls: string[]): Promise<{ services: DarvinLocalServiceInfo[] }> => {
    const services = await Promise.all(
      urls.map(async (raw): Promise<DarvinLocalServiceInfo> => {
        let host = '';
        let port = 0;
        try {
          const u = new URL(raw);
          host = u.hostname;
          port = u.port ? Number(u.port) : 0;
        } catch {
          // invalid url → offline
        }
        if (!host || !port) return { url: raw, title: '', host, port, online: false };
        const probe = await probeLocalService(host, port);
        return { url: raw, title: probe.title ?? '', host, port, online: probe.online };
      }),
    );
    return { services };
  },
);

ipcMain.handle(
  'darvin:artifact:create_preview_session',
  async (_e, relativePath: string): Promise<DarvinCreateArtifactPreviewSessionResponse> => {
    if (!workspaceLoc) return { success: false, error: 'workspace not ready' };
    try {
      const r = await artifactPreviewServer.createPreviewSession(workspaceLoc.rootPath, relativePath);
      return { success: true, sessionId: r.sessionId, url: r.url };
    } catch (e) {
      console.warn(`[main] artifact preview session failed: ${(e as Error).message}`);
      return { success: false, error: 'preview file not found in workspace' };
    }
  },
);

ipcMain.handle(
  'darvin:artifact:destroy_preview_session',
  async (_e, sessionId: string): Promise<DarvinDestroyArtifactPreviewSessionResponse> => {
    artifactPreviewServer.destroyPreviewSession(sessionId);
    return { success: true };
  },
);

ipcMain.handle(
  'darvin:set_workspace_root',
  async (): Promise<DarvinSetWorkspaceResult> => {
    if (!client.isConnected()) throw new Error('agent offline');
    const win = BrowserWindow.getFocusedWindow() ?? BrowserWindow.getAllWindows()[0];
    if (!win) return { canceled: true, error: 'no window' };
    const result = await dialog.showOpenDialog(win, {
      title: '选择工作目录',
      properties: ['openDirectory', 'createDirectory'],
    });
    if (result.canceled || result.filePaths.length === 0) return { canceled: true };
    return setWorkspaceRootTo(result.filePaths[0]);
  },
);

ipcMain.handle(
  'darvin:set_workspace_root_to',
  async (_e, rootPath: string): Promise<DarvinSetWorkspaceResult> => {
    if (!client.isConnected()) throw new Error('agent offline');
    return setWorkspaceRootTo(rootPath);
  },
);

/**
 * 用户指定一个目录作为工作区：已是指向某个现有 workspace 的根 → 切换过去；
 * 否则在该目录新建 workspace 并设为 active。目录所有权只有一份：workspace 是
 * 目录的容器，不再存在 per-session 的"自定义工作目录映射"。
 */
async function setWorkspaceRootTo(rootPath: string): Promise<DarvinSetWorkspaceResult> {
  const abs = path.resolve(rootPath);
  await refreshWorkspaceCache();
  const existing = [...workspaceCache.values()].find(
    (w) => path.resolve(w.rootPath) === abs,
  );
  let workspaceId: string;
  if (existing) {
    workspaceId = existing.id;
  } else {
    try {
      const st = await fs.stat(abs);
      if (!st.isDirectory()) return { canceled: true, error: 'not a directory' };
    } catch {
      return { canceled: true, error: 'directory not found' };
    }
    const created = await client.request<DarvinCreateWorkspaceResponse>('agent.create_workspace', {
      rootPath: abs,
    });
    workspaceId = created.workspace.id;
    await refreshWorkspaceCache();
  }
  try {
    await client.request<DarvinSetActiveWorkspaceResponse>('agent.set_active_workspace', { workspaceId });
  } catch (e) {
    return { canceled: true, error: (e as Error).message };
  }
  await followActiveWorkspace(workspaceId);
  await refreshWorkspaceCache();
  activeWorkspaceId = workspaceId;
  broadcastWorkspacesChanged();
  return { canceled: false, rootPath: abs, label: path.basename(abs) };
}

ipcMain.handle('darvin:get_workspace_root', async (): Promise<DarvinWorkspaceRootResult> => {
  if (!workspaceLoc) return { rootPath: null, label: null };
  const w = activeWorkspaceId ? workspaceCache.get(activeWorkspaceId) : undefined;
  return { rootPath: workspaceLoc.rootPath, label: w ? w.label : path.basename(workspaceLoc.rootPath) };
});

// workspace 命名空间。Go 端是 source of truth，main 端缓存一份用于随 active
// session 跟随重锚。list 直接透传 Go；create/set-active/delete 写 Go 后刷新缓存。
ipcMain.handle('darvin:list_workspaces', async (): Promise<DarvinListWorkspacesResponse> => {
  if (!client.isConnected()) return { workspaces: [...workspaceCache.values()] };
  await refreshWorkspaceCache();
  return { workspaces: [...workspaceCache.values()] };
});

ipcMain.handle(
  'darvin:create_workspace',
  async (_e, req?: { name?: string; rootPath?: string }): Promise<DarvinCreateWorkspaceResponse> => {
    if (!client.isConnected()) throw new Error('agent offline');
    // 未指定目录时用默认工作区目录（userData/workspaces/<uuid>）兜底，保证
    // 「仅起名」也能创建；目录由 Go 端 create_workspace 幂等 mkdir。
    const rootPath = req?.rootPath ?? path.join(userDataDir(), 'workspaces', randomUUID());
    const r = await client.request<DarvinCreateWorkspaceResponse>('agent.create_workspace', {
      name: req?.name,
      rootPath,
    });
    await refreshWorkspaceCache();
    broadcastWorkspacesChanged();
    return r;
  },
);

ipcMain.handle(
  'darvin:get_active_workspace',
  async (): Promise<DarvinActiveWorkspaceResponse> => {
    if (!client.isConnected()) return { workspaceId: activeWorkspaceId };
    const r = await client.request<DarvinActiveWorkspaceResponse>('agent.get_active_workspace', {});
    activeWorkspaceId = r.workspaceId;
    return r;
  },
);

ipcMain.handle(
  'darvin:set_active_workspace',
  async (_e, workspaceId: string): Promise<DarvinSetActiveWorkspaceResponse> => {
    if (!workspaceId) {
      // 空 id（启动竞态 / 非法调用）直接回读当前 active，不触发 RPC。
      const cur = await client.request<DarvinActiveWorkspaceResponse>('agent.get_active_workspace', {});
      activeWorkspaceId = cur.workspaceId;
      return { workspaceId: cur.workspaceId ?? '', activeSessionId: null };
    }
    if (!client.isConnected()) throw new Error('agent offline');
    const r = await client.request<DarvinSetActiveWorkspaceResponse>('agent.set_active_workspace', { workspaceId });
    activeWorkspaceId = r.workspaceId;
    cache.activeSessionId = r.activeSessionId;
    await followActiveWorkspace(r.workspaceId);
    await refreshSessionsAndBroadcast();
    broadcastActiveSession();
    broadcastWorkspacesChanged();
    return r;
  },
);

ipcMain.handle(
  'darvin:delete_workspace',
  async (_e, req: { workspaceId: string; force?: boolean }): Promise<DarvinDeleteWorkspaceResponse> => {
    if (!client.isConnected()) throw new Error('agent offline');
    const w = workspaceCache.get(req.workspaceId);
    const r = await client.request<DarvinDeleteWorkspaceResponse>('agent.delete_workspace', {
      workspaceId: req.workspaceId,
      force: req.force === true,
    });
    const defaultRoot = path.join(userDataDir(), 'workspaces');
    if (w && path.resolve(w.rootPath).startsWith(path.resolve(defaultRoot) + path.sep)) {
      try {
        await fs.rm(w.rootPath, { recursive: true, force: true });
      } catch (e) {
        console.warn(`[main] workspace dir cleanup failed for ${req.workspaceId}: ${(e as Error).message}`);
      }
    }
    workspaceCache.delete(req.workspaceId);
    if (activeWorkspaceId === req.workspaceId) {
      activeWorkspaceId = r.nextActiveWorkspaceId;
      workspaceLoc = null;
      if (r.nextActiveWorkspaceId) await followActiveWorkspace(r.nextActiveWorkspaceId);
    }
    broadcastWorkspacesChanged();
    return r;
  },
);

ipcMain.handle(
  'darvin:rename_workspace',
  async (_e, req: { workspaceId: string; name: string }): Promise<DarvinCreateWorkspaceResponse> => {
    if (!client.isConnected()) throw new Error('agent offline');
    if (!req?.workspaceId || !req?.name?.trim()) {
      throw new Error('workspaceId and name are required');
    }
    const r = await client.request<DarvinCreateWorkspaceResponse>('agent.rename_workspace', {
      workspaceId: req.workspaceId,
      name: req.name.trim(),
    });
    await refreshWorkspaceCache();
    broadcastWorkspacesChanged();
    return r;
  },
);

ipcMain.handle(
  'darvin:update_workspace_root',
  async (_e, req: { workspaceId: string; rootPath: string }): Promise<DarvinCreateWorkspaceResponse> => {
    if (!client.isConnected()) throw new Error('agent offline');
    if (!req?.workspaceId || !req?.rootPath) {
      throw new Error('workspaceId and rootPath are required');
    }
    const r = await client.request<DarvinCreateWorkspaceResponse>('agent.update_workspace_root', req);
    await refreshWorkspaceCache();
    // 改目录后若是 active workspace，本地 workspaceLoc 跟 Go 端 sandbox 一起重新锚定。
    if (activeWorkspaceId === req.workspaceId) {
      const loc = await resolveWorkspaceRoot(req.workspaceId);
      if (loc) {
        workspaceLoc = loc;
        try {
          await client.setWorkspace(loc.rootPath);
        } catch (e) {
          console.warn(`[main] re-anchor after root update failed: ${(e as Error).message}`);
        }
      }
    }
    broadcastWorkspacesChanged();
    return r;
  },
);

// skills 命名空间。list 走本地缓存（SkillManager 已经是
// source of truth 的视图），set_enabled 写 SQLite + 调 Go 端
// set_enabled。失败时不回退本地缓存——main 端是 source of truth。
ipcMain.handle('darvin:list_skills', async (): Promise<DarvinListSkillsResponse> => {
  return skillManager.list();
});

ipcMain.handle(
  'darvin:set_skill_enabled',
  async (_e, req: DarvinSetSkillEnabledRequest): Promise<DarvinSetSkillEnabledResponse> => {
    if (!req || typeof req.skillId !== 'string' || typeof req.enabled !== 'boolean') {
      return { ok: false };
    }
    return skillManager.setEnabled(req);
  },
);

// install: 从用户选的本地目录复制到 <UserConfigDir>/darvin-cowork/darvin-agent/skills/
// 然后 rescan 让 chokidar + in-memory Map 一致。
ipcMain.handle(
  'darvin:install_skill',
  async (_e, req: { source: string }): Promise<DarvinInstallSkillResponse> => {
    const source = req?.source ?? '';
    if (!source) throw new Error('source required');
    const result = await installSkillFromFolder(source, getSkillsRoot());
    await skillManager.rescan();
    return result;
  },
);

ipcMain.handle(
  'darvin:uninstall_skill',
  async (_e, req: { skillId: string }): Promise<DarvinUninstallSkillResponse> => {
    if (!req?.skillId) return { ok: false };
    const removed = await uninstallSkill(req.skillId, getSkillsRoot());
    if (removed) await skillManager.rescan();
    return { ok: removed };
  },
);

ipcMain.handle(
  'darvin:upgrade_skill',
  async (_e, req: { skillId: string }): Promise<DarvinUpgradeSkillResponse> => {
    if (!req?.skillId) throw new Error('skillId required');
    const cur = await skillManager.list();
    const found = cur.skills.find((s) => s.id === req.skillId);
    if (!found) throw new Error(`skill not found: ${req.skillId}`);
    return { skill: { ...found, version: '0.0.0+stub' } };
  },
);

ipcMain.handle(
  'darvin:get_skill_details',
  async (_e, req: { skillId: string }): Promise<DarvinGetSkillDetailsResponse> => {
    if (!req?.skillId) throw new Error('skillId required');
    const cur = await skillManager.list();
    const found = cur.skills.find((s) => s.id === req.skillId);
    if (!found) throw new Error(`skill not found: ${req.skillId}`);
    return { skill: found, body: `# ${found.name}\n\n${found.description}\n` };
  },
);

// mcp 命名空间。list 走本地 mcpManager 缓存（SQLite + Go
// runtime 状态合并），其它写操作先落 SQLite 再调 Go 端对应 RPC。
// 失败不阻塞本地状态——mcpManager 内部容错,IPC 总是返回结构化响应
// 而不是 throw(便于 renderer catch 后给用户 toast)。
ipcMain.handle('mcp:list', async (): Promise<DarvinListMcpServersResponse> => {
  return { servers: mcpManager.list() };
});

// 工具面合并视图（内置 + skill + mcp）。直连 Go RPC，无本地缓存；
// renderer 拿到的是当前 session 懒建后的实时工具列表。
ipcMain.handle('tools:list', async (): Promise<DarvinListToolsResponse> => {
  return client.tools.list();
});

// Subagents artifact tab 数据源：list / messages / abort / read_result。
ipcMain.handle(
  'subagent:list',
  async (_e, parentSessionId: string): Promise<DarvinSubagentListResponse> => {
    if (!parentSessionId) throw new Error('parentSessionId required');
    return client.subagent.list(parentSessionId);
  },
);

ipcMain.handle(
  'subagent:get_messages',
  async (_e, runId: string): Promise<DarvinSubagentGetMessagesResponse> => {
    if (!runId) throw new Error('runId required');
    return client.subagent.getMessages(runId);
  },
);

ipcMain.handle(
  'subagent:abort',
  async (_e, runId: string): Promise<{ ok: boolean }> => {
    if (!runId) throw new Error('runId required');
    return client.subagent.abort(runId);
  },
);

ipcMain.handle(
  'subagent:read_result',
  async (_e, runId: string, offsetBytes: number, limitBytes: number): Promise<DarvinSubagentReadResultResponse> => {
    if (!runId) throw new Error('runId required');
    return client.subagent.readResult(runId, offsetBytes, limitBytes);
  },
);

ipcMain.handle(
  'mcp:create',
  async (_e, req: DarvinMcpServerCreate): Promise<DarvinCreateMcpServerResponse> => {
    if (!req?.name || !req.transportType) {
      throw new Error('name + transportType required');
    }
    const server = await mcpManager.createServer(req);
    return { server };
  },
);

ipcMain.handle(
  'mcp:update',
  async (
    _e,
    req: { id: string; patch: DarvinMcpServerPatch },
  ): Promise<DarvinUpdateMcpServerResponse> => {
    if (!req?.id) throw new Error('id required');
    const server = await mcpManager.updateServer(req.id, req.patch);
    if (!server) throw new Error(`mcp server not found: ${req.id}`);
    return { server };
  },
);

ipcMain.handle(
  'mcp:delete',
  async (_e, req: { id: string }): Promise<DarvinDeleteMcpServerResponse> => {
    if (!req?.id) throw new Error('id required');
    const ok = await mcpManager.deleteServer(req.id);
    return { ok };
  },
);

ipcMain.handle(
  'mcp:set_enabled',
  async (
    _e,
    req: DarvinSetMcpServerEnabledRequest,
  ): Promise<DarvinSetMcpServerEnabledResponse> => {
    if (!req?.id || typeof req.enabled !== 'boolean') {
      throw new Error('id + enabled required');
    }
    const ok = await mcpManager.setEnabled(req.id, req.enabled);
    return { ok };
  },
);

ipcMain.handle(
  'mcp:test',
  async (
    _e,
    req: DarvinTestMcpConnectionRequest,
  ): Promise<DarvinTestMcpConnectionResponse> => {
    if (!req?.id) throw new Error('id required');
    return mcpManager.testConnection(req);
  },
);

ipcMain.handle(
  'mcp:retry_resolution',
  async (
    _e,
    req: { id: string },
  ): Promise<DarvinRetryMcpLaunchResolutionResponse> => {
    if (!req?.id) throw new Error('id required');
    return mcpManager.retryResolution(req.id);
  },
);

ipcMain.handle('mcp:resources_list', async (_e, req: { id: string }) => {
  if (!req?.id) throw new Error('id required');
  return mcpManager.listResources(req.id);
});

ipcMain.handle('mcp:resource_read', async (_e, req: { id: string; uri: string }) => {
  if (!req?.id || !req?.uri) throw new Error('id and uri required');
  return mcpManager.readResource(req.id, req.uri);
});

ipcMain.handle('mcp:prompts_list', async (_e, req: { id: string }) => {
  if (!req?.id) throw new Error('id required');
  return mcpManager.listPrompts(req.id);
});

ipcMain.handle('mcp:prompt_get', async (_e, req: { id: string; name: string; arguments?: Record<string, unknown> }) => {
  if (!req?.id || !req?.name) throw new Error('id and name required');
  return mcpManager.getPrompt(req.id, req.name, req.arguments);
});

ipcMain.handle('mcp:logs_get', async (_e, req: { id: string }) => {
  if (!req?.id) throw new Error('id required');
  return mcpManager.getLogs(req.id);
});

// 图片附件 base64 读取上限（10MB 阈值）。
const MAX_READ_AS_DATA_URL_BYTES = 10 * 1024 * 1024;

/** 扩展名 → MIME；未知类型回落 application/octet-stream。 */
const MIME_BY_EXT: Record<string, string> = {
  png: 'image/png',
  jpg: 'image/jpeg',
  jpeg: 'image/jpeg',
  gif: 'image/gif',
  webp: 'image/webp',
  bmp: 'image/bmp',
  svg: 'image/svg+xml',
  pdf: 'application/pdf',
  txt: 'text/plain',
  md: 'text/markdown',
  csv: 'text/csv',
  json: 'application/json',
};

function mimeForPath(p: string): string {
  const ext = path.extname(p).toLowerCase().replace(/^\./, '');
  return MIME_BY_EXT[ext] ?? 'application/octet-stream';
}

ipcMain.handle(
  'darvin:read_file_data_url',
  async (_e, filePath: string): Promise<DarvinReadFileDataUrlResponse> => {
    try {
      // 相对路径按 workspace 根解析（write_file 等 agent 产物常用相对路径）。
      const abs = path.isAbsolute(filePath)
        ? filePath
        : workspaceLoc
          ? await resolveWorkspacePath(workspaceLoc.rootPath, filePath)
          : null;
      if (!abs) return { success: false, error: 'invalid_path' };
      const st = await fs.stat(abs);
      if (!st.isFile()) return { success: false, error: 'not a file' };
      if (st.size > MAX_READ_AS_DATA_URL_BYTES) {
        return { success: false, error: 'too_large' };
      }
      const buf = await fs.readFile(abs);
      return { success: true, dataUrl: `data:${mimeForPath(abs)};base64,${buf.toString('base64')}` };
    } catch (e) {
      return { success: false, error: (e as Error).message };
    }
  },
);

ipcMain.handle('darvin:pick_attachments', async (): Promise<DarvinPickAttachmentsResponse> => {
  if (!client.isConnected()) throw new Error('agent offline');
  const win = BrowserWindow.getFocusedWindow() ?? BrowserWindow.getAllWindows()[0];
  if (!win) return { attachments: [] };
  const result = await dialog.showOpenDialog(win, {
    title: '选择要附加的文件',
    properties: ['openFile', 'multiSelections'],
  });
  if (result.canceled || result.filePaths.length === 0) return { attachments: [] };
  const attachments: DarvinAttachmentRef[] = [];
  for (const p of result.filePaths) {
    try {
      const st = await fs.stat(p);
      if (!st.isFile()) continue;
      attachments.push({ path: p, name: path.basename(p), size: st.size });
    } catch {
      /* 不可读的文件跳过 */
    }
  }
  return { attachments };
});

ipcMain.handle('darvin:pick_skill_folder', async (): Promise<DarvinPickSkillFolderResponse> => {
  const win = BrowserWindow.getFocusedWindow() ?? BrowserWindow.getAllWindows()[0];
  if (!win) return { canceled: true };
  const result = await dialog.showOpenDialog(win, {
    title: '选择 skill 目录（包含 SKILL.md 的文件夹）',
    properties: ['openDirectory'],
  });
  if (result.canceled || result.filePaths.length === 0) return { canceled: true };
  return { canceled: false, path: result.filePaths[0] };
});

ipcMain.handle('darvin:permission_response', async (_e, req: DarvinPermissionResponse): Promise<void> => {
  if (!client.isConnected()) throw new Error('agent offline');
  await client.request('agent.permission_response', req);
});

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
        provider: req.provider,
        model: req.model,
        attachments: req.attachments,
        images: req.images,
      });
      return { sessionId, messageId: r.messageId, runId: r.runId ?? runId };
    } catch (e) {
      currentRunIdBySessionId.delete(sessionId);
      throw e;
    }
  },
);

ipcMain.handle(
  'darvin:invoke_skill',
  async (_e, req: DarvinInvokeSkillRequest): Promise<DarvinInvokeSkillResponse> => {
    let sessionId = req.sessionId;
    if (!sessionId) {
      try {
        const a = await client.request<DarvinActiveSessionResponse>('agent.get_active_session', {});
        if (a.sessionId === null) throw new Error('no active session');
        sessionId = a.sessionId;
      } catch (e) {
        if (!client.isConnected()) throw new Error('agent offline');
        throw e;
      }
    }
    const runId = randomUUID();
    currentRunIdBySessionId.set(sessionId, runId);
    try {
      const r = await client.invokeSkill({
        sessionId,
        skillId: req.skillId,
        args: req.args,
        content: req.content,
      });
      const resolvedRunId = r.runId ?? runId;
      currentRunIdBySessionId.set(sessionId, resolvedRunId);
      return { ok: r.ok, sessionId, messageId: r.messageId, runId: resolvedRunId };
    } catch (e) {
      currentRunIdBySessionId.delete(sessionId);
      throw e;
    }
  },
);

ipcMain.handle(
  'darvin:compact_context',
  async (_e, sessionId: string): Promise<DarvinCompactContextResponse> => {
    if (!client.isConnected() || !sessionId) return { accepted: false, sessionId: '' };
    try {
      return await client.request<DarvinCompactContextResponse>('agent.compact_context', { sessionId });
    } catch {
      return { accepted: false, sessionId };
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
  const providers: DarvinLLMConfig['providers'] = {};
  for (const [name, entry] of Object.entries(cfg?.providers ?? {})) {
    providers[name] = {
      apiFormat: entry?.api_format ?? '',
      apiKey: entry?.api_key ?? '',
      baseUrl: entry?.base_url ?? '',
      defaultModel: entry?.default_model ?? '',
    };
  }
  const activeProvider = cfg?.llm?.provider ?? 'anthropic';
  const active = providers[activeProvider];
  return {
    provider: activeProvider,
    activeProvider,
    apiKey: active?.apiKey ?? cfg?.llm?.api_key ?? '',
    baseUrl: active?.baseUrl ?? cfg?.llm?.base_url ?? '',
    defaultModel: active?.defaultModel ?? cfg?.llm?.default_model ?? '',
    providers,
    registeredProviders: [...new Set(DARVIN_PROVIDERS.map((p) => p.apiFormat))],
  };
});

ipcMain.handle('darvin:get_llm_models', async (): Promise<DarvinModelInfo[]> => {
  if (!client.isConnected()) return [];
  try {
    const r = await client.request<{ models: DarvinModelInfo[] }>('agent.llm.list_models', {});
    return r.models ?? [];
  } catch {
    return [];
  }
});

ipcMain.handle(
  'darvin:set_llm_config',
  async (
    _e,
    req: { provider: string; apiKey: string; baseUrl?: string; defaultModel?: string; apiFormat?: string },
  ): Promise<DarvinSetLLMConfigResponse> => {
    const preset = darvinProviderPreset(req.provider);
    if (!preset) {
      return { saved: false, restarted: false };
    }
    if (preset.apiKeyRequired && !req.apiKey) {
      return { saved: false, restarted: false };
    }
    const apiFormat = req.apiFormat || preset.apiFormat;
    // 显式 apiFormat 且该 provider 有对应端点时，base URL 自动跟随。
    let baseUrl = req.baseUrl;
    if (!baseUrl && req.apiFormat && preset.switchableBaseUrls?.[req.apiFormat as 'openai' | 'anthropic']) {
      baseUrl = preset.switchableBaseUrls[req.apiFormat as 'openai' | 'anthropic'];
    }
    baseUrl = baseUrl || preset.defaultBaseUrl;
    if (preset.requiresBaseUrl && !baseUrl) {
      return { saved: false, restarted: false };
    }
    const defaultModel = req.defaultModel || preset.defaultModels[0]?.id || '';
    // 统一激活：写 llm.provider + providers.<key>（含 api_format），并保留
    // 顶层 api_key/base_url/default_model 作为 Go 的向后兼容兜底。
    await writeUserSettingsYAML({
      llm: {
        provider: req.provider,
        api_key: req.apiKey,
        base_url: baseUrl,
        default_model: defaultModel,
      },
      providers: {
        [req.provider]: {
          api_format: apiFormat,
          api_key: req.apiKey,
          base_url: baseUrl,
          default_model: defaultModel,
        },
      },
    });
    const restarted = await restartGoSubprocess();
    return { saved: true, restarted };
  },
);

ipcMain.handle('darvin:get_app_preferences', async (): Promise<DarvinAppPreferences> => {
  const cfg = await readUserSettingsYAML();
  return {
    autoLaunch: app.getLoginItemSettings().openAtLogin,
    notifications: cfg?.app?.notifications ?? true,
    proxy: cfg?.app?.proxy ?? '',
    memory: {
      enabled: cfg?.memory?.enabled ?? false,
      embeddingProvider: cfg?.memory?.embedding_provider ?? 'openai',
      apiKey: cfg?.memory?.api_key ?? '',
    },
  };
});

ipcMain.handle(
  'darvin:set_app_preferences',
  async (_e, patch: DarvinAppPreferencesPatch): Promise<void> => {
    if (patch.autoLaunch !== undefined) {
      app.setLoginItemSettings({ openAtLogin: patch.autoLaunch });
    }
    await writeUserSettingsYAML({
      app: {
        notifications: patch.notifications,
        proxy: patch.proxy,
      },
      memory: {
        enabled: patch.memory?.enabled,
        embedding_provider: patch.memory?.embeddingProvider,
        api_key: patch.memory?.apiKey,
      },
    });
  },
);

ipcMain.handle('darvin:get_app_info', async (): Promise<DarvinAppInfo> => {
  return {
    version: app.getVersion(),
    electron: process.versions.electron,
    platform: process.platform,
    arch: process.arch,
  };
});

async function broadcastAgentsChanged(workspaceId: string): Promise<void> {
  if (!client.isConnected()) return;
  try {
    const r = await client.request<{ agents: DarvinAgent[] }>('agent.list_agents', { workspaceId });
    for (const w of BrowserWindow.getAllWindows()) {
      w.webContents.send(DarvinPushEvent.AgentsChanged, r.agents);
    }
  } catch (e) {
    console.warn(`[main] broadcast agents-changed failed: ${(e as Error).message}`);
  }
}

ipcMain.handle('agents:list', async (_e, workspaceId: string): Promise<{ agents: DarvinAgent[] }> => {
  if (!client.isConnected()) throw new Error('agent offline');
  return client.request('agent.list_agents', { workspaceId });
});

ipcMain.handle('agents:get', async (_e, agentId: string): Promise<{ agent: DarvinAgent }> => {
  if (!client.isConnected()) throw new Error('agent offline');
  return client.request('agent.get_agent', { agentId });
});

ipcMain.handle(
  'agents:create',
  async (_e, req: Parameters<DarvinApi['createAgent']>[0]): Promise<{ agent: DarvinAgent }> => {
    if (!client.isConnected()) throw new Error('agent offline');
    const r = await client.request<{ agent: DarvinAgent }>('agent.create_agent', req);
    void broadcastAgentsChanged(req.workspaceId);
    return r;
  },
);

ipcMain.handle(
  'agents:update',
  async (
    _e,
    args: { agentId: string; patch: Partial<DarvinAgent> },
  ): Promise<{ agent: DarvinAgent }> => {
    if (!client.isConnected()) throw new Error('agent offline');
    const r = await client.request<{ agent: DarvinAgent }>('agent.update_agent', {
      agentId: args.agentId,
      ...args.patch,
    });
    void broadcastAgentsChanged(r.agent.workspaceId);
    return r;
  },
);

ipcMain.handle('agents:delete', async (_e, agentId: string): Promise<{ deleted: boolean }> => {
  if (!client.isConnected()) throw new Error('agent offline');
  return client.request('agent.delete_agent', { agentId });
});

ipcMain.handle(
  'agents:update_default',
  async (_e, req: { workspaceId: string; defaultAgentId: string }) => {
    if (!client.isConnected()) throw new Error('agent offline');
    const r = await client.request<{ workspace: DarvinWorkspace }>('agent.update_default_agent', req);
    void refreshWorkspaceCache();
    void broadcastAgentsChanged(req.workspaceId);
    return r;
  },
);

ipcMain.handle(
  'darvin:get_locale',
  async (): Promise<DarvinLocaleResponse> => {
    const cfg = await readUserSettingsYAML();
    return { locale: cfg?.locale ?? 'zh' };
  },
);

const scheduleProxy = createScheduleProxy(client);
const imProxy = createIMProxy(client);

ipcMain.handle('schedule:list', async (_e, req: { workspaceId: string }) => scheduleProxy.list(req));
ipcMain.handle('schedule:get', async (_e, req: { workspaceId: string; scheduleId: string }) => scheduleProxy.get(req));
ipcMain.handle('schedule:create', async (_e, req: { workspaceId: string; schedule: Parameters<DarvinApi['scheduleCreate']>[0]['schedule'] }) => scheduleProxy.create(req));
ipcMain.handle('schedule:update', async (_e, req: Parameters<DarvinApi['scheduleUpdate']>[0]) => scheduleProxy.update(req));
ipcMain.handle('schedule:delete', async (_e, req: { workspaceId: string; scheduleId: string }) => scheduleProxy.delete(req));
ipcMain.handle('schedule:toggle', async (_e, req: Parameters<DarvinApi['scheduleToggle']>[0]) => scheduleProxy.toggle(req));
ipcMain.handle('schedule:run_now', async (_e, req: { workspaceId: string; scheduleId: string }) => scheduleProxy.runNow(req));
ipcMain.handle('schedule:abort', async (_e, req: Parameters<DarvinApi['scheduleAbort']>[0]) => scheduleProxy.abort(req));
ipcMain.handle('schedule:list_runs', async (_e, req: Parameters<DarvinApi['scheduleListRuns']>[0]) => scheduleProxy.listRuns(req));
ipcMain.handle('schedule:list_all_runs', async (_e, req: Parameters<DarvinApi['scheduleListAllRuns']>[0]) => scheduleProxy.listAllRuns(req));

ipcMain.handle('im:list', async (_e, req: { workspaceId?: string }) => imProxy.list(req));
ipcMain.handle('im:get', async (_e, req: { instanceId: string }) => imProxy.get(req));
ipcMain.handle('im:create', async (_e, req: Parameters<DarvinApi['imCreate']>[0]) => imProxy.create(req));
ipcMain.handle('im:update', async (_e, req: Parameters<DarvinApi['imUpdate']>[0]) => imProxy.update(req));
ipcMain.handle('im:delete', async (_e, req: { instanceId: string }) => imProxy.delete(req));
ipcMain.handle('im:set_enabled', async (_e, req: Parameters<DarvinApi['imSetEnabled']>[0]) => imProxy.setEnabled(req));
ipcMain.handle('im:test', async (_e, req: Parameters<DarvinApi['imTest']>[0]) => imProxy.test(req));
ipcMain.handle('im:login_start', async (_e, req: Parameters<DarvinApi['imLoginStart']>[0]) => imProxy.loginStart(req));
ipcMain.handle('im:login_poll', async (_e, req: Parameters<DarvinApi['imLoginPoll']>[0]) => imProxy.loginPoll(req));

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
 * 重启 Go 子进程：用于 set_llm_config 等需要冷启动以加载新配置的场景，也用于
 * 启动期把 DARVIN_AGENT_WORKSPACE（workspaceRoot）注入进子进程环境。
 *
 * 返回值表示是否真的拉起了一个新子进程。binary 缺失或 restart 失败
 * 时返 false，caller 决定如何 surface 给 UI（toast 即可，不阻塞写盘）。
 */
async function restartGoSubprocess(workspaceRoot?: string): Promise<boolean> {
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
    const resolved = await mgr.start(workspaceRoot);
    await client.connect(resolved.port);
    await subscribeAllSessions();
    eventRouter.start();
    // restart 后 Go 端 registry 仍保留 enabled 标志(内存)，重新 bootstrap
    // 把 main 端 SQLite 状态再覆盖一次,避免跨会话切换 workspace 时
    // 残留旧 enabled 值。
    void skillManager.bootstrap();
    void mcpManager.bootstrap();
    return true;
  } catch (e) {
    console.error(`[runtime] restart failed: ${(e as Error).message}`);
    return false;
  }
}

app.whenReady().then(async () => {
  installAppMenu();

  try {
    // 1) 起子进程（无 workspace env，Go 端 dev 兜底），用于 bootstrap。
    const resolved = await mgr.start();
    await client.connect(resolved.port);
    await subscribeAllSessions();

    // 2) workspace 清单 + 存量迁移（旧 session 无归属时反建 workspace，目录不丢）。
    await refreshWorkspaceCache();
    await migrateLegacySessions();

    // 3) 解析 active session id（作为确定 active workspace 的依据）。
    let sessionId: string | null = null;
    try {
      const active = await client.request<DarvinActiveSessionResponse>('agent.get_active_session', {});
      sessionId = active.sessionId;
    } catch {
      /* agent 未就绪 */
    }

    // 4) 解析 active workspace：优先 app_state，其次 active session 所属，再次第一个
    //    workspace。无 workspace → 空态，UI 落在工作区首屏引导创建。
    let workspaceId: string | null = null;
    try {
      const a = await client.request<DarvinActiveWorkspaceResponse>('agent.get_active_workspace', {});
      workspaceId = a.workspaceId;
    } catch {
      /* agent 未就绪 */
    }
    if (!workspaceId && sessionId) {
      const sess = cache.sessions?.find((s) => s.id === sessionId);
      workspaceId = sess?.workspaceId ?? null;
    }
    if (!workspaceId) {
      const list = await client.request<DarvinListWorkspacesResponse>('agent.list_workspaces', {});
      workspaceId = list.workspaces[0]?.id ?? null;
    }

    if (workspaceId) {
      const loc = await resolveWorkspaceRoot(workspaceId);
      if (loc) {
        activeWorkspaceId = workspaceId;
        cache.activeSessionId = sessionId;
        workspaceLoc = loc;
        await ensureWorkspaceRoot(workspaceLoc);
        // 带 DARVIN_AGENT_WORKSPACE 重启子进程，让 Go agent 沙箱根与
        // workspace 对齐（先建好目录再注入）。
        await restartGoSubprocess(workspaceLoc.rootPath);
      } else {
        eventRouter.start();
      }
    } else {
      activeWorkspaceId = null;
      workspaceLoc = null;
      eventRouter.start();
    }
    // 启动期推 skills bootstrap。restartGoSubprocess 已幂等,
    // 此处调用是首次启动路径(空 skillManager)，与 restart 路径不重复
    // 执行(SkillManager.bootstrap 内置幂等守卫)。
    void skillManager.bootstrap();
    // 启动期推 mcp bootstrap：bundled filesystem 幂等 upsert
    // + 全部 server 推 Go。restart 路径在 restartGoSubprocess 内也调一次。
    void mcpManager.bootstrap();
    broadcastWorkspacesChanged();
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
    skillManager.shutdown();
    mcpManager.shutdown();
    try {
      mcpStore.close();
    } catch {
      /* 已关闭或文件不存在 */
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
