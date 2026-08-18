/**
 * IM 通道的 renderer 状态。
 *
 * 数据所有权：Go 端 gateway 是 source of truth（im_channels 表 + 连接器状态）。
 * renderer 这层只做：
 * 1. `loadAll(workspaceId)` 一次性拉 Go 端实例列表（含状态快照）
 * 2. `create / update / delete / toggle / test / loginStart / loginPoll`
 *    调 main 端 IPC
 * 3. 订阅 `onImChanged / onImStatusChanged` 同步 Go 端推送
 *    （连接状态 / 配置 / 登录失效 都能即时更新 UI）
 *
 * composable 走 singleton 模式（模块级 ref），与 useSchedules 同款。
 */

import { onUnmounted, ref } from 'vue';
import type {
  DarvinIMCheck,
  DarvinIMInstance,
  DarvinIMLoginResult,
} from '../../shared/darvin-api';
import { showToast } from '../services/toast';
import { t } from '../services/i18n';

const instances = ref<DarvinIMInstance[]>([]);
const loading = ref(false);
const error = ref<string | null>(null);

const unsubscribers: Array<() => void> = [];
let bootstrapped = false;

async function loadAll(): Promise<void> {
  loading.value = true;
  error.value = null;
  try {
    const r = await window.darvin.imList({});
    instances.value = r.instances;
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    loading.value = false;
  }
}

async function create(req: Parameters<typeof window.darvin.imCreate>[0]): Promise<DarvinIMInstance | null> {
  try {
    const r = await window.darvin.imCreate(req);
    showToast(t('im.toast.created', { name: r.instance.name || req.channel }), 'success');
    return r.instance;
  } catch (e) {
    showToast(t('im.toast.create_failed', { error: (e as Error).message }), 'error');
    return null;
  }
}

async function update(instanceId: string, patch: Record<string, unknown>): Promise<DarvinIMInstance | null> {
  try {
    const r = await window.darvin.imUpdate({ instanceId, patch });
    showToast(t('im.toast.updated'), 'success');
    return r.instance;
  } catch (e) {
    showToast(t('im.toast.update_failed', { error: (e as Error).message }), 'error');
    return null;
  }
}

async function remove(instanceId: string): Promise<void> {
  try {
    await window.darvin.imDelete({ instanceId });
    showToast(t('im.toast.deleted'), 'success');
  } catch (e) {
    showToast(t('im.toast.delete_failed', { error: (e as Error).message }), 'error');
  }
}

async function toggle(instanceId: string, enabled: boolean): Promise<void> {
  try {
    await window.darvin.imSetEnabled({ instanceId, enabled });
  } catch (e) {
    showToast(t('im.toast.toggle_failed', { error: (e as Error).message }), 'error');
  }
}

async function test(channel: string, config: Record<string, unknown>): Promise<{ ok: boolean; error?: string; checks?: DarvinIMCheck[] }> {
  try {
    return await window.darvin.imTest({ channel, config });
  } catch (e) {
    showToast(t('im.toast.test_failed', { error: (e as Error).message }), 'error');
    return { ok: false, error: (e as Error).message };
  }
}

function loginStart(req: Parameters<typeof window.darvin.imLoginStart>[0]): Promise<DarvinIMLoginResult> {
  return window.darvin.imLoginStart(req);
}

function loginPoll(req: Parameters<typeof window.darvin.imLoginPoll>[0]): Promise<DarvinIMLoginResult> {
  return window.darvin.imLoginPoll(req);
}

function bootstrap(): void {
  if (bootstrapped) return;
  bootstrapped = true;
  const reload = (): void => {
    void loadAll();
  };
  unsubscribers.push(window.darvin.onImChanged(reload));
  unsubscribers.push(window.darvin.onImStatusChanged(reload));
}

function teardown(): void {
  while (unsubscribers.length > 0) {
    const off = unsubscribers.pop();
    off?.();
  }
  bootstrapped = false;
}

export function useIm() {
  onUnmounted(() => {
    // composable 是 singleton；不在 onUnmounted teardown（多组件共享）
  });
  bootstrap();
  return {
    instances,
    loading,
    error,
    loadAll,
    create,
    update,
    remove,
    toggle,
    test,
    loginStart,
    loginPoll,
    teardown,
  };
}
