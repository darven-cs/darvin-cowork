/**
 * spec 36 — main 端 MCP 状态管理器。
 *
 * 职责：
 * 1. 启动期：ensure bundled filesystem → 读 SQLite servers → 推 Go
 *    `agent.mcp.bootstrap` → 订阅 `mcp.connection_changed` /
 *    `mcp.resolution_changed` → 启动 bundled server 同步。
 * 2. 运行时：list / create / update / delete / setEnabled 写 SQLite 后
 *    调 Go 端对应 RPC；test / retryResolution 直通 Go 端。
 * 3. 通知：connection_changed 更新 in-memory connectionStatus，触发
 *    `onMcpServersChanged` push；resolution_changed 落 SQLite，触发
 *    `onMcpServersChanged` push（launchStatus / launchError 反映在
 *    server 视图里）。
 *
 * 数据所有权：main 端 SQLite 是 server 元数据 / launch resolution 的
 * source of truth；Go 端 registry 是运行时连接状态（connected / tools
 * 列表）的 source of truth。两者通过 bootstrap / register / set_enabled
 * / unregister / update / notification 双向同步。
 */

import { app, BrowserWindow } from 'electron';
import { randomUUID } from 'node:crypto';
import os from 'node:os';
import path from 'node:path';
import type {
  DarvinMcpConnectionChangedEvent,
  DarvinMcpConnectionStatus,
  DarvinMcpLaunchResolution,
  DarvinMcpResolutionChangedEvent,
  DarvinMcpServer,
  DarvinMcpServerCreate,
  DarvinMcpServerPatch,
  DarvinTestMcpConnectionRequest,
  DarvinTestMcpConnectionResponse,
} from '../../shared/darvin-api';
import { DarvinPushEvent } from '../../shared/darvin-api';
import type { AgentClient } from '../runtime/client';
import { McpStore, buildServerFromCreate, type McpLaunchResolutionRow } from './mcpStore';

interface Logger {
  warn(msg: string, ...args: unknown[]): void;
  info(msg: string, ...args: unknown[]): void;
}

export interface McpManagerOptions {
  client: AgentClient;
  store: McpStore;
  logger?: Logger;
}

/**
 * bundled filesystem server 的固定 schema：跟着 darvin-agent 二进制，
 * 永远 enabled + isBuiltIn=true，user 不能改。id 固定 `filesystem`。
 */
function bundledFilesystemSpec(): Omit<DarvinMcpServer, 'createdAt' | 'updatedAt'> {
  return {
    id: 'filesystem',
    name: 'Filesystem',
    description: '本地文件系统读写（bundled）',
    enabled: true,
    transportType: 'stdio',
    command: process.execPath, // darwin-agent 二进制自己;spec 36 FR-9
    args: ['mcp-filesystem'],
    isBuiltIn: true,
  };
}

export class McpManager {
  private client: AgentClient;
  private store: McpStore;
  private logger: Logger;
  private servers: Map<string, DarvinMcpServer> = new Map();
  private bootstrapped = false;
  private offConnection?: () => void;
  private offResolution?: () => void;

  constructor(opts: McpManagerOptions) {
    this.client = opts.client;
    this.store = opts.store;
    this.logger = opts.logger ?? console;
  }

  /**
   * 启动期幂等：确保 bundled filesystem 落 SQLite → 读所有 server →
   * 推 Go bootstrap → 订阅两条通知。重复调用 noop。
   */
  async bootstrap(): Promise<void> {
    if (this.bootstrapped) return;
    this.bootstrapped = true;

    this.store.upsertBundledServer(bundledFilesystemSpec());
    const list = this.store.listServers();
    this.servers = new Map(list.map((s) => [s.id, s]));

    if (this.client.isConnected()) {
      try {
        await this.client.mcp.bootstrap({ servers: list });
        this.logger.info(`[mcp] bootstrap ${list.length} server(s)`);
      } catch (e) {
        this.logger.warn(`[mcp] bootstrap RPC failed: ${(e as Error).message}`);
      }
      this.offConnection = this.client.mcp.onConnectionChanged((e) => this.handleConnectionChanged(e));
      this.offResolution = this.client.mcp.onResolutionChanged((e) => this.handleResolutionChanged(e));
    }
  }

  list(): DarvinMcpServer[] {
    return Array.from(this.servers.values());
  }

  async createServer(req: DarvinMcpServerCreate): Promise<DarvinMcpServer> {
    const id = `mcp_${randomUUID()}`;
    const server = this.store.createServer(buildServerFromCreate(id, req));
    this.servers.set(id, server);
    if (this.client.isConnected()) {
      try {
        await this.client.mcp.register({ server });
      } catch (e) {
        this.logger.warn(`[mcp] register RPC failed: ${(e as Error).message}`);
      }
    }
    this.broadcastServers();
    return server;
  }

