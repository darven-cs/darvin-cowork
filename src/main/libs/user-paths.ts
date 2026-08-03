/**
 * 用户级数据目录。
 *
 * 布局：
 *   <userDataDir>/                        ← app.getPath('userData')，
 *   │                                       由 app.setName('darvin-cowork')
 *   │                                       决定，三平台下分别是：
 *   │                                       Linux:   ~/.config/darvin-cowork/
 *   │                                       macOS:   ~/Library/Application Support/darvin-cowork/
 *   │                                       Windows: %APPDATA%\darvin-cowork\
 *   ├── Chromium cache...                 ← Electron 自身的运行时缓存
 *   │   (Cache/ Cookies/ GPUCache/ ...)
 *   └── darvin-agent/                     ← agent 拥有的业务数据
 *       ├── config.yaml                   ← Electron 写入、Go 读合并
 *       └── sessions.db                   ← Go agent sessions.db（统一数据源）
 *
 * 与 Go 侧 config.UserDataDir() 落在同一绝对路径下；config.UserConfigPath()
 * 与 agentSessionsDsnPath() 也对齐。旧的 darvin-cowork.sqlite（Electron
 * SessionStore）已随 merge-databases refactor 删除，dev 阶段由开发者人工
 * rm 清掉（spec §4.7）。
 */
import { app } from 'electron';
import path from 'node:path';
import fs from 'node:fs';
import { readWorkspaceMap, writeWorkspaceMap } from './workspace-map';

export function userDataDir(): string {
  return app.getPath('userData');
}

export function agentDataDir(): string {
  return path.join(userDataDir(), 'darvin-agent');
}

export function userSettingsPath(): string {
  return path.join(agentDataDir(), 'config.yaml');
}

export function agentSessionsDsnPath(): string {
  return path.join(agentDataDir(), 'sessions.db');
}

/**
 * spec 32 — main 端独立 SQLite 文件，存 skills 的 enabled 状态。独立于
 * Go 端的 sessions.db，避免 Go 重启时丢 cache，也避免让 main 端再次持有
 * 业务会话数据（merge-databases refactor 之后 main 不直接写业务数据）。
 * 文件不存在时由 better-sqlite3 触发惰性 open + schema init。
 */
export function skillStateDbPath(): string {
  return path.join(agentDataDir(), 'skill-state.db');
}

/** spec 32 — user 装 skill 的根目录；chokidar 监听它。 */
export function getSkillsRoot(): string {
  return path.join(agentDataDir(), 'SKILLs');
}

/** 单 workspace 总容量上限（软上限，触发后 import 拒；不主动 GC）。 */
export const MAX_WORKSPACE_BYTES = 500 * 1024 * 1024; // 500 MiB

/** 单文件导入上限。 */
export const MAX_IMPORT_BYTES = 50 * 1024 * 1024; // 50 MiB

export interface WorkspaceLocation {
  /** 绝对路径；macOS 例: ~/Library/Application Support/darvin-cowork/workspaces/{sid}/ */
  rootPath: string;
  /** workspace 自身的 id（v0 = sessionId，一一对应）。 */
  workspaceId: string;
}

/** workspaceId = sessionId；用户自选目录存在则优先，否则退回默认 workspaces/<sid>。 */
export function resolveWorkspaceRoot(sessionId: string): WorkspaceLocation {
  const map = readWorkspaceMap();
  const mapped = map[sessionId];
  if (mapped) {
    const abs = path.resolve(mapped);
    if (fs.existsSync(abs) && fs.statSync(abs).isDirectory()) {
      return { rootPath: abs, workspaceId: sessionId };
    }
    // 映射目录已失效：清掉映射回退默认，避免后续指向不存在的根。
    delete map[sessionId];
    writeWorkspaceMap(map);
  }
  const root = path.join(userDataDir(), 'workspaces', sessionId);
  return { rootPath: root, workspaceId: sessionId };
}

export async function ensureWorkspaceRoot(loc: WorkspaceLocation): Promise<void> {
  await fs.promises.mkdir(loc.rootPath, { recursive: true });
}
