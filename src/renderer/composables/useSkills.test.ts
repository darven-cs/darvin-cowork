/**
 * spec 33 — useSkills 纯逻辑测试。
 *
 * 不挂 Vue component，直接在 module 级 ref + 假 darvin client 上验证：
 * - refresh 把 darvin.listSkills() 的结果写进 skills.value
 * - setEnabled 乐观更新 + 失败回滚
 * - setEnabled 失败时弹 toast
 * - onSkillsChanged 推送会覆盖 skills.value
 *
 * 跑测前要先把模块 singleton 清掉，避免前一个 case 的状态污染。
 */

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { DarvinSkillSummary } from '../../shared/darvin-api';
import { __resetSkillsForTest, useSkills } from './useSkills';

type Listener = (skills: DarvinSkillSummary[]) => void;

interface FakeDarvin {
  listSkills: ReturnType<typeof vi.fn>;
  setSkillEnabled: ReturnType<typeof vi.fn>;
  installSkill: ReturnType<typeof vi.fn>;
  uninstallSkill: ReturnType<typeof vi.fn>;
  upgradeSkill: ReturnType<typeof vi.fn>;
  onSkillsChanged: ReturnType<typeof vi.fn>;
  __listeners: Listener[];
  __emit: (next: DarvinSkillSummary[]) => void;
}

const SKILL: DarvinSkillSummary = {
  id: 'web-search',
  name: 'Web Search',
  description: '联网搜索',
  version: '0.1.0',
  enabled: true,
  isOfficial: true,
  isBuiltIn: true,
  path: 'bundled://web-search',
  source: 'bundled',
  updatedAt: 0,
};

function installFakeDarvin(): FakeDarvin {
  const fake: FakeDarvin = {
    listSkills: vi.fn(),
    setSkillEnabled: vi.fn(),
    installSkill: vi.fn(),
    uninstallSkill: vi.fn(),
    upgradeSkill: vi.fn(),
    onSkillsChanged: vi.fn(),
    __listeners: [],
    __emit(next) {
      for (const l of fake.__listeners) l(next);
    },
  };
  fake.listSkills.mockResolvedValue({ skills: [SKILL] });
  fake.setSkillEnabled.mockResolvedValue({ ok: true });
  fake.installSkill.mockResolvedValue({ skill: SKILL, riskLevel: 'safe' });
  fake.uninstallSkill.mockResolvedValue({ ok: true });
  fake.upgradeSkill.mockResolvedValue({ skill: { ...SKILL, version: '0.2.0' } });
  fake.onSkillsChanged.mockImplementation((handler: Listener) => {
    fake.__listeners.push(handler);
    return () => {
      const idx = fake.__listeners.indexOf(handler);
      if (idx >= 0) fake.__listeners.splice(idx, 1);
    };
  });
  (globalThis as unknown as { window: { darvin: unknown } }).window = { darvin: fake };
  return fake;
}

describe('useSkills', () => {
  let fake: FakeDarvin;

  beforeEach(() => {
    __resetSkillsForTest();
    fake = installFakeDarvin();
  });

  afterEach(() => {
    __resetSkillsForTest();
  });

  it('refresh pulls listSkills into skills.value', async () => {
    const { skills, loading, refresh } = useSkills();
    await refresh();
    expect(skills.value.map((s) => s.id)).toEqual(['web-search']);
    expect(loading.value).toBe(false);
  });

  it('setEnabled optimistically toggles, awaits darvin.setSkillEnabled', async () => {
    const { skills, setEnabled } = useSkills();
    await new Promise((r) => setTimeout(r, 10));
    await setEnabled('web-search', false);
    expect(skills.value.find((s) => s.id === 'web-search')?.enabled).toBe(false);
    expect(fake.setSkillEnabled).toHaveBeenCalledWith({ skillId: 'web-search', enabled: false });
  });

  it('setEnabled rolls back when darvin rejects', async () => {
    const { skills, setEnabled } = useSkills();
    await new Promise((r) => setTimeout(r, 10));
    fake.setSkillEnabled.mockRejectedValueOnce(new Error('boom'));
    await expect(setEnabled('web-search', false)).rejects.toThrow('boom');
    expect(skills.value.find((s) => s.id === 'web-search')?.enabled).toBe(true);
  });

  it('onSkillsChanged push overrides skills.value', async () => {
    const { skills } = useSkills();
    await new Promise((r) => setTimeout(r, 10));
    const next: DarvinSkillSummary[] = [
      { ...SKILL, id: 'code-review', name: 'Code Review' },
      { ...SKILL, id: 'testing', name: 'Testing' },
    ];
    fake.__emit(next);
    expect(skills.value.map((s) => s.id).sort()).toEqual(['code-review', 'testing']);
  });

  it('install calls darvin.installSkill and toasts success', async () => {
    const spy = vi.spyOn(await import('../services/toast'), 'showToast');
    const { install } = useSkills();
    const r = await install('/tmp/SKILL.md');
    expect(r?.skill.id).toBe('web-search');
    expect(fake.installSkill).toHaveBeenCalledWith({ source: '/tmp/SKILL.md' });
    expect(spy).toHaveBeenCalled();
    spy.mockRestore();
  });
});