  async updateServer(id: string, patch: DarvinMcpServerPatch): Promise<DarvinMcpServer | null> {
    const updated = this.store.updateServer(id, patch);
    if (!updated) return null;
    this.servers.set(id, updated);
    if (this.client.isConnected()) {
      try {
        await this.client.mcp.update({ id, patch });
      } catch (e) {
        this.logger.warn(`[mcp] update RPC failed: ${(e as Error).message}`);
      }
    }
    this.broadcastServers();
    return updated;
  }

  async deleteServer(id: string): Promise<boolean> {
    if (!this.store.getServer(id)) return false;
    this.store.deleteServer(id);
    this.servers.delete(id);
    if (this.client.isConnected()) {
      try {
        await this.client.mcp.unregister({ id });
      } catch (e) {
        this.logger.warn(`[mcp] unregister RPC failed: ${(e as Error).message}`);
      }
    }
    this.broadcastServers();
    return true;
  }

  async setEnabled(id: string, enabled: boolean): Promise<boolean> {
    const updated = this.store.updateServer(id, { enabled });
    if (!updated) return false;
    this.servers.set(id, updated);
    if (this.client.isConnected()) {
      try {
        await this.client.mcp.setEnabled({ id, enabled });
      } catch (e) {
        this.logger.warn(`[mcp] setEnabled RPC failed: ${(e as Error).message}`);
      }
    }
    this.broadcastServers();
    return true;
  }

  async testConnection(req: DarvinTestMcpConnectionRequest): Promise<DarvinTestMcpConnectionResponse> {
    if (!this.client.isConnected()) {
      return { ok: false, error: 'agent offline' };
    }
    return this.client.mcp.test(req);
  }

  async retryResolution(id: string): Promise<{ ok: boolean }> {
    if (!this.client.isConnected()) return { ok: false };
    return this.client.mcp.retryResolution({ id });
  }

  shutdown(): void {
    this.offConnection?.();
    this.offConnection = undefined;
    this.offResolution?.();
    this.offResolution = undefined;
  }

  private handleConnectionChanged(e: DarvinMcpConnectionChangedEvent): void {
    const s = this.servers.get(e.id);
    if (!s) return;
    s.connectionStatus = e.status as DarvinMcpConnectionStatus;
    s.connectionError = e.error;
    this.broadcastConnection(e);
    this.broadcastServers();
  }

  private handleResolutionChanged(e: DarvinMcpResolutionChangedEvent): void {
    const row = resolutionToRow(e.resolution);
    try {
      this.store.saveResolution(row);
    } catch (err) {
      this.logger.warn(`[mcp] saveResolution failed: ${(err as Error).message}`);
      return;
    }
    const s = this.servers.get(e.serverId);
    if (s) {
      s.launchStatus = e.resolution.status;
      s.launchError = e.resolution.error ?? undefined;
      this.broadcastServers();
    }
  }

  private broadcastServers(): void {
    for (const win of BrowserWindow.getAllWindows()) {
      if (win.isDestroyed()) continue;
      win.webContents.send(DarvinPushEvent.McpServersChanged, Array.from(this.servers.values()));
    }
    void app;
  }

  private broadcastConnection(e: DarvinMcpConnectionChangedEvent): void {
    for (const win of BrowserWindow.getAllWindows()) {
      if (win.isDestroyed()) continue;
      win.webContents.send(DarvinPushEvent.McpConnectionChanged, e);
    }
  }
}

/** IPC wire 形态 → mcpStore 入参形态。字段一一对齐;env / args 容错。 */
function resolutionToRow(r: DarvinMcpLaunchResolution): McpLaunchResolutionRow {
  return {
    serverId: r.serverId,
    resolverKind: r.resolverKind,
    sourceFingerprint: r.sourceFingerprint,
    status: r.status,
    packageName: r.packageName,
    requestedVersion: r.requestedVersion,
    resolvedVersion: r.resolvedVersion,
    installDir: r.installDir,
    command: r.command,
    args: r.args,
    env: r.env,
    error: r.error,
    installedAt: r.installedAt,
    resolvedAt: r.resolvedAt,
    updatedAt: r.updatedAt,
  };
}

/** 测试 helper:platform-aware 临时 SQLite 路径。 */
export function tmpMcpStorePath(label = 'mcp'): string {
  return path.join(os.tmpdir(), `darvin-${label}-${process.pid}-${Date.now()}.db`);
}
