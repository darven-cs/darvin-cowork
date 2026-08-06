/**
 * skills 的 main 端状态管理器。
 *
 * 职责：
 * 1. 启动期：扫 userData/SKILLs/**\/SKILL.md → 合并 SQLite 里的 enabled
 *    状态 → 调 `client.skills.bootstrap` 把初始列表推给 Go agent →
 *    订阅 Go 的 `agent.skills.changed` 通知 → 启动 chokidar 监听 user 目录。
 * 2. 运行时：响应 renderer 的 list / setEnabled；写 SQLite（source of
 *    truth）→ 调 Go 端 setEnabled → 乐观更新本地缓存 → 广播 IPC。
 * 3. 兜底：chokidar 的 add / change / unlink 都触发 reloadFromDisk，
 *    然后再把新列表推给 Go 端。
 *
 * frontmatter 解析用手写 regex（不引 js-yaml），与 `user-settings.ts`
 * 风格保持一致；main 端不参与安全扫描——扫描在 Go 端做。
 *
 * 数据所有权：main 端 SQLite 是 enabled 状态的 source of truth；Go 端
 * registry 是 prompt 期的 source of truth。两者通过 bootstrap /
 * setEnabled / changed 三路同步。
 */

import { app, BrowserWindow } from 'electron';
import { watch as chokidarWatch, type FSWatcher } from 'chokidar';
import BetterSqlite3, { type Database as BetterSqliteDb } from 'better-sqlite3';
import fs from 'node:fs';
import fsp from 'node:fs/promises';
import path from 'node:path';
import type {
  DarvinListSkillsResponse,
  DarvinSetSkillEnabledRequest,
  DarvinSkillSummary,
} from '../../shared/darvin-api';
import { DarvinPushEvent } from '../../shared/darvin-api';
import { getSkillsRoot, skillStateDbPath } from './user-paths';
import type { AgentClient } from '../runtime/client';

interface Logger {
  warn(msg: string, ...args: unknown[]): void;
  info(msg: string, ...args: unknown[]): void;
}

/** Bundled skill 的元数据；SKILL.md 正文在 Go 端 embed 解析。 */
const BUNDLED_SKILLS: ReadonlyArray<{
  id: string;
  name: string;
  description: string;
  version: string;
  userInvocable: boolean;
}> = [
  { id: 'code-review', name: 'Code Review', description: '对代码做静态审查并给出修改建议', version: '0.1.0', userInvocable: true },
  { id: 'api-design', name: 'API Design', description: '检查 REST API 设计规范（命名 / 状态码 / 错误处理）', version: '0.1.0', userInvocable: true },
  { id: 'testing', name: 'Testing', description: '给出单元测试覆盖建议', version: '0.1.0', userInvocable: true },
  { id: 'web-search', name: 'Web Search', description: '联网搜索最新信息', version: '0.1.0', userInvocable: true },
  { id: 'docx', name: 'Word Document', description: '创建 / 修改 Word 文档', version: '0.1.0', userInvocable: true },
];

/**
 * 打开（或惰性创建）main 端 skill_state 表。失败抛错，让 caller 决定
 * 是否 surface 给用户——skills bootstrap 的失败不阻塞主进程启动。
 */
function openSkillStateDb(file: string): BetterSqliteDb {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  const db = new BetterSqlite3(file);
  db.pragma('journal_mode = WAL');
  db.exec(`
    CREATE TABLE IF NOT EXISTS skill_state (
      skill_id   TEXT PRIMARY KEY,
      enabled    INTEGER NOT NULL DEFAULT 1,
      updated_at INTEGER NOT NULL
    );
  `);
  return db;
}

/**
 * 极简 frontmatter 解析：识别 `^---\n...\n---\n` 块；块内逐行读
 * `key: value`，不引 yaml 库。空格和空行容忍；字段缺失返 null。
 */
