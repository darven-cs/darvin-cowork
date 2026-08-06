/**
 * main 端 MCP server 持久化。
 *
 * 两张表：
 *   mcp_servers              server 元数据 + transport 配置（source of truth）
 *   mcp_launch_resolutions   resolver 输出（Go 端 resync 落点，cascade delete）
 *
 * 独立 SQLite 文件 `mcp.db`，与 sessions.db / skill-state.db 解耦；本类不
 * 持网络句柄——所有读 / 写都是 sync better-sqlite3 调用，main 进程内线程安全。
 */

import BetterSqlite3, { type Database as BetterSqliteDb } from 'better-sqlite3';
import fs from 'node:fs';
import path from 'node:path';
import type {
  DarvinMcpServer,
  DarvinMcpServerCreate,
  DarvinMcpServerPatch,
  DarvinMcpLaunchStatus,
  DarvinMcpTransportType,
} from '../../shared/darvin-api';
import { mcpStoreDbPath } from './user-paths';

/** Go 端 LaunchResolution 的可持久化视图。main 端不存 InstalledAt 等
 * 不可序列化字段；时间用 unix ms。 */
export interface McpLaunchResolutionRow {
  serverId: string;
  resolverKind: 'npx' | 'uvx' | 'go' | 'raw';
  sourceFingerprint: string;
  status: DarvinMcpLaunchStatus;
  packageName: string | null;
  requestedVersion: string | null;
  resolvedVersion: string | null;
  installDir: string | null;
  command: string | null;
  args: string[];
  env: Record<string, string>;
  error: string | null;
  installedAt: number | null;
  resolvedAt: number | null;
  updatedAt: number;
}

/**
 * 打开（惰性创建）main 端 mcp.db。失败抛错——启动期 hard requirement：
 *  mcp 表缺失会让 bootstrap 退化为空 list，main 选择 warn + 继续。
 */
export function openMcpStoreDb(file: string): BetterSqliteDb {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  const db = new BetterSqlite3(file);
  db.pragma('journal_mode = WAL');
  db.pragma('foreign_keys = ON');
  db.exec(`
    CREATE TABLE IF NOT EXISTS mcp_servers (
      id             TEXT PRIMARY KEY,
      name           TEXT NOT NULL,
      description    TEXT NOT NULL DEFAULT '',
      enabled        INTEGER NOT NULL DEFAULT 1,
      is_built_in    INTEGER NOT NULL DEFAULT 0,
      transport_type TEXT NOT NULL,
      config_json    TEXT NOT NULL,
      created_at     INTEGER NOT NULL,
      updated_at     INTEGER NOT NULL
    );
    CREATE INDEX IF NOT EXISTS idx_mcp_servers_enabled ON mcp_servers(enabled);

    CREATE TABLE IF NOT EXISTS mcp_launch_resolutions (
      server_id          TEXT PRIMARY KEY REFERENCES mcp_servers(id) ON DELETE CASCADE,
      resolver_kind      TEXT NOT NULL,
      source_fingerprint TEXT NOT NULL,
      status             TEXT NOT NULL,
      package_name       TEXT,
      requested_version  TEXT,
      resolved_version   TEXT,
      install_dir        TEXT,
      command            TEXT,
      args_json          TEXT NOT NULL DEFAULT '[]',
      env_json           TEXT NOT NULL DEFAULT '{}',
      error              TEXT,
      installed_at       INTEGER,
      resolved_at        INTEGER,
      updated_at         INTEGER NOT NULL
    );
  `);
  db.prepare(`DELETE FROM mcp_servers WHERE is_built_in = 1`).run();
  return db;
}

interface McpServerRow {
  id: string;
  name: string;
  description: string;
  enabled: number;
  is_built_in: number;
  transport_type: string;
  config_json: string;
  created_at: number;
  updated_at: number;
}

interface McpResolutionRow {
  server_id: string;
  resolver_kind: string;
  source_fingerprint: string;
  status: string;
  package_name: string | null;
  requested_version: string | null;
  resolved_version: string | null;
  install_dir: string | null;
  command: string | null;
  args_json: string;
  env_json: string;
  error: string | null;
  installed_at: number | null;
  resolved_at: number | null;
  updated_at: number;
}

