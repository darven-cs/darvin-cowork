/**
 * McpView 的 renderer 状态。
 *
 * 数据所有权：main 端 SQLite 是 server 元数据 + launch resolution 的 source
 * of truth；Go 端 registry 是运行时连接状态的 source of truth。两者通过
 * onMcpServersChanged / onMcpConnectionChanged push 向 renderer 广播。
 *
 * composable 走 singleton 模式（模块级 ref）：多处 useMcpServers() 共享同一份
 * servers 数组，避免侧栏角标 / McpView 列表出现 stale 不一致。
 */

import { ref } from 'vue';
import type {
  DarvinMcpServer,
  DarvinMcpServerCreate,
  DarvinMcpServerPatch,
  DarvinMcpServerExposedTool,
  DarvinMcpResource,
  DarvinMcpPrompt,
  DarvinMcpPromptMessage,
  DarvinTestMcpConnectionResponse,
} from '../../shared/darvin-api';
import { showToast } from '../services/toast';
import { t } from '../services/i18n';

const servers = ref<DarvinMcpServer[]>([]);
const loading = ref(false);
const error = ref<string | null>(null);
let subscribed = false;
let offServers: (() => void) | null = null;
let offConnection: (() => void) | null = null;

async function refresh(): Promise<void> {
  loading.value = true;
  error.value = null;
  try {
    const r = await window.darvin.listMcpServers();
    servers.value = r.servers;
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    loading.value = false;
  }
}

async function create(req: DarvinMcpServerCreate): Promise<DarvinMcpServer | null> {
  try {
    const r = await window.darvin.createMcpServer(req);
    servers.value = [...servers.value, r.server];
    showToast(t('mcp.create.success', { name: r.server.name }), 'success');
    return r.server;
  } catch (e) {
    showToast(t('mcp.create.failed', { error: (e as Error).message }), 'error');
    return null;
  }
}

async function update(id: string, patch: DarvinMcpServerPatch): Promise<DarvinMcpServer | null> {
  try {
    const r = await window.darvin.updateMcpServer({ id, patch });
    servers.value = servers.value.map((s) => (s.id === id ? r.server : s));
    showToast(t('mcp.update.success', { name: r.server.name }), 'success');
    return r.server;
  } catch (e) {
    showToast(t('mcp.update.failed', { error: (e as Error).message }), 'error');
    return null;
  }
}

async function remove(id: string): Promise<void> {
  const found = servers.value.find((s) => s.id === id);
  try {
    await window.darvin.deleteMcpServer({ id });
    servers.value = servers.value.filter((s) => s.id !== id);
    showToast(t('mcp.delete.success', { name: found?.name ?? id }), 'success');
  } catch (e) {
    showToast(t('mcp.delete.failed', { error: (e as Error).message }), 'error');
  }
}

/**
 * 乐观更新：立刻翻本地缓存的 enabled，调 main 端 setMcpServerEnabled。
 * 失败时回滚并 toast。
 */
async function setEnabled(id: string, enabled: boolean): Promise<void> {
  const idx = servers.value.findIndex((s) => s.id === id);
  const prev = idx >= 0 ? servers.value[idx].enabled : undefined;
  if (idx >= 0) {
    servers.value = servers.value.map((s, i) => (i === idx ? { ...s, enabled } : s));
  }
  try {
    await window.darvin.setMcpServerEnabled({ id, enabled });
  } catch (e) {
    if (idx >= 0 && prev !== undefined) {
      servers.value = servers.value.map((s, i) => (i === idx ? { ...s, enabled: prev } : s));
    }
    showToast(t('mcp.update.failed', { error: (e as Error).message }), 'error');
    throw e;
  }
}

async function testConnection(id: string): Promise<DarvinTestMcpConnectionResponse | null> {
  try {
    const r = await window.darvin.testMcpConnection({ id });
    if (r.ok) {
      const count = r.tools?.length ?? 0;
      showToast(t('mcp.test.success', { count }), 'success');
    } else {
      showToast(t('mcp.test.failed', { error: r.error ?? 'unknown' }), 'error');
    }
    return r;
  } catch (e) {
    showToast(t('mcp.test.failed', { error: (e as Error).message }), 'error');
    return null;
  }
}

async function retryResolution(id: string): Promise<void> {
  try {
    await window.darvin.retryMcpLaunchResolution({ id });
  } catch (e) {
    showToast(t('mcp.update.failed', { error: (e as Error).message }), 'error');
  }
}

async function listResources(id: string): Promise<DarvinMcpResource[]> {
  try {
    const r = await window.darvin.listMcpResources(id);
    return r.resources;
  } catch {
    return [];
  }
}

async function listPrompts(id: string): Promise<DarvinMcpPrompt[]> {
  try {
    const r = await window.darvin.listMcpPrompts(id);
    return r.prompts;
  } catch {
    return [];
  }
}

async function getPrompt(id: string, name: string): Promise<DarvinMcpPromptMessage[]> {
  try {
    const r = await window.darvin.getMcpPrompt(id, name);
    return r.messages;
  } catch {
    return [];
  }
}

async function getLogs(id: string): Promise<string[]> {
  try {
    const r = await window.darvin.getMcpLogs(id);
    return r.lines;
  } catch {
    return [];
  }
}

/** 无 toast 的工具列表拉取：卡片在 exposedTools 缺失时兜底用。 */
async function fetchTools(id: string): Promise<DarvinMcpServerExposedTool[]> {
  try {
    const r = await window.darvin.testMcpConnection({ id });
    return r.tools ?? [];
  } catch {
    return [];
  }
}

function ensureSubscribed(): void {
  if (subscribed) return;
  subscribed = true;
  offServers = window.darvin.onMcpServersChanged((next) => {
    servers.value = next;
  });
  offConnection = window.darvin.onMcpConnectionChanged((e) => {
    const idx = servers.value.findIndex((s) => s.id === e.id);
    if (idx < 0) return;
    servers.value = servers.value.map((s, i) =>
      i === idx ? { ...s, connectionStatus: e.status, connectionError: e.error } : s,
    );
  });
}

export function useMcpServers() {
  ensureSubscribed();
  if (servers.value.length === 0 && !loading.value) {
    void refresh();
  }
  return {
    servers,
    loading,
    error,
    refresh,
    create,
    update,
    remove,
    setEnabled,
    testConnection,
    retryResolution,
    listResources,
    listPrompts,
    getPrompt,
    getLogs,
    fetchTools,
  };
}

export function __resetMcpServersForTest(): void {
  servers.value = [];
  loading.value = false;
  error.value = null;
  subscribed = false;
  offServers?.();
  offServers = null;
  offConnection?.();
  offConnection = null;
}