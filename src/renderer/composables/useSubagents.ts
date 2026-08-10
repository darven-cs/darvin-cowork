/**
 * useSubagents — Subagents artifact tab 的 renderer 状态。
 *
 * 数据来源：Go 端 subagent.Manager（权威）+ SubagentStore（持久化），
 * 通过 window.darvin.subagent* IPC 主动拉取。本 composable 走 singleton
 * 模式（模块级 ref），与 useArtifacts / useMcpServers 一致；父 session
 * 切换时自动重置 runs / selectedId / messagesByRun。
 *
 * 轮询：任一 run 处于 running 时每 5s 拉一次列表；全部终态后停。
 */

import { ref, watch, onBeforeUnmount } from 'vue';
import type {
  SubagentRun,
  SubagentMessage,
} from '../../shared/darvin-api';
import { useSession } from './useSession';

const runs = ref<SubagentRun[]>([]);
const loading = ref(false);
const selectedId = ref<string | null>(null);
const messagesByRun = ref<Record<string, SubagentMessage[]>>({});

const session = useSession();

let timer: ReturnType<typeof setInterval> | undefined;

function stopPolling(): void {
  if (timer) {
    clearInterval(timer);
    timer = undefined;
  }
}

function startPolling(): void {
  if (timer) return;
  timer = setInterval(() => {
    void refreshList();
  }, 5_000);
}

function resetForSession(): void {
  runs.value = [];
  selectedId.value = null;
  messagesByRun.value = {};
  stopPolling();
}

/** 面板激活 / session 切换后重新拉当前 session 的 runs。 */
function refreshForActiveSession(): void {
  resetForSession();
  void refreshList();
}

async function refreshList(): Promise<void> {
  const sid = session.activeSessionId.value;
  if (!sid) return;
  loading.value = true;
  try {
    const r = await window.darvin.subagentList(sid);
    runs.value = r.subagents ?? [];
  } catch {
    // 保留上次列表；subagent 面板非关键路径，不弹 toast
  } finally {
    loading.value = false;
  }
}

async function loadMessages(runId: string): Promise<void> {
  try {
    const r = await window.darvin.subagentGetMessages(runId);
    messagesByRun.value = { ...messagesByRun.value, [runId]: r.messages ?? [] };
  } catch {
    // 静默失败；详情态展示空历史兜底
  }
}

function selectRun(id: string | null): void {
  selectedId.value = id;
  if (id && !messagesByRun.value[id]) void loadMessages(id);
}

/** 显示名：description > id 短前缀 > 兜底。 */
function getSubagentDisplayName(run: SubagentRun): string {
  const description = run.description?.trim();
  if (description) return description;
  const id = run.id?.trim();
  if (!id) return 'Subagent';
  return id.length > 8 ? id.slice(0, 8) : id;
}

function getSubagentDisplayInitial(run: SubagentRun): string {
  const name = getSubagentDisplayName(run).trim();
  return (name.charAt(0) || 'S').toUpperCase();
}

function isTerminal(status: SubagentRun['status']): boolean {
  return status === 'done' || status === 'error' || status === 'aborted' || status === 'timeout';
}

// 轮询：任一 running → start；全部终态 → stop
watch(
  runs,
  (rs) => {
    rs.some((r) => !isTerminal(r.status)) ? startPolling() : stopPolling();
  },
  { deep: false },
);

// 父 session 切换 → 重置 + 拉新 session 的 runs
watch(
  () => session.activeSessionId.value,
  () => refreshForActiveSession(),
);

onBeforeUnmount(stopPolling);

export function useSubagents() {
  return {
    runs,
    loading,
    selectedId,
    messagesByRun,
    refreshList,
    refreshForActiveSession,
    loadMessages,
    selectRun,
    getSubagentDisplayName,
    getSubagentDisplayInitial,
    isTerminal,
  };
}

/** 测试专用：清空单例状态 + 停轮询。 */
export function __resetSubagentsForTest(): void {
  resetForSession();
}