/**
 * useMcpServers 纯逻辑测试。
 *
 * 不挂 Vue component，直接在 module 级 ref + 假 darvin client 上验证：
 * - refresh 把 listMcpServers() 的结果写进 servers.value
 * - create 调 createMcpServer + 追加到 servers.value + toast
 * - update 调 updateMcpServer + 替换对应条目 + toast
 * - remove 调 deleteMcpServer + 过滤 + toast
 * - setEnabled 乐观更新 + 失败回滚 + toast
 * - testConnection 成功 toast 显示 tools 数；失败 toast 显示 error
 * - onMcpServersChanged 推送覆盖 servers.value
 * - onMcpConnectionChanged 更新对应 server 的 connectionStatus / connectionError
 */

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type {
  DarvinMcpConnectionChangedEvent,
  DarvinMcpServer,
  DarvinMcpServerCreate,
  DarvinMcpServerPatch,
  DarvinTestMcpConnectionResponse,
} from '../../shared/darvin-api';
import { __resetMcpServersForTest, useMcpServers } from './useMcpServers';

type ServersListener = (servers: DarvinMcpServer[]) => void;
type ConnListener = (e: DarvinMcpConnectionChangedEvent) => void;

interface FakeDarvin {
  listMcpServers: ReturnType<typeof vi.fn>;
  createMcpServer: ReturnType<typeof vi.fn>;
  updateMcpServer: ReturnType<typeof vi.fn>;
  deleteMcpServer: ReturnType<typeof vi.fn>;
  setMcpServerEnabled: ReturnType<typeof vi.fn>;
  testMcpConnection: ReturnType<typeof vi.fn>;
  retryMcpLaunchResolution: ReturnType<typeof vi.fn>;
  listMcpResources: ReturnType<typeof vi.fn>;
  listMcpPrompts: ReturnType<typeof vi.fn>;
  getMcpPrompt: ReturnType<typeof vi.fn>;
  onMcpServersChanged: ReturnType<typeof vi.fn>;
  onMcpConnectionChanged: ReturnType<typeof vi.fn>;
  __serverListeners: ServersListener[];
  __connListeners: ConnListener[];
  __emitServers: (next: DarvinMcpServer[]) => void;
  __emitConn: (e: DarvinMcpConnectionChangedEvent) => void;
}

const FS: DarvinMcpServer = {
  id: 'filesystem',
  name: 'Filesystem',
  description: '本地文件系统',
  enabled: true,
  transportType: 'stdio',
  command: 'darvin-agent',
  args: ['mcp-filesystem'],
  isBuiltIn: true,
  createdAt: 0,
  updatedAt: 0,
  connectionStatus: 'disconnected',
  launchStatus: 'ready',
};

