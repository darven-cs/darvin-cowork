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

import { app, BrowserWindow, dialog, ipcMain, shell } from 'electron';
import path from 'node:path';
import fs from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import { readUserSettingsYAML, writeUserSettingsYAML } from './libs/user-settings';
import { installAppMenu } from './menu';
import { RuntimeMgr, resolveAgentBinaryPath } from './runtime/manager';
import { AgentClient } from './runtime/client';
import { EventRouter } from './store/EventRouter';
import { runImport } from './libs/importFiles';
import { ensureWorkspaceRoot, resolveWorkspaceRoot, userDataDir, type WorkspaceLocation } from './libs/user-paths';
import { readWorkspaceMap, writeWorkspaceMap } from './libs/workspace-map';
import { readWorkspaceTextFile, resolveWorkspacePath, walkWorkspace } from './libs/workspaceFiles';
import { artifactPreviewServer } from './services/artifact-preview-server';
import { SkillManager } from './libs/skillManager';
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
  DarvinGetSkillDetailsResponse,
  DarvinImportFilesResponse,
  DarvinInstallSkillResponse,
  DarvinListImportedFilesResponse,
  DarvinListSessionsResponse,
  DarvinListSkillsResponse,
  DarvinListWorkspaceFilesResponse,
  DarvinOpenWorkspaceFileResponse,
  DarvinLLMConfig,
  DarvinLocale,
  DarvinModelProvider,
  DarvinLocaleResponse,
  DarvinMessage,
  DarvinPermissionResponse,
  DarvinPickAttachmentsResponse,
  DarvinPromptRequest,
  DarvinPromptResponse,
  DarvinReadFileDataUrlResponse,
  DarvinReadWorkspaceFileResponse,
  DarvinRemoveImportedFileResponse,
  DarvinRenameSessionResponse,
  DarvinRuntimeStatus,
  DarvinSearchSessionsResponse,
  DarvinSession,
  DarvinSetLLMConfigResponse,
  DarvinSetSkillEnabledRequest,
  DarvinSetSkillEnabledResponse,
  DarvinSetWorkspaceResult,
  DarvinSwitchSessionResponse,
  DarvinUninstallSkillResponse,
  DarvinUpgradeSkillResponse,
  DarvinWorkspaceInfoResponse,
  DarvinWorkspaceRootResult,
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
// spec 32 — main 端 skills 状态管理器。启动期调 bootstrap()，restart 路径
// 通过 restartGoSubprocess 重置后再 bootstrap。
const skillManager = new SkillManager({ client, logger: console });

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

/**
 * 受控 workspace 根。v0 在启动期由 active session 解析一次并保持固定，
 * Go agent 的 fsSandbox.root 通过 DARVIN_AGENT_WORKSPACE 与之对齐；所有
 * import/remove/list 都走它，避免跨 session 指向不一致导致 agent 读不到。
 */
let workspaceLoc: WorkspaceLocation | null = null;

/** workspace 内容变更后 main 端广播（import / remove 完成后调用）。 */
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
 * 把受控 workspace 重锚到指定 session：更新 workspaceLoc + 建目录 + 以新根
 * 重启 Go 子进程（重新注入 DARVIN_AGENT_WORKSPACE，agent 沙箱随会话切换）。
 * 相同 session 直接跳过（bootstrap 已锚定）。切换会话会中断其它在途流式，
 * 成本约 1s，本版本接受（后续可做 Go set_workspace RPC 消除重启）。
 */
