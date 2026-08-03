/**
 * mcpManager 单测。AgentClient 走最小 fake(EventEmitter-based)避免拉 WS；
 * electron 模块整体 mock 以让 broadcastServers 静默 noop(无 BrowserWindow)。
 */
import { describe, expect, it, beforeEach, vi } from 'vitest';
import { EventEmitter } from 'node:events';
import path from 'node:path';
import fs from 'node:fs';
import os from 'node:os';

vi.mock('electron', () => ({
  app: { on: () => undefined },
  BrowserWindow: { getAllWindows: () => [] },
}));

import { McpManager } from './mcpManager';
import { McpStore } from './mcpStore';
import type {
  DarvinMcpConnectionChangedEvent,
  DarvinMcpResolutionChangedEvent,
} from '../../shared/darvin-api';
import type { AgentClient } from '../runtime/client';

interface FakeAgentClient extends EventEmitter {
  isConnected(): boolean;
  mcp: {
    bootstrap: ReturnType<typeof vi.fn>;
    register: ReturnType<typeof vi.fn>;
    update: ReturnType<typeof vi.fn>;
    unregister: ReturnType<typeof vi.fn>;
    setEnabled: ReturnType<typeof vi.fn>;
    test: ReturnType<typeof vi.fn>;
    retryResolution: ReturnType<typeof vi.fn>;
    onConnectionChanged: (cb: (e: DarvinMcpConnectionChangedEvent) => void) => () => void;
    onResolutionChanged: (cb: (e: DarvinMcpResolutionChangedEvent) => void) => () => void;
  };
  __emitConn: (e: DarvinMcpConnectionChangedEvent) => void;
  __emitRes: (e: DarvinMcpResolutionChangedEvent) => void;
}

function makeFakeClient(opts: { connected?: boolean } = {}): FakeAgentClient {
  const bus = new EventEmitter();
  const client = new EventEmitter() as FakeAgentClient;
  client.isConnected = () => opts.connected ?? true;
  client.mcp = {
    bootstrap: vi.fn().mockResolvedValue({ ok: true }),
    register: vi.fn().mockResolvedValue({ ok: true }),
    update: vi.fn().mockResolvedValue({ ok: true }),
    unregister: vi.fn().mockResolvedValue({ ok: true }),
    setEnabled: vi.fn().mockResolvedValue({ ok: true }),
    test: vi.fn().mockResolvedValue({ ok: true, tools: [] }),
    retryResolution: vi.fn().mockResolvedValue({ ok: true }),
    onConnectionChanged: (cb) => {
      bus.on('connection', cb);
      return () => bus.off('connection', cb);
    },
    onResolutionChanged: (cb) => {
      bus.on('resolution', cb);
      return () => bus.off('resolution', cb);
    },
  };
  client.__emitConn = (e) => bus.emit('connection', e);
  client.__emitRes = (e) => bus.emit('resolution', e);
  return client;
}

let tmpDir: string;
let store: McpStore;
let client: FakeAgentClient;
let mgr: McpManager;

beforeEach(() => {
  tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'mcp-mgr-'));
  store = new McpStore({ dbPath: path.join(tmpDir, 'mcp.db') });
  client = makeFakeClient();
  mgr = new McpManager({ client: client as unknown as AgentClient, store });
});

describe('mcpManager.bootstrap', () => {
  it('ensures bundled filesystem + pushes all servers to Go', async () => {
    await mgr.bootstrap();
    const list = mgr.list();
    expect(list.find((s) => s.id === 'filesystem')).toBeDefined();
    expect(client.mcp.bootstrap).toHaveBeenCalledOnce();
    const arg = (client.mcp.bootstrap as ReturnType<typeof vi.fn>).mock.calls[0][0];
    expect(arg.servers.length).toBeGreaterThan(0);
  });

  it('is idempotent across repeat calls', async () => {
    await mgr.bootstrap();
    await mgr.bootstrap();
    expect(client.mcp.bootstrap).toHaveBeenCalledOnce();
  });

  it('skips Go push when agent offline but still seeds bundled server', async () => {
    client.isConnected = () => false;
    await mgr.bootstrap();
    expect(client.mcp.bootstrap).not.toHaveBeenCalled();
    expect(mgr.list().find((s) => s.id === 'filesystem')).toBeDefined();
  });
});