function installFakeDarvin(): FakeDarvin {
  const fake: FakeDarvin = {
    listMcpServers: vi.fn(),
    createMcpServer: vi.fn(),
    updateMcpServer: vi.fn(),
    deleteMcpServer: vi.fn(),
    setMcpServerEnabled: vi.fn(),
    testMcpConnection: vi.fn(),
    retryMcpLaunchResolution: vi.fn(),
    listMcpResources: vi.fn(),
    listMcpPrompts: vi.fn(),
    getMcpPrompt: vi.fn(),
    onMcpServersChanged: vi.fn(),
    onMcpConnectionChanged: vi.fn(),
    __serverListeners: [],
    __connListeners: [],
    __emitServers(next) {
      for (const l of fake.__serverListeners) l(next);
    },
    __emitConn(e) {
      for (const l of fake.__connListeners) l(e);
    },
  };
  fake.listMcpServers.mockResolvedValue({ servers: [FS] });
  fake.createMcpServer.mockImplementation(async (req: DarvinMcpServerCreate) => ({
    server: {
      ...FS,
      id: `mcp_${req.name}`,
      name: req.name,
      transportType: req.transportType,
      command: req.command,
      args: req.args ?? [],
      env: req.env ?? {},
      isBuiltIn: false,
    },
  }));
  fake.updateMcpServer.mockImplementation(async ({ id, patch }: { id: string; patch: DarvinMcpServerPatch }) => ({
    server: { ...FS, id, name: patch.name ?? FS.name, args: patch.args ?? FS.args },
  }));
  fake.deleteMcpServer.mockResolvedValue({ ok: true });
  fake.setMcpServerEnabled.mockResolvedValue({ ok: true });
  fake.testMcpConnection.mockResolvedValue({ ok: true, tools: [{ name: 'a', description: '', inputSchema: {} }] } as DarvinTestMcpConnectionResponse);
  fake.retryMcpLaunchResolution.mockResolvedValue({ ok: true });
  fake.onMcpServersChanged.mockImplementation((handler: ServersListener) => {
    fake.__serverListeners.push(handler);
    return () => {
      const idx = fake.__serverListeners.indexOf(handler);
      if (idx >= 0) fake.__serverListeners.splice(idx, 1);
    };
  });
  fake.onMcpConnectionChanged.mockImplementation((handler: ConnListener) => {
    fake.__connListeners.push(handler);
    return () => {
      const idx = fake.__connListeners.indexOf(handler);
      if (idx >= 0) fake.__connListeners.splice(idx, 1);
    };
  });
  (globalThis as unknown as { window: { darvin: unknown } }).window = { darvin: fake };
  return fake;
}

