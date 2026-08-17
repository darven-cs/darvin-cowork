/**
 * useWorkspaces — renderer 端 workspace 状态的薄视图。
 *
 * 数据所有权归 main（workspaces 表在 Go sessions.db，main 缓存一份）。
 * renderer 不持任何持久化状态：读写走 IPC；workspace 列表变更由 main
 * 通过 WorkspacesChanged push 下发。
 */

import { computed, ref } from 'vue';
import type { DarvinWorkspace } from '../../shared/darvin-api';

const workspaces = ref<DarvinWorkspace[]>([]);
const activeWorkspaceId = ref<string | null>(null);
let initialized = false;

export function useWorkspaces() {
  function ensureSubscribed(): void {
    if (initialized || typeof window === 'undefined' || !window.darvin) return;
    initialized = true;

    window.darvin.onWorkspacesChanged((list) => {
      workspaces.value = list;
    });

    void (async () => {
      try {
        const [w, a] = await Promise.all([
          window.darvin.listWorkspaces(),
          window.darvin.getActiveWorkspace(),
        ]);
        workspaces.value = w.workspaces;
        activeWorkspaceId.value = a.workspaceId;
      } catch {
        // agent offline: 空态由视图兜底
      }
    })();
  }

  ensureSubscribed();

  async function refresh(): Promise<void> {
    try {
      const [w, a] = await Promise.all([
        window.darvin.listWorkspaces(),
        window.darvin.getActiveWorkspace(),
      ]);
      workspaces.value = w.workspaces;
      activeWorkspaceId.value = a.workspaceId;
    } catch {
      // agent offline: 保留现有列表
    }
  }

  async function createWorkspace(req?: { name?: string; rootPath?: string }): Promise<DarvinWorkspace> {
    const r = await window.darvin.createWorkspace(req);
    if (!workspaces.value.some((w) => w.id === r.workspace.id)) {
      workspaces.value = [r.workspace, ...workspaces.value];
    }
    return r.workspace;
  }

  async function switchWorkspace(id: string): Promise<void> {
    if (activeWorkspaceId.value === id) return;
    const r = await window.darvin.setActiveWorkspace(id);
    activeWorkspaceId.value = r.workspaceId;
  }

  async function deleteWorkspace(id: string, opts?: { force?: boolean }): Promise<void> {
    const r = await window.darvin.deleteWorkspace(id, opts);
    workspaces.value = workspaces.value.filter((w) => w.id !== id);
    activeWorkspaceId.value = r.nextActiveWorkspaceId;
  }

  async function renameWorkspace(id: string, name: string): Promise<DarvinWorkspace> {
    const r = await window.darvin.renameWorkspace({ workspaceId: id, name });
    replaceInList(r.workspace);
    return r.workspace;
  }

  async function updateWorkspaceRoot(id: string, rootPath: string): Promise<DarvinWorkspace> {
    const r = await window.darvin.updateWorkspaceRoot({ workspaceId: id, rootPath });
    replaceInList(r.workspace);
    return r.workspace;
  }

  async function updateDefaultAgent(req: { workspaceId: string; defaultAgentId: string }): Promise<DarvinWorkspace> {
    const r = await window.darvin.updateDefaultAgent(req);
    replaceInList(r.workspace);
    return r.workspace;
  }

  function replaceInList(updated: DarvinWorkspace): void {
    const i = workspaces.value.findIndex((w) => w.id === updated.id);
    if (i === -1) workspaces.value = [updated, ...workspaces.value];
    else {
      const list = workspaces.value.slice();
      list[i] = updated;
      workspaces.value = list;
    }
  }

  const activeWorkspace = computed(() =>
    workspaces.value.find((w) => w.id === activeWorkspaceId.value) ?? null,
  );

  return {
    workspaces,
    activeWorkspaceId,
    activeWorkspace,
    refresh,
    createWorkspace,
    switchWorkspace,
    deleteWorkspace,
    renameWorkspace,
    updateWorkspaceRoot,
    updateDefaultAgent,
  };
}