export function parseSkillFrontmatter(raw: string): {
  name: string;
  description: string;
  version?: string;
  invocation?: { userInvocable?: boolean; disableModelInvocation?: boolean };
} | null {
  const start = raw.indexOf('---\n');
  if (start !== 0) return null;
  const end = raw.indexOf('\n---', 4);
  if (end < 0) return null;
  const block = raw.slice(4, end);
  const out: Record<string, unknown> = {};
  const inv: { userInvocable?: boolean; disableModelInvocation?: boolean } = {};
  let inInvocation = false;
  for (const line of block.split('\n')) {
    const trimmed = line.trimEnd();
    if (!trimmed.trim()) continue;
    const m = /^(\s*)([\w.-]+):\s*(.*)$/.exec(trimmed);
    if (!m) continue;
    const indent = m[1].length;
    const key = m[2];
    const val = m[3].trim();
    if (indent === 0) {
      inInvocation = key === 'invocation';
      if (inInvocation) continue;
      out[key] = val.replace(/^"(.*)"$/, '$1');
    } else if (inInvocation && (key === 'userInvocable' || key === 'disableModelInvocation')) {
      if (val === 'true') inv[key] = true;
      else if (val === 'false') inv[key] = false;
    }
  }
  const name = typeof out.name === 'string' ? out.name.trim() : '';
  const description = typeof out.description === 'string' ? out.description.trim() : '';
  if (!name || !description) return null;
  if (!/^[a-z0-9][a-z0-9-]{0,63}$/.test(name)) return null;
  const result: {
    name: string;
    description: string;
    version?: string;
    invocation?: { userInvocable?: boolean; disableModelInvocation?: boolean };
  } = { name, description };
  if (typeof out.version === 'string' && out.version) result.version = out.version;
  if (Object.keys(inv).length > 0) result.invocation = inv;
  return result;
}

/**
 * 把 chokidar 监听到的 user 目录 SKILL.md 解析成 DarvinSkillSummary。
 * 不存在的目录 / 解析失败的 SKILL.md 静默跳过。
 */
async function readUserSkills(root: string): Promise<DarvinSkillSummary[]> {
  const out: DarvinSkillSummary[] = [];
  let entries: string[];
  try {
    entries = await fsp.readdir(root);
  } catch {
    return out;
  }
  for (const sub of entries) {
    if (sub.startsWith('.')) continue;
    const skillMd = path.join(root, sub, 'SKILL.md');
    try {
      const raw = await fsp.readFile(skillMd, 'utf8');
      const fm = parseSkillFrontmatter(raw);
      if (!fm) continue;
      const st = await fsp.stat(skillMd);
      out.push({
        id: fm.name,
        name: fm.name,
        description: fm.description,
        version: fm.version,
        enabled: true,
        // 与 Go loader 的默认一致：frontmatter 未声明 invocation.userInvocable 时不可手动触发。
        userInvocable: fm.invocation?.userInvocable ?? false,
        isOfficial: false,
        isBuiltIn: false,
        path: skillMd,
        source: 'user',
        updatedAt: st.mtimeMs,
      });
    } catch {
      // 文件不存在 / 不可读 → 跳过
    }
  }
  return out;
}

function bundledSummaries(): DarvinSkillSummary[] {
  return BUNDLED_SKILLS.map((b) => ({
    id: b.id,
    name: b.name,
    description: b.description,
    version: b.version,
    enabled: true,
    userInvocable: b.userInvocable,
    isOfficial: true,
    isBuiltIn: true,
    path: `bundled://${b.id}`,
    source: 'bundled',
    updatedAt: 0,
  }));
}

export interface SkillManagerOptions {
  client: AgentClient;
  logger?: Logger;
  /** 监听目录；默认 userData/SKILLs。 */
  root?: string;
  /** SQLite 文件；默认 userData/darvin-agent/skill-state.db。 */
  dbPath?: string;
}

/**
 * SkillManager 是 main 端 skills 的运行时 handle。构造时立刻开
 * SQLite、起 fs watcher，await `bootstrap()` 等同于「首次 reload +
 * 推 Go + 订阅 changed 通知」三步都跑完。`shutdown()` 关 watcher 与
 * 解除 listener。
 */
export class SkillManager {
  private client: AgentClient;
  private logger: Logger;
  private root: string;
  private db: Database.Database;
  private skills: Map<string, DarvinSkillSummary> = new Map();
  private watcher?: FSWatcher;
  private offClient?: () => void;
  private bootstrapped = false;
  private syncing = false;

  constructor(opts: SkillManagerOptions) {
    this.client = opts.client;
    this.logger = opts.logger ?? console;
    this.root = opts.root ?? getSkillsRoot();
    this.db = openSkillStateDb(opts.dbPath ?? skillStateDbPath());
  }

  /** bootstrap 三步：扫磁盘 → 推 Go → 订阅 changed。幂等，重复调用 noop。 */
  async bootstrap(): Promise<void> {
    if (this.bootstrapped) return;
    this.bootstrapped = true;
    await this.ensureSkillsDir();
    await this.reloadFromDisk();
    await this.pushBootstrap();
    this.startWatcher();
    this.subscribeClient();
  }

  async list(): Promise<DarvinListSkillsResponse> {
    return { skills: Array.from(this.skills.values()) };
  }

