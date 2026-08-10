/**
 * useSubagents 纯逻辑测试：不挂 Vue component，直接在 module 级 ref +
 * 假 darvin client 上验证 refreshList / loadMessages / selectRun /
 * 轮询启停 / session 切换重置。
 */

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { SubagentMessage, SubagentRun } from '../../shared/darvin-api';
import { __resetSubagentsForTest, useSubagents } from './useSubagents';
import { useSession } from './useSession';

const RUN: SubagentRun = {
  id: 'sess/sub/abc',
  parentId: 'sess',
  status: 'done',
  prompt: 'do thing',
  description: 'thing',
  scope: [],
  model: '',
  startedAt: 1000,
  endedAt: 2000,
  toolCalls: 1,
  durationMs: 1000,
};

const RUNNING: SubagentRun = { ...RUN, id: 'sess/sub/def', status: 'running', durationMs: 0 };

interface FakeDarvin {
  subagentList: ReturnType<typeof vi.fn>;
  subagentGetMessages: ReturnType<typeof vi.fn>;
  onSessionsChanged: ReturnType<typeof vi.fn>;
  onActiveSessionChanged: ReturnType<typeof vi.fn>;
}

function installFakeDarvin(): FakeDarvin {
  const fake: FakeDarvin = {
    subagentList: vi.fn(),
    subagentGetMessages: vi.fn(),
    onSessionsChanged: vi.fn(),
    onActiveSessionChanged: vi.fn(),
  };
  fake.subagentList.mockResolvedValue({ subagents: [RUN] });
  fake.subagentGetMessages.mockResolvedValue({
    messages: [
      { id: 'm1', sessionId: 'sess/sub/abc', role: 'user', content: 'hi', createdAt: 1 },
    ] as SubagentMessage[],
  });
  (globalThis as unknown as { window: { darvin: unknown } }).window = { darvin: fake };
  return fake;
}

function setActiveSession(id: string | null): void {
  useSession().activeSessionId.value = id;
}

beforeEach(() => {
  __resetSubagentsForTest();
  installFakeDarvin();
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
  delete (globalThis as unknown as { window?: { darvin?: unknown } }).window;
});

describe('useSubagents', () => {
  it('refreshList 拉列表并写入 runs', async () => {
    const fake = installFakeDarvin();
    setActiveSession('sess');
    const sub = useSubagents();
    await sub.refreshList();
    expect(fake.subagentList).toHaveBeenCalledWith('sess');
    expect(sub.runs.value).toEqual([RUN]);
  });

  it('selectRun 设置 selectedId 并拉消息（缓存缺失时）', async () => {
    const fake = installFakeDarvin();
    const sub = useSubagents();
    sub.selectRun('sess/sub/abc');
    expect(sub.selectedId.value).toBe('sess/sub/abc');
    await vi.runAllTimersAsync();
    expect(fake.subagentGetMessages).toHaveBeenCalledWith('sess/sub/abc');
    expect(sub.messagesByRun.value['sess/sub/abc']).toHaveLength(1);
  });

  it('selectRun(null) 仅清 selectedId，不拉消息', async () => {
    installFakeDarvin();
    const sub = useSubagents();
    sub.selectRun(null);
    expect(sub.selectedId.value).toBeNull();
  });

  it('running run 存在 → 启动轮询；全部终态 → 停止', async () => {
    const fake = installFakeDarvin();
    fake.subagentList.mockResolvedValue({ subagents: [RUNNING] });
    const sub = useSubagents();
    sub.runs.value = [RUNNING];
    await vi.advanceTimersByTimeAsync(5_000);
    expect(fake.subagentList.mock.calls.length).toBeGreaterThanOrEqual(1);

    // 全部终态后停止轮询
    const callsAtStop = fake.subagentList.mock.calls.length;
    fake.subagentList.mockResolvedValue({ subagents: [RUN] });
    sub.runs.value = [RUN];
    await vi.advanceTimersByTimeAsync(15_000);
    expect(fake.subagentList.mock.calls.length).toBe(callsAtStop);
  });

  it('session 切换 → 重置 selectedId/messages 并重新拉新 session 的 runs', async () => {
    const fake = installFakeDarvin();
    const sub = useSubagents();
    sub.runs.value = [RUN];
    sub.selectedId.value = RUN.id;
    sub.messagesByRun.value = { [RUN.id]: [] };
    setActiveSession('other-session');
    // 先 flush 重置（selectedId / messages 清空），再等 refresh 落地新 session 数据
    await Promise.resolve();
    await Promise.resolve();
    expect(sub.selectedId.value).toBeNull();
    expect(sub.messagesByRun.value).toEqual({});
    // refresh 用新 sessionId 调 list，把 mock 返回的 runs 填回来
    expect(fake.subagentList).toHaveBeenCalledWith('other-session');
    expect(sub.runs.value).toEqual([RUN]);
  });

  it('显示名：description 优先，空 description 用 id 短前缀', () => {
    installFakeDarvin();
    const sub = useSubagents();
    expect(sub.getSubagentDisplayName(RUN)).toBe('thing');
    const noDesc = { ...RUN, description: '' };
    expect(sub.getSubagentDisplayName(noDesc)).toBe('sess/sub');
    expect(sub.getSubagentDisplayInitial(noDesc)).toBe('S');
  });
});