/** sqlite 行 → renderer DarvinMcpServer。失败字段静默回落空值。 */
function rowToServer(row: McpServerRow): DarvinMcpServer {
  let cfg: Record<string, unknown> = {};
  try {
    const parsed = JSON.parse(row.config_json);
    if (parsed && typeof parsed === 'object') cfg = parsed as Record<string, unknown>;
  } catch {
    /* config_json 损坏：fallback 到空对象,不影响主字段展示 */
  }
  const get = (k: string): string | undefined => {
    const v = cfg[k];
    return typeof v === 'string' ? v : undefined;
  };
  const out: DarvinMcpServer = {
    id: row.id,
    name: row.name,
    description: row.description,
    enabled: row.enabled === 1,
    transportType: row.transport_type as DarvinMcpTransportType,
    isBuiltIn: row.is_built_in === 1,
    createdAt: row.created_at,
    updatedAt: row.updated_at,
  };
  const cmd = get('command');
  const url = get('url');
  const gh = get('githubUrl');
  const reg = get('registryId');
  if (cmd !== undefined) out.command = cmd;
  if (url !== undefined) out.url = url;
  if (gh !== undefined) out.githubUrl = gh;
  if (reg !== undefined) out.registryId = reg;
  const args = cfg.args;
  if (Array.isArray(args)) out.args = args.filter((x): x is string => typeof x === 'string');
  const env = cfg.env;
  if (env && typeof env === 'object') {
    out.env = {};
    for (const [k, v] of Object.entries(env as Record<string, unknown>)) {
      if (typeof v === 'string') out.env[k] = v;
    }
  }
  const headers = cfg.headers;
  if (headers && typeof headers === 'object') {
    out.headers = {};
    for (const [k, v] of Object.entries(headers as Record<string, unknown>)) {
      if (typeof v === 'string') out.headers[k] = v;
    }
  }
  return out;
}

function rowToResolution(row: McpResolutionRow): McpLaunchResolutionRow {
  let args: string[] = [];
  let env: Record<string, string> = {};
  try {
    const a = JSON.parse(row.args_json);
    if (Array.isArray(a)) args = a.filter((x): x is string => typeof x === 'string');
  } catch {
    /* ignore */
  }
  try {
    const e = JSON.parse(row.env_json);
    if (e && typeof e === 'object') {
      env = {};
      for (const [k, v] of Object.entries(e as Record<string, unknown>)) {
        if (typeof v === 'string') env[k] = v;
      }
    }
  } catch {
    /* ignore */
  }
  return {
    serverId: row.server_id,
    resolverKind: row.resolver_kind as McpLaunchResolutionRow['resolverKind'],
    sourceFingerprint: row.source_fingerprint,
    status: row.status as DarvinMcpLaunchStatus,
    packageName: row.package_name,
    requestedVersion: row.requested_version,
    resolvedVersion: row.resolved_version,
    installDir: row.install_dir,
    command: row.command,
    args,
    env,
    error: row.error,
    installedAt: row.installed_at,
    resolvedAt: row.resolved_at,
    updatedAt: row.updated_at,
  };
}

export interface McpStoreOptions {
  dbPath?: string;
}

/**
 * McpStore 是 main 端 mcp.db 的 thin wrapper。所有 API sync（better-sqlite3
 * 本身 sync），调用方自行决定是否包成 async。
 */
export class McpStore {
  private db: BetterSqliteDb;

  constructor(opts: McpStoreOptions = {}) {
    this.db = openMcpStoreDb(opts.dbPath ?? mcpStoreDbPath());
  }

  close(): void {
    this.db.close();
  }

