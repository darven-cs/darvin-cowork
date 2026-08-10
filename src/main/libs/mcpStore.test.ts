/**
 * mcpStore 单测。所有 case 走 tmp SQLite 文件，与 userData 解耦。
 */
import { describe, expect, it, beforeEach } from 'vitest';
import path from 'node:path';
import fs from 'node:fs';
import os from 'node:os';
import { McpStore, buildServerFromCreate, type McpLaunchResolutionRow } from './mcpStore';

let tmpDir: string;
let store: McpStore;

beforeEach(() => {
  tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'mcp-store-'));
  store = new McpStore({ dbPath: path.join(tmpDir, 'mcp.db') });
});

describe('mcpStore — server CRUD', () => {
  it('createServer + getServer roundtrip', () => {
    const created = store.createServer({
      id: 'fs',
      name: 'filesystem',
      description: 'local',
      enabled: true,
      isBuiltIn: true,
      transportType: 'stdio',
      command: 'darvin-agent',
      args: ['mcp-filesystem'],
    });
    expect(created.createdAt).toBeGreaterThan(0);
    expect(created.updatedAt).toBe(created.createdAt);
    const got = store.getServer('fs');
    expect(got).toEqual(created);
  });

  it('listServers sorts by isBuiltIn desc + name asc', () => {
    store.createServer({ id: 'zeta', name: 'Zeta', description: '', enabled: true, isBuiltIn: false, transportType: 'stdio' });
    store.createServer({ id: 'bundled', name: 'Filesystem', description: '', enabled: true, isBuiltIn: true, transportType: 'stdio' });
    store.createServer({ id: 'alpha', name: 'Alpha', description: '', enabled: true, isBuiltIn: false, transportType: 'stdio' });
    const list = store.listServers().map((s) => s.id);
    expect(list[0]).toBe('bundled');
    expect(new Set(list.slice(1))).toEqual(new Set(['alpha', 'zeta']));
  });

  it('updateServer patches fields + writes updated_at', async () => {
    store.createServer({ id: 'fs', name: 'f', description: '', enabled: true, isBuiltIn: false, transportType: 'stdio' });
    const before = store.getServer('fs')!;
    await new Promise((r) => setTimeout(r, 5));
    const updated = store.updateServer('fs', { name: 'filesystem', enabled: false })!;
    expect(updated.name).toBe('filesystem');
    expect(updated.enabled).toBe(false);
    expect(updated.updatedAt).toBeGreaterThan(before.updatedAt);
    expect(updated.createdAt).toBe(before.createdAt);
  });

  it('updateServer returns null for unknown id', () => {
    expect(store.updateServer('nope', { name: 'x' })).toBeNull();
  });

  it('deleteServer removes row + cascade resolution', () => {
    store.createServer({ id: 'fs', name: 'f', description: '', enabled: true, isBuiltIn: false, transportType: 'stdio' });
    const res: McpLaunchResolutionRow = {
      serverId: 'fs',
      resolverKind: 'npx',
      sourceFingerprint: 'fp',
      status: 'ready',
      packageName: '@scope/pkg',
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
    };
    store.saveResolution(res);
    expect(store.loadAllResolutions()).toHaveLength(1);
    store.deleteServer('fs');
    expect(store.getServer('fs')).toBeNull();
    expect(store.loadAllResolutions()).toHaveLength(0);
  });

  it('openMcpStoreDb drops pre-existing builtin rows on open', () => {
    store.createServer({
      id: 'filesystem',
      name: 'Filesystem',
      description: 'legacy',
      enabled: true,
      isBuiltIn: true,
      transportType: 'stdio',
      command: 'darvin-agent',
      args: ['mcp-filesystem'],
    });
    expect(store.getServer('filesystem')).not.toBeNull();

    const reopened = new McpStore({ dbPath: path.join(tmpDir, 'mcp.db') });
    expect(reopened.getServer('filesystem')).toBeNull();
  });
});

describe('mcpStore — launch resolution', () => {
  it('saveResolution overwrites by server_id', () => {
    store.createServer({ id: 'fs', name: 'f', description: '', enabled: true, isBuiltIn: false, transportType: 'stdio' });
    const base: McpLaunchResolutionRow = {
      serverId: 'fs',
      resolverKind: 'npx',
      sourceFingerprint: 'fp',
      status: 'installing',
      packageName: null,
      requestedVersion: null,
      resolvedVersion: null,
      installDir: null,
      command: null,
      args: [],
      env: {},
      error: null,
      installedAt: null,
      resolvedAt: null,
      updatedAt: Date.now(),
    };
    store.saveResolution(base);
    const ready: McpLaunchResolutionRow = { ...base, status: 'ready', command: 'node', args: ['/abs/bin.js'] };
    store.saveResolution(ready);
    const all = store.loadAllResolutions();
    expect(all).toHaveLength(1);
    expect(all[0].status).toBe('ready');
    expect(all[0].args).toEqual(['/abs/bin.js']);
  });

  it('loadAllResolutions parses args / env JSON', () => {
    store.createServer({ id: 'fs', name: 'f', description: '', enabled: true, isBuiltIn: false, transportType: 'stdio' });
    store.saveResolution({
      serverId: 'fs',
      resolverKind: 'npx',
      sourceFingerprint: 'fp',
      status: 'ready',
      packageName: 'pkg',
      requestedVersion: '1',
      resolvedVersion: '1',
      installDir: '/x',
      command: 'node',
      args: ['a', 'b'],
      env: { K: 'V' },
      error: null,
      installedAt: 1,
      resolvedAt: 2,
      updatedAt: 3,
    });
    const r = store.loadAllResolutions()[0];
    expect(r.args).toEqual(['a', 'b']);
    expect(r.env).toEqual({ K: 'V' });
  });

  it('deleteResolution is no-op for missing server', () => {
    expect(() => store.deleteResolution('nope')).not.toThrow();
  });
});

describe('buildServerFromCreate', () => {
  it('fills defaults', () => {
    const s = buildServerFromCreate('m1', { name: 'X', transportType: 'stdio', command: 'c' });
    expect(s.id).toBe('m1');
    expect(s.description).toBe('');
    expect(s.enabled).toBe(true);
    expect(s.isBuiltIn).toBe(false);
    expect(s.trustLevel).toBe('ask');
  });

  it('preserves explicit trustLevel', () => {
    const s = buildServerFromCreate('m2', {
      name: 'X',
      transportType: 'stdio',
      command: 'c',
      trustLevel: 'trusted',
    });
    expect(s.trustLevel).toBe('trusted');
  });

  it('round-trips trustLevel through create + get + update', () => {
    const created = store.createServer(
      buildServerFromCreate('m3', {
        name: 'T',
        transportType: 'stdio',
        command: 'npx',
        trustLevel: 'trusted',
      }),
    );
    expect(created.trustLevel).toBe('trusted');
    expect(store.getServer('m3')?.trustLevel).toBe('trusted');

    const updated = store.updateServer('m3', { trustLevel: 'ask' });
    expect(updated?.trustLevel).toBe('ask');
    expect(store.getServer('m3')?.trustLevel).toBe('ask');
  });
});