describe('useMcpServers', () => {
  let fake: FakeDarvin;

  beforeEach(() => {
    __resetMcpServersForTest();
    fake = installFakeDarvin();
  });

  afterEach(() => {
    __resetMcpServersForTest();
  });

  it('refresh pulls listMcpServers into servers.value', async () => {
    const { servers, loading, refresh } = useMcpServers();
    await refresh();
    expect(servers.value.map((s) => s.id)).toEqual(['filesystem']);
    expect(loading.value).toBe(false);
  });

  it('create appends the new server and toasts', async () => {
    const { servers, create } = useMcpServers();
    const s = await create({
      name: 'github',
      transportType: 'stdio',
      command: 'npx',
      args: ['-y', '@scope/server-github'],
    });
    expect(s?.id).toBe('mcp_github');
    expect(servers.value.find((x) => x.id === 'mcp_github')).toBeDefined();
    expect(fake.createMcpServer).toHaveBeenCalled();
  });

  it('update replaces the matching server and toasts', async () => {
    const { servers, update } = useMcpServers();
    await update('filesystem', { name: 'FS', args: ['mcp-filesystem', '--debug'] });
    const found = servers.value.find((s) => s.id === 'filesystem');
    expect(found?.name).toBe('FS');
    expect(found?.args).toEqual(['mcp-filesystem', '--debug']);
    expect(fake.updateMcpServer).toHaveBeenCalledWith({
      id: 'filesystem',
      patch: { name: 'FS', args: ['mcp-filesystem', '--debug'] },
    });
  });

  it('remove filters the server and toasts', async () => {
    const { servers, remove } = useMcpServers();
    await new Promise((r) => setTimeout(r, 10));
    expect(servers.value.length).toBe(1);
    await remove('filesystem');
    expect(servers.value).toEqual([]);
    expect(fake.deleteMcpServer).toHaveBeenCalledWith({ id: 'filesystem' });
  });

  it('setEnabled optimistically toggles and awaits the IPC', async () => {
    const { servers, setEnabled } = useMcpServers();
    await new Promise((r) => setTimeout(r, 10));
    await setEnabled('filesystem', false);
    expect(servers.value.find((s) => s.id === 'filesystem')?.enabled).toBe(false);
    expect(fake.setMcpServerEnabled).toHaveBeenCalledWith({ id: 'filesystem', enabled: false });
  });

  it('setEnabled rolls back when the IPC rejects', async () => {
    const { servers, setEnabled } = useMcpServers();
    await new Promise((r) => setTimeout(r, 10));
    fake.setMcpServerEnabled.mockRejectedValueOnce(new Error('boom'));
    await expect(setEnabled('filesystem', false)).rejects.toThrow('boom');
    expect(servers.value.find((s) => s.id === 'filesystem')?.enabled).toBe(true);
  });

  it('testConnection success toasts with the tool count', async () => {
    const { testConnection } = useMcpServers();
    const r = await testConnection('filesystem');
    expect(r?.ok).toBe(true);
    expect(r?.tools?.length).toBe(1);
  });

  it('testConnection failure toasts the error', async () => {
    fake.testMcpConnection.mockResolvedValueOnce({ ok: false, error: 'ECONNREFUSED' });
    const { testConnection } = useMcpServers();
    const r = await testConnection('filesystem');
    expect(r?.ok).toBe(false);
  });

  it('retryResolution proxies to the IPC', async () => {
    const { retryResolution } = useMcpServers();
    await retryResolution('filesystem');
    expect(fake.retryMcpLaunchResolution).toHaveBeenCalledWith({ id: 'filesystem' });
  });

  it('onMcpServersChanged push overrides servers.value', async () => {
    const { servers } = useMcpServers();
    await new Promise((r) => setTimeout(r, 10));
    const next: DarvinMcpServer[] = [
      { ...FS, id: 'github', name: 'GitHub' },
      { ...FS, id: 'slack', name: 'Slack' },
    ];
    fake.__emitServers(next);
    expect(servers.value.map((s) => s.id).sort()).toEqual(['github', 'slack']);
  });

  it('onMcpConnectionChanged updates matching server connectionStatus / error', async () => {
    const { servers } = useMcpServers();
    await new Promise((r) => setTimeout(r, 10));
    fake.__emitConn({ id: 'filesystem', status: 'connected' });
    expect(servers.value.find((s) => s.id === 'filesystem')?.connectionStatus).toBe('connected');
    fake.__emitConn({ id: 'filesystem', status: 'error', error: 'ECONNREFUSED' });
    expect(servers.value.find((s) => s.id === 'filesystem')?.connectionStatus).toBe('error');
    expect(servers.value.find((s) => s.id === 'filesystem')?.connectionError).toBe('ECONNREFUSED');
  });

  it('onMcpConnectionChanged ignores unknown server IDs', async () => {
    const { servers } = useMcpServers();
    await new Promise((r) => setTimeout(r, 10));
    fake.__emitConn({ id: 'unknown', status: 'connected' });
    expect(servers.value.find((s) => s.id === 'unknown')).toBeUndefined();
  });
});
describe('useMcpServers — resources & prompts', () => {
  it('listResources returns the resource array on success', async () => {
    const fake = installFakeDarvin();
    fake.listMcpResources.mockResolvedValue({
      resources: [{ uri: 'file:///tmp/a.txt', name: 'a' }],
    });
    const { listResources } = useMcpServers();
    const got = await listResources('filesystem');
    expect(got).toHaveLength(1);
    expect(got[0].uri).toBe('file:///tmp/a.txt');
  });

  it('listResources falls back to [] on failure', async () => {
    installFakeDarvin();
    const { listResources } = useMcpServers();
    const got = await listResources('gh');
    expect(got).toEqual([]);
  });

  it('listPrompts + getPrompt round-trip', async () => {
    const fake = installFakeDarvin();
    fake.listMcpPrompts.mockResolvedValue({ prompts: [{ name: 'summarize' }] });
    fake.getMcpPrompt.mockResolvedValue({ messages: [{ role: 'user', content: 'hi' }] });
    const { listPrompts, getPrompt } = useMcpServers();
    const prompts = await listPrompts('filesystem');
    expect(prompts[0].name).toBe('summarize');
    const msgs = await getPrompt('filesystem', 'summarize');
    expect(msgs[0].content).toBe('hi');
  });
});