async function followActiveWorkspace(sessionId: string): Promise<void> {
  if (workspaceLoc && workspaceLoc.workspaceId === sessionId) return;
  workspaceLoc = resolveWorkspaceRoot(sessionId);
  await ensureWorkspaceRoot(workspaceLoc);
  await restartGoSubprocess(workspaceLoc.rootPath);
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
    // 重锚 workspace 到新 session（restart 内部已 subscribeAllSessions，幂等）
    await followActiveWorkspace(r.session.id);
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
    // 重锚 workspace 到新 active session；先改 workspace 再广播，避免
    // renderer 读到中间态。
    await followActiveWorkspace(r.sessionId);
    // active 切换后顺手 touch list 的 updatedAt，让 sidebar 排序更新
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
    const r = await client.request<DarvinDeleteSessionResponse>(
      'agent.delete_session',
      { sessionId },
    );
    // 清掉该 session 的自定义工作目录映射；默认工作区目录（workspaces/<sid>）
    // 才删除磁盘目录，用户自选的真实目录绝不 rm。
    const map = readWorkspaceMap();
    if (map[sessionId]) {
      delete map[sessionId];
      writeWorkspaceMap(map);
    }
    const loc = resolveWorkspaceRoot(sessionId);
    const defaultRoot = path.join(userDataDir(), 'workspaces');
    if (path.resolve(loc.rootPath).startsWith(path.resolve(defaultRoot) + path.sep)) {
      try {
        await fs.rm(loc.rootPath, { recursive: true, force: true });
      } catch (e) {
        console.warn(`[main] workspace cleanup failed for ${sessionId}: ${(e as Error).message}`);
      }
    }
    cache.activeSessionId = r.nextActiveSessionId;
    if (r.nextActiveSessionId) {
      await followActiveWorkspace(r.nextActiveSessionId);
      broadcastWorkspaceChanged(r.nextActiveSessionId);
    } else {
      // 无会话空态：没有可跟随的 workspace
      workspaceLoc = null;
    }
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
    const sessionId = workspaceLoc.workspaceId;
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
    if (!workspaceLoc || !client.isConnected()) return { files: [], workspaceBytes: 0 };
    return client.request<DarvinListImportedFilesResponse>('agent.list_imported_files', {
      sessionId: workspaceLoc.workspaceId,
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
    const r = await client.request<DarvinRemoveImportedFileResponse>('agent.remove_imported_file', {
      sessionId: workspaceLoc.workspaceId,
      relPath,
    });
    try {
      await client.request('agent.save_message', {
        sessionId: workspaceLoc.workspaceId,
        content: `[系统] 文件已从工作区移除：${relPath}`,
        meta: { tag: 'workspace_event' },
      });
    } catch {
      /* system note 是 best-effort */
    }
    broadcastWorkspaceChanged(workspaceLoc.workspaceId);
    return r;
  },
);

ipcMain.handle(
  'darvin:get_workspace_info',
  async (): Promise<DarvinWorkspaceInfoResponse> => {
    if (!workspaceLoc || !client.isConnected()) {
      return { workspaceBytes: 0, label: workspaceLoc ? path.basename(workspaceLoc.rootPath) : undefined };
    }
    const r = await client.request<DarvinWorkspaceInfoResponse>('agent.get_workspace_info', {
      sessionId: workspaceLoc.workspaceId,
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
    if (!workspaceLoc) throw new Error('workspace not ready');
    const win = BrowserWindow.getFocusedWindow() ?? BrowserWindow.getAllWindows()[0];
    if (!win) return { canceled: true, error: 'no window' };
    const result = await dialog.showOpenDialog(win, {
      title: '选择会话工作目录',
      properties: ['openDirectory', 'createDirectory'],
    });
    if (result.canceled || result.filePaths.length === 0) return { canceled: true };
    return setWorkspaceRootTo(workspaceLoc.workspaceId, result.filePaths[0]);
  },
);

ipcMain.handle(
  'darvin:set_workspace_root_to',
  async (_e, rootPath: string): Promise<DarvinSetWorkspaceResult> => {
    if (!client.isConnected()) throw new Error('agent offline');
    if (!workspaceLoc) throw new Error('workspace not ready');
    return setWorkspaceRootTo(workspaceLoc.workspaceId, rootPath);
  },
);

/** 把当前会话工作目录重锚到指定路径：校验 → 写映射 → 以新根重启 Go 子进程。 */
async function setWorkspaceRootTo(sessionId: string, rootPath: string): Promise<DarvinSetWorkspaceResult> {
  const abs = path.resolve(rootPath);
  try {
    const st = await fs.stat(abs);
    if (!st.isDirectory()) return { canceled: true, error: 'not a directory' };
  } catch {
    return { canceled: true, error: 'directory not found' };
  }
  const map = readWorkspaceMap();
  map[sessionId] = abs;
  writeWorkspaceMap(map);
  workspaceLoc = { rootPath: abs, workspaceId: sessionId };
  await restartGoSubprocess(abs);
  broadcastWorkspaceChanged(sessionId);
  return { canceled: false, rootPath: abs, label: path.basename(abs) };
}

ipcMain.handle('darvin:get_workspace_root', async (): Promise<DarvinWorkspaceRootResult> => {
  if (!workspaceLoc) return { rootPath: null, label: null };
  return { rootPath: workspaceLoc.rootPath, label: path.basename(workspaceLoc.rootPath) };
});

// spec 32 — skills 命名空间。list 走本地缓存（SkillManager 已经是
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

// spec 33 — install / uninstall / upgrade / getDetails 全部是 v0 stub。
// 真正的「扫描 SKILL.md + 复制进 userData/darvin-agent/SKILLs + 走 fs
// watcher reload」是另一个 spec 的范围；这里只暴露 IPC 通道让 renderer
// 流程跑通，UI 上提示「未实现」。这样 SkillsView 的 install / upgrade /
// 详情 modal 可以完整联调，等 main 端真接 scanner 时只换 handler 体。
ipcMain.handle(
  'darvin:install_skill',
  async (_e, req: { source: string }): Promise<DarvinInstallSkillResponse> => {
    const source = req?.source ?? '';
    // stub:返回一个空 skill 记录让 renderer 走完「成功」分支
    return {
      skill: {
        id: `pending-${Date.now()}`,
        name: source.split('/').pop() || 'pending',
        description: `未实现：从 ${source} 装`,
        enabled: true,
        isOfficial: false,
        isBuiltIn: false,
        path: source,
        source: 'user',
        updatedAt: Date.now(),
      },
      riskLevel: 'safe',
    };
  },
);

ipcMain.handle(
  'darvin:uninstall_skill',
  async (_e, req: { skillId: string }): Promise<DarvinUninstallSkillResponse> => {
    if (!req?.skillId) return { ok: false };
    return { ok: true };
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

// spec 13 — 图片附件 base64 读取上限（对齐 LobsterAI 的 10MB 阈值）。
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
      const st = await fs.stat(filePath);
      if (!st.isFile()) return { success: false, error: 'not a file' };
      if (st.size > MAX_READ_AS_DATA_URL_BYTES) {
        return { success: false, error: 'too_large' };
      }
      const buf = await fs.readFile(filePath);
      return { success: true, dataUrl: `data:${mimeForPath(filePath)};base64,${buf.toString('base64')}` };
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
      apiKey: entry?.api_key ?? '',
      baseUrl: entry?.base_url ?? '',
      defaultModel: entry?.default_model ?? '',
    };
  }
  const activeProvider = cfg?.llm?.provider ?? 'anthropic';
  const active = providers[activeProvider];
  // yaml 里可能是任意字符串；只认 UI 支持的三个 provider，未知值回落 anthropic。
  const provider: DarvinModelProvider =
    activeProvider === 'anthropic' || activeProvider === 'openai' || activeProvider === 'custom'
      ? activeProvider
      : 'anthropic';
  return {
    provider,
    activeProvider,
    apiKey: active?.apiKey ?? cfg?.llm?.api_key ?? '',
    baseUrl: active?.baseUrl ?? cfg?.llm?.base_url ?? '',
    defaultModel: active?.defaultModel ?? cfg?.llm?.default_model ?? '',
    providers,
  };
});

ipcMain.handle(
  'darvin:set_llm_config',
  async (
    _e,
    req: { provider: string; apiKey: string; baseUrl?: string; defaultModel?: string },
  ): Promise<DarvinSetLLMConfigResponse> => {
    if (req.provider === 'anthropic') {
      // anthropic 是 Go 唯一注册的 provider：写进 llm 块并重启使新 key 生效。
      await writeUserSettingsYAML({
        llm: {
          provider: 'anthropic',
          api_key: req.apiKey,
          base_url: req.baseUrl ?? '',
          default_model: req.defaultModel ?? '',
        },
      });
      const restarted = await restartGoSubprocess();
      return { saved: true, restarted };
    }
    // openai / custom：Go 尚未接入，只把凭据存进 providers 块（不重启、不激活），
    // 避免下一轮启动时 Go 因未知 provider 直接 os.Exit(1)。
    await writeUserSettingsYAML({
      providers: {
        [req.provider]: {
          api_key: req.apiKey,
          base_url: req.baseUrl ?? '',
          default_model: req.defaultModel ?? '',
        },
      },
    });
    return { saved: true, restarted: false };
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
    return true;
  } catch (e) {
    console.error(`[runtime] restart failed: ${(e as Error).message}`);
    return false;
  }
}

app.whenReady().then(async () => {
  installAppMenu();

  try {
    // 1) 先起子进程（无 workspace env，Go 端 dev 兜底），用于 bootstrap。
    const resolved = await mgr.start();
    await client.connect(resolved.port);
    await subscribeAllSessions();

    // 2) bootstrap active session：优先取 app_state，冷启动无 session 时建一个，
    //    保证有稳定的 workspace 根。
    let sessionId: string | null = null;
    try {
      const active = await client.request<DarvinActiveSessionResponse>('agent.get_active_session', {});
      sessionId = active.sessionId;
    } catch {
      /* agent 未就绪 */
    }
    if (!sessionId) {
      try {
        const list = await client.request<DarvinListSessionsResponse>('agent.list_sessions', {});
        sessionId = list.sessions[0]?.id ?? null;
      } catch {
        /* 同上 */
      }
    }
    if (!sessionId) {
      const created = await client.request<DarvinCreateSessionResponse>('agent.create_session', {});
      sessionId = created.session.id;
    }

    if (sessionId) {
      cache.activeSessionId = sessionId;
      // 3) workspace 根 + 建目录。
      workspaceLoc = resolveWorkspaceRoot(sessionId);
      await ensureWorkspaceRoot(workspaceLoc);
      // 4) 带 DARVIN_AGENT_WORKSPACE 重启子进程，让 Go agent 沙箱根与
      //    workspace 对齐（先建好目录再注入，FR-5.1 不变量 1）。
      await restartGoSubprocess(workspaceLoc.rootPath);
    } else {
      eventRouter.start();
    }
    // spec 32 — 启动期推 skills bootstrap。restartGoSubprocess 已幂等,
    // 此处调用是首次启动路径(空 skillManager)，与 restart 路径不重复
    // 执行(SkillManager.bootstrap 内置幂等守卫)。
    void skillManager.bootstrap();
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