  async setEnabled(req: DarvinSetSkillEnabledRequest): Promise<{ ok: boolean }> {
    const enabled = req.enabled ? 1 : 0;
    const updatedAt = Date.now();
    this.db
      .prepare(
        `INSERT INTO skill_state (skill_id, enabled, updated_at) VALUES (?, ?, ?)
         ON CONFLICT(skill_id) DO UPDATE SET enabled = excluded.enabled, updated_at = excluded.updated_at`,
      )
      .run(req.skillId, enabled, updatedAt);
    try {
      await this.client.skills.setEnabled({ skillId: req.skillId, enabled: req.enabled });
    } catch (e) {
      this.logger.warn(`[skillManager] setEnabled RPC failed: ${(e as Error).message}`);
    }
    const cur = this.skills.get(req.skillId);
    if (cur) {
      cur.enabled = req.enabled;
      this.broadcast();
    }
    return { ok: true };
  }

  shutdown(): void {
    this.offClient?.();
    this.offClient = undefined;
    void this.watcher?.close();
    this.watcher = undefined;
    this.db.close();
  }

  private async ensureSkillsDir(): Promise<void> {
    await fsp.mkdir(this.root, { recursive: true });
  }

  private async reloadFromDisk(): Promise<void> {
    const next = new Map<string, DarvinSkillSummary>();
    for (const s of bundledSummaries()) {
      next.set(s.id, s);
    }
    const user = await readUserSkills(this.root);
    for (const s of user) {
      // user 同 id 覆盖 bundled
      next.set(s.id, s);
    }
    const states = this.db
      .prepare(`SELECT skill_id, enabled, updated_at FROM skill_state`)
      .all() as Array<{ skill_id: string; enabled: number; updated_at: number }>;
    for (const row of states) {
      const cur = next.get(row.skill_id);
      if (cur) {
        cur.enabled = row.enabled === 1;
      }
    }
    this.skills = next;
  }

  private async pushBootstrap(): Promise<void> {
    if (!this.client.isConnected()) return;
    try {
      await this.client.skills.bootstrap({ skills: Array.from(this.skills.values()) });
    } catch (e) {
      this.logger.warn(`[skillManager] bootstrap RPC failed: ${(e as Error).message}`);
    }
  }

  private startWatcher(): void {
    // 注意：`ignored` 不能用 regex 形式 `/(^|[\\/])\..//`，因为该 pattern
    // 会匹配祖先路径里的 `.config` / `.cache` 等段（例如 userData 路径
    // `/home/<u>/.config/darvin-cowork/.../SKILLs` 本身就含 `/.config`），
    // 一旦 chokidar 判定 root 被忽略，整个子树都不会被监听。改用函数形式
    // 显式只对 root 内部的 basename 做 dotfile 判定。
    this.watcher = chokidarWatch(this.root, {
      depth: 2,
      ignoreInitial: true,
      awaitWriteFinish: { stabilityThreshold: 400, pollInterval: 100 },
      ignored: (p: string) => {
        if (p === this.root) return false;
        const base = path.basename(p);
        return base.startsWith('.');
      },
    });
    const onChange = (): void => {
      this.scheduleSync();
    };
    this.watcher.on('add', onChange);
    this.watcher.on('change', onChange);
    this.watcher.on('unlink', onChange);
    this.watcher.on('error', (e) => this.logger.warn(`[skillManager] watcher error: ${(e as Error).message}`));
  }

  /**
   * fs 事件是高频信号；用 microtask 合并相邻事件，避免连发 reload +
   * 多次推 Go。
   */
  private scheduleSync(): void {
    if (this.syncing) return;
    this.syncing = true;
    queueMicrotask(async () => {
      try {
        await this.reloadFromDisk();
        await this.pushBootstrap();
        this.broadcast();
      } catch (e) {
        this.logger.warn(`[skillManager] sync failed: ${(e as Error).message}`);
      } finally {
        this.syncing = false;
      }
    });
  }

  private subscribeClient(): void {
    this.offClient = this.client.skills.onChanged((skills) => {
      for (const s of skills) {
        this.skills.set(s.id, s);
      }
      this.broadcast();
    });
  }

  private broadcast(): void {
    for (const win of BrowserWindow.getAllWindows()) {
      if (win.isDestroyed()) continue;
      win.webContents.send(DarvinPushEvent.SkillsChanged, Array.from(this.skills.values()));
    }
    // app 还没创建窗口时 this.skills 已就绪；broadcast() 自身仅 push 给
    // 已有 webContents，renderer 会在首次 listSkills() 时拿到完整列表。
    void app;
  }
}