  createServer(
    server: Omit<DarvinMcpServer, 'createdAt' | 'updatedAt'>,
  ): DarvinMcpServer {
    const now = Date.now();
    const config = {
      command: server.command,
      args: server.args,
      env: server.env,
      url: server.url,
      headers: server.headers,
      githubUrl: server.githubUrl,
      registryId: server.registryId,
    };
    this.db
      .prepare(
        `INSERT INTO mcp_servers
         (id, name, description, enabled, is_built_in, transport_type, config_json, created_at, updated_at)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
      )
      .run(
        server.id,
        server.name,
        server.description,
        server.enabled ? 1 : 0,
        server.isBuiltIn ? 1 : 0,
        server.transportType,
        JSON.stringify(config),
        now,
        now,
      );
    return { ...server, createdAt: now, updatedAt: now };
  }

  getServer(id: string): DarvinMcpServer | null {
    const row = this.db
      .prepare(`SELECT * FROM mcp_servers WHERE id = ?`)
      .get(id) as McpServerRow | undefined;
    return row ? rowToServer(row) : null;
  }

  listServers(): DarvinMcpServer[] {
    const rows = this.db
      .prepare(`SELECT * FROM mcp_servers ORDER BY is_built_in DESC, name ASC`)
      .all() as McpServerRow[];
    return rows.map(rowToServer);
  }

  updateServer(id: string, patch: DarvinMcpServerPatch): DarvinMcpServer | null {
    const cur = this.getServer(id);
    if (!cur) return null;
    const next: DarvinMcpServer = { ...cur };
    if (patch.name !== undefined) next.name = patch.name;
    if (patch.description !== undefined) next.description = patch.description;
    if (patch.enabled !== undefined) next.enabled = patch.enabled;
    if (patch.transportType !== undefined) next.transportType = patch.transportType;
    if (patch.command !== undefined) next.command = patch.command;
    if (patch.args !== undefined) next.args = patch.args;
    if (patch.env !== undefined) next.env = patch.env;
    if (patch.url !== undefined) next.url = patch.url;
    if (patch.headers !== undefined) next.headers = patch.headers;
    const now = Date.now();
    next.updatedAt = now;
    const config = {
      command: next.command,
      args: next.args,
      env: next.env,
      url: next.url,
      headers: next.headers,
      githubUrl: next.githubUrl,
      registryId: next.registryId,
    };
    this.db
      .prepare(
        `UPDATE mcp_servers
         SET name = ?, description = ?, enabled = ?, transport_type = ?, config_json = ?, updated_at = ?
         WHERE id = ?`,
      )
      .run(
        next.name,
        next.description,
        next.enabled ? 1 : 0,
        next.transportType,
        JSON.stringify(config),
        now,
        id,
      );
    return next;
  }

  /**
   * 删除 server：CASCADE 自动清 launch_resolutions 行；显式 SQL 二次
   * 兜底避免 foreign_keys pragma 在某些 better-sqlite3 编译选项下失效。
   */
  deleteServer(id: string): void {
    this.db.prepare(`DELETE FROM mcp_launch_resolutions WHERE server_id = ?`).run(id);
    this.db.prepare(`DELETE FROM mcp_servers WHERE id = ?`).run(id);
  }

  saveResolution(res: McpLaunchResolutionRow): void {
    this.db
      .prepare(
        `INSERT INTO mcp_launch_resolutions
         (server_id, resolver_kind, source_fingerprint, status, package_name,
          requested_version, resolved_version, install_dir, command,
          args_json, env_json, error, installed_at, resolved_at, updated_at)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
         ON CONFLICT(server_id) DO UPDATE SET
           resolver_kind = excluded.resolver_kind,
           source_fingerprint = excluded.source_fingerprint,
           status = excluded.status,
           package_name = excluded.package_name,
           requested_version = excluded.requested_version,
           resolved_version = excluded.resolved_version,
           install_dir = excluded.install_dir,
           command = excluded.command,
           args_json = excluded.args_json,
           env_json = excluded.env_json,
           error = excluded.error,
           installed_at = excluded.installed_at,
           resolved_at = excluded.resolved_at,
           updated_at = excluded.updated_at`,
      )
      .run(
        res.serverId,
        res.resolverKind,
        res.sourceFingerprint,
        res.status,
        res.packageName,
        res.requestedVersion,
        res.resolvedVersion,
        res.installDir,
        res.command,
        JSON.stringify(res.args),
        JSON.stringify(res.env),
        res.error,
        res.installedAt,
        res.resolvedAt,
        res.updatedAt,
      );
  }

  loadAllResolutions(): McpLaunchResolutionRow[] {
    const rows = this.db
      .prepare(`SELECT * FROM mcp_launch_resolutions`)
      .all() as McpResolutionRow[];
    return rows.map(rowToResolution);
  }

  deleteResolution(serverId: string): void {
    this.db
      .prepare(`DELETE FROM mcp_launch_resolutions WHERE server_id = ?`)
      .run(serverId);
  }
}

/** convenience: 把 renderer / AgentClient 入参收敛成 store 写盘字段。 */
export function buildServerFromCreate(
  id: string,
  req: DarvinMcpServerCreate,
): Omit<DarvinMcpServer, 'createdAt' | 'updatedAt'> {
  return {
    id,
    name: req.name,
    description: req.description ?? '',
    enabled: req.enabled ?? true,
    transportType: req.transportType,
    command: req.command,
    args: req.args,
    env: req.env,
    url: req.url,
    headers: req.headers,
    isBuiltIn: false,
  };
}
