/**
 * Scheduled tasks 的 renderer 状态。
 *
 * 数据所有权：Go 端 gateway 是 source of truth（schedules 表 + cron engine）。
 * renderer 这层只做：
 * 1. `loadAll(workspaceId)` 一次性拉 Go 端列表
 * 2. `create / update / delete / toggle / runNow / abort` 调 main 端 IPC
 * 3. `loadRuns(scheduleId)` 拉单条 schedule 的运行历史
 * 4. 订阅 `onSchedulesChanged / onScheduleRunsChanged / onScheduleFired`
 *    同步 Go 端推送（cancelled due to failure / new fire 都能即时更新 UI）
 *
 * composable 走 singleton 模式（模块级 ref），与 useSkills / useAgents
 * 同款；多处 useSchedules() 共享同一份 schedules / runs 状态。
 */

import { onUnmounted, ref } from 'vue';
import type { DarvinSchedule, DarvinScheduleRun } from '../../shared/darvin-api';
import { showToast } from '../services/toast';
import { t } from '../services/i18n';

const schedules = ref<DarvinSchedule[]>([]);
const runs = ref<Record<string, DarvinScheduleRun[]>>({});
const loading = ref(false);
const error = ref<string | null>(null);

const unsubscribers: Array<() => void> = [];
let bootstrapped = false;

async function loadAll(workspaceId: string): Promise<void> {
  loading.value = true;
  error.value = null;
  try {
    const r = await window.darvin.scheduleList({ workspaceId });
    schedules.value = r.schedules;
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    loading.value = false;
  }
}

async function loadRuns(workspaceId: string, scheduleId: string): Promise<void> {
  try {
    const r = await window.darvin.scheduleListRuns({ workspaceId, scheduleId });
    runs.value = { ...runs.value, [scheduleId]: r.runs };
  } catch (e) {
    showToast(t('schedule.history.failed', { error: (e as Error).message }), 'error');
  }
}

async function loadAllRuns(workspaceId: string): Promise<void> {
  try {
    const r = await window.darvin.scheduleListAllRuns({ workspaceId });
    // 按 scheduleId 分桶
    const bucket: Record<string, DarvinScheduleRun[]> = {};
    for (const run of r.runs) {
      (bucket[run.scheduleId] ??= []).push(run);
    }
    runs.value = bucket;
  } catch (e) {
    showToast(t('schedule.history.failed', { error: (e as Error).message }), 'error');
  }
}

async function create(
  workspaceId: string,
  schedule: Parameters<typeof window.darvin.scheduleCreate>[0]['schedule'],
): Promise<DarvinSchedule | null> {
  try {
    const r = await window.darvin.scheduleCreate({ workspaceId, schedule });
    showToast(t('schedule.toast.created', { name: r.schedule.name }), 'success');
    return r.schedule;
  } catch (e) {
    showToast(t('schedule.toast.create_failed', { error: (e as Error).message }), 'error');
    return null;
  }
}

async function update(
  workspaceId: string,
  scheduleId: string,
  patch: Parameters<typeof window.darvin.scheduleUpdate>[0]['patch'],
): Promise<DarvinSchedule | null> {
  try {
    const r = await window.darvin.scheduleUpdate({ workspaceId, scheduleId, patch });
    showToast(t('schedule.toast.updated', { name: r.schedule.name }), 'success');
    return r.schedule;
  } catch (e) {
    showToast(t('schedule.toast.update_failed', { error: (e as Error).message }), 'error');
    return null;
  }
}

async function remove(workspaceId: string, scheduleId: string, name: string): Promise<boolean> {
  try {
    await window.darvin.scheduleDelete({ workspaceId, scheduleId });
    showToast(t('schedule.toast.deleted', { name }), 'success');
    return true;
  } catch (e) {
    showToast(t('schedule.toast.delete_failed', { error: (e as Error).message }), 'error');
    return false;
  }
}

async function toggle(
  workspaceId: string,
  scheduleId: string,
  enabled: boolean,
): Promise<void> {
  await window.darvin.scheduleToggle({ workspaceId, scheduleId, enabled });
}

async function runNow(workspaceId: string, scheduleId: string, name: string): Promise<void> {
  try {
    await window.darvin.scheduleRunNow({ workspaceId, scheduleId });
    showToast(t('schedule.toast.run_now', { name }), 'info');
  } catch (e) {
    showToast(t('schedule.toast.run_failed', { error: (e as Error).message }), 'error');
  }
}

async function abort(workspaceId: string, scheduleId: string, runId: string): Promise<void> {
  try {
    await window.darvin.scheduleAbort({ workspaceId, scheduleId, runId });
  } catch (e) {
    showToast(t('schedule.toast.abort_failed', { error: (e as Error).message }), 'error');
  }
}

function bootstrap(): void {
  if (bootstrapped) return;
  bootstrapped = true;

  unsubscribers.push(
    window.darvin.onSchedulesChanged((payload) => {
      void loadAll(payload.workspaceId);
    }),
  );
  unsubscribers.push(
    window.darvin.onScheduleRunsChanged(({ scheduleId }) => {
      const wsId = schedules.value.find((s) => s.id === scheduleId)?.workspaceId;
      if (wsId) {
        void loadRuns(wsId, scheduleId);
      }
    }),
  );
  unsubscribers.push(
    window.darvin.onScheduleFired(({ scheduleId }) => {
      const wsId = schedules.value.find((s) => s.id === scheduleId)?.workspaceId;
      if (wsId) {
        void loadRuns(wsId, scheduleId);
      }
    }),
  );
}

function teardown(): void {
  while (unsubscribers.length > 0) {
    const off = unsubscribers.pop();
    off?.();
  }
  bootstrapped = false;
}

export function useSchedules() {
  onUnmounted(() => {
    // composable 是 singleton；不在 onUnmounted teardown（多组件共享）
  });
  bootstrap();
  return {
    schedules,
    runs,
    loading,
    error,
    loadAll,
    loadRuns,
    loadAllRuns,
    create,
    update,
    remove,
    toggle,
    runNow,
    abort,
    teardown,
  };
}