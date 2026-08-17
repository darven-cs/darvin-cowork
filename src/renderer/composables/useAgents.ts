/**
 * useAgents — renderer 端 agent 状态的薄视图。
 *
 * 数据所有权归 main（agents 表在 Go sessions.db；workspace 级隔离）。
 * 列表通过 darvin:push:agents-changed 推送；写操作走 IPC 后 main 端再
 * 广播。renderer 不持有持久化状态。
 */

import { computed, ref } from 'vue';
import type { DarvinAgent } from '../../shared/darvin-api';

const agents = ref<DarvinAgent[]>([]);
const workspaceId = ref<string | null>(null);
let initialized = false;

export function useAgents() {
  function ensureSubscribed(): void {
    if (initialized || typeof window === 'undefined' || !window.darvin) return;
    initialized = true;

    window.darvin.onAgentsChanged((list) => {
      agents.value = list;
    });
  }

  ensureSubscribed();

  async function listAgents(wsId: string): Promise<DarvinAgent[]> {
    const r = await window.darvin.listAgents(wsId);
    agents.value = r.agents;
    workspaceId.value = wsId;
    return r.agents;
  }

  async function refresh(wsId?: string): Promise<DarvinAgent[]> {
    const target = wsId ?? workspaceId.value;
    if (!target) return [];
    return listAgents(target);
  }

  async function createAgent(req: Parameters<typeof window.darvin.createAgent>[0]): Promise<DarvinAgent> {
    const r = await window.darvin.createAgent(req);
    return r.agent;
  }

  async function updateAgent(agentId: string, patch: Partial<DarvinAgent>): Promise<DarvinAgent> {
    const r = await window.darvin.updateAgent(agentId, patch);
    return r.agent;
  }

  async function deleteAgent(agentId: string): Promise<boolean> {
    const r = await window.darvin.deleteAgent(agentId);
    return r.deleted;
  }

  const presetAgents = computed(() => agents.value.filter((a) => a.source === 'preset'));
  const userAgents = computed(() => agents.value.filter((a) => a.source === 'user'));
  const defaultAgent = computed(() => agents.value.find((a) => a.isDefault) ?? null);

  return {
    agents,
    presetAgents,
    userAgents,
    defaultAgent,
    listAgents,
    refresh,
    createAgent,
    updateAgent,
    deleteAgent,
  };
}