describe('mcpManager CRUD', () => {
  it('createServer assigns uuid, persists, calls Go register', async () => {
    await mgr.bootstrap();
    const before = mgr.list().length;
    (client.mcp.register as ReturnType<typeof vi.fn>).mockClear();
    const created = await mgr.createServer({ name: 'github', transportType: 'stdio', command: 'npx', args: ['-y', 'pkg'] });
    expect(created.id.startsWith('mcp_')).toBe(true);
    expect(store.getServer(created.id)).toEqual(created);
    expect(client.mcp.register).toHaveBeenCalledOnce();
    expect(mgr.list().length).toBe(before + 1);
  });

  it('updateServer patches + calls Go update', async () => {
    await mgr.bootstrap();
    const created = await mgr.createServer({ name: 'a', transportType: 'stdio' });
    (client.mcp.update as ReturnType<typeof vi.fn>).mockClear();
    const updated = await mgr.updateServer(created.id, { name: 'b', enabled: false });
    expect(updated?.name).toBe('b');
    expect(updated?.enabled).toBe(false);
    expect(client.mcp.update).toHaveBeenCalledWith({ id: created.id, patch: { name: 'b', enabled: false } });
  });

  it('updateServer returns null for unknown id', async () => {
    await mgr.bootstrap();
    expect(await mgr.updateServer('nope', { name: 'x' })).toBeNull();
  });

  it('deleteServer removes + calls Go unregister', async () => {
    await mgr.bootstrap();
    const created = await mgr.createServer({ name: 'a', transportType: 'stdio' });
    (client.mcp.unregister as ReturnType<typeof vi.fn>).mockClear();
    expect(await mgr.deleteServer(created.id)).toBe(true);
    expect(mgr.list().find((s) => s.id === created.id)).toBeUndefined();
    expect(client.mcp.unregister).toHaveBeenCalledWith({ id: created.id });
  });

  it('deleteServer returns false for unknown id', async () => {
    await mgr.bootstrap();
    expect(await mgr.deleteServer('nope')).toBe(false);
  });

  it('setEnabled toggles + calls Go setEnabled', async () => {
    await mgr.bootstrap();
    const created = await mgr.createServer({ name: 'a', transportType: 'stdio' });
    (client.mcp.setEnabled as ReturnType<typeof vi.fn>).mockClear();
    expect(await mgr.setEnabled(created.id, false)).toBe(true);
    expect(store.getServer(created.id)?.enabled).toBe(false);
    expect(client.mcp.setEnabled).toHaveBeenCalledWith({ id: created.id, enabled: false });
  });
});

describe('mcpManager notifications', () => {
  it('connection_changed updates in-memory', async () => {
    await mgr.bootstrap();
    const s = await mgr.createServer({ name: 'a', transportType: 'stdio' });
    client.__emitConn({ id: s.id, status: 'connected' });
    expect(mgr.list().find((x) => x.id === s.id)?.connectionStatus).toBe('connected');
  });

  it('resolution_changed persists to store + updates launchStatus', async () => {
    await mgr.bootstrap();
    const s = await mgr.createServer({ name: 'a', transportType: 'stdio' });
    client.__emitRes({
      serverId: s.id,
      resolution: {
        serverId: s.id,
        resolverKind: 'npx',
        sourceFingerprint: 'fp',
        status: 'ready',
        packageName: 'pkg',
        requestedVersion: '1.0.0',
        resolvedVersion: '1.0.0',
        installDir: '/tmp/x',
        command: 'node',
        args: ['/abs/bin.js'],
        env: {},
        error: null,
        installedAt: Date.now(),
        resolvedAt: Date.now(),
        updatedAt: Date.now(),
      },
    });
    const stored = store.loadAllResolutions().find((r) => r.serverId === s.id);
    expect(stored?.status).toBe('ready');
    expect(mgr.list().find((x) => x.id === s.id)?.launchStatus).toBe('ready');
  });

  it('resolution_changed for unknown server is a no-op', async () => {
    await mgr.bootstrap();
    expect(() => {
      client.__emitRes({
        serverId: 'nope',
        resolution: {
          serverId: 'nope',
          resolverKind: 'npx',
          sourceFingerprint: 'fp',
          status: 'failed',
          packageName: null,
          requestedVersion: null,
          resolvedVersion: null,
          installDir: null,
          command: null,
          args: [],
          env: {},
          error: 'oops',
          installedAt: null,
          resolvedAt: null,
          updatedAt: Date.now(),
        },
      });
    }).not.toThrow();
  });
});

describe('mcpManager test / retry', () => {
  it('testConnection proxies to Go', async () => {
    await mgr.bootstrap();
    const r = await mgr.testConnection({ id: 'filesystem' });
    expect(r.ok).toBe(true);
    expect(client.mcp.test).toHaveBeenCalled();
  });

  it('testConnection returns ok=false when agent offline', async () => {
    await mgr.bootstrap();
    client.isConnected = () => false;
    const r = await mgr.testConnection({ id: 'filesystem' });
    expect(r.ok).toBe(false);
    expect(r.error).toBe('agent offline');
  });

  it('retryResolution proxies to Go', async () => {
    await mgr.bootstrap();
    const r = await mgr.retryResolution('filesystem');
    expect(r.ok).toBe(true);
  });
});
