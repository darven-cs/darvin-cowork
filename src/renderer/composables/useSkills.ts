/**
 * skills 列表的 renderer 状态。
 *
 * 数据所有权：main 端是 source of truth（SQLite enabled + chokidar fs）。
 * renderer 这层只做：
 * 1. `refresh()` 一次性拉 main 端 list 缓存
 * 2. `setEnabled()` 乐观更新 + 失败回滚
 * 3. `install/uninstall/upgrade` 调 main 端 IPC（v0 是 stub）
 * 4. 订阅 `onSkillsChanged` 同步 main 端推送（chokidar / Go 端 emit 都会触发）
 *
 * composable 走 singleton 模式（模块级 ref）：多处 useSkills() 共享同一份
 * skills 数组，避免 Sidebar 角标 / SkillsView 列表出现 stale 不一致。
 */

import { computed, onUnmounted, ref } from 'vue';
import type { DarvinSkillSummary } from '../../shared/darvin-api';
import { showToast } from '../services/toast';
import { t } from '../services/i18n';

const skills = ref<DarvinSkillSummary[]>([]);
const loading = ref(false);
const error = ref<string | null>(null);
let subscribed = false;
let unsubscribe: (() => void) | null = null;

async function refresh(): Promise<void> {
  loading.value = true;
  error.value = null;
  try {
    const r = await window.darvin.listSkills();
    skills.value = r.skills;
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    loading.value = false;
  }
}

/**
 * 乐观更新：立刻翻本地缓存的 enabled，调 main 端 setEnabled。
 * 失败时回滚并 toast。
 */
async function setEnabled(skillId: string, enabled: boolean): Promise<void> {
  const idx = skills.value.findIndex((s) => s.id === skillId);
  const prev = idx >= 0 ? skills.value[idx].enabled : undefined;
  if (idx >= 0) {
    skills.value = skills.value.map((s, i) => (i === idx ? { ...s, enabled } : s));
  }
  try {
    await window.darvin.setSkillEnabled({ skillId, enabled });
  } catch (e) {
    if (idx >= 0 && prev !== undefined) {
      skills.value = skills.value.map((s, i) => (i === idx ? { ...s, enabled: prev } : s));
    }
    showToast(t('skill.toggle.failed'), 'error');
    throw e;
  }
}

interface InstallResult {
  skill: DarvinSkillSummary;
  riskLevel: 'safe' | 'low' | 'medium' | 'high' | 'critical';
}

async function install(source: string): Promise<InstallResult | null> {
  try {
    const r = await window.darvin.installSkill({ source });
    // 不直接改 skills.value；等 chokidar 触发 main 端 push 即可
    showToast(t('skill.install.success', { name: r.skill.name }), 'success');
    return r;
  } catch (e) {
    showToast(t('skill.install.failed', { error: (e as Error).message }), 'error');
    return null;
  }
}

async function uninstall(skillId: string): Promise<void> {
  try {
    await window.darvin.uninstallSkill({ skillId });
    // 同样等 chokidar push；这里先乐观过滤
    skills.value = skills.value.filter((s) => s.id !== skillId);
    const found = skills.value.find((s) => s.id === skillId);
    showToast(t('skill.uninstall.success', { name: found?.name ?? skillId }), 'success');
  } catch (e) {
    showToast(t('skill.install.failed', { error: (e as Error).message }), 'error');
  }
}

async function upgrade(skillId: string): Promise<void> {
  const found = skills.value.find((s) => s.id === skillId);
  if (found?.isBuiltIn) {
    showToast(t('skill.upgrade.bundled_blocked'), 'error');
    return;
  }
  try {
    const r = await window.darvin.upgradeSkill({ skillId });
    skills.value = skills.value.map((s) => (s.id === skillId ? r.skill : s));
    showToast(t('skill.upgrade.success', { name: r.skill.name, version: r.skill.version ?? '0.0.0' }), 'success');
  } catch (e) {
    showToast(t('skill.upgrade.failed', { error: (e as Error).message }), 'error');
  }
}

function ensureSubscribed(): void {
  if (subscribed) return;
  subscribed = true;
  unsubscribe = window.darvin.onSkillsChanged((next) => {
    skills.value = next;
  });
}

export function useSkills() {
  ensureSubscribed();
  // 第一次调用 refresh；后续 useSkills() 调用共享同一份 skills，
  // 不再二次 refresh——避免与 chokidar 推送抢状态。
  if (skills.value.length === 0 && !loading.value) {
    void refresh();
  }
  onUnmounted(() => {
    // 不解除 singleton 订阅；多个 view 共用同一份数据
  });
  return {
    skills,
    loading,
    error,
    refresh,
    setEnabled,
    install,
    uninstall,
    upgrade,
    installed: computed(() => skills.value.filter((s) => s.isBuiltIn)),
    userSkills: computed(() => skills.value.filter((s) => !s.isBuiltIn)),
    bundled: computed(() => skills.value.filter((s) => s.isOfficial)),
  };
}

// 供测试使用：清空模块级状态（不在生产代码里调用）
export function __resetSkillsForTest(): void {
  skills.value = [];
  loading.value = false;
  error.value = null;
  subscribed = false;
  unsubscribe?.();
  unsubscribe = null;
}
