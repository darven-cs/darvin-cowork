import { describe, expect, it } from 'vitest';
import { AgentClient, parseDarvinEvent } from './client';

describe('parseDarvinEvent', () => {
  it('promotes toolUseId from Go message.id for tool_start', () => {
    const ev = parseDarvinEvent({
      type: 'tool_start',
      sessionId: 's1',
      tool: 'Bash',
      input: { command: 'ls' },
      message: { id: 'call-1' },
    });
    expect(ev).not.toBeNull();
    if (ev && ev.type === 'tool_start') {
      expect(ev.toolUseId).toBe('call-1');
      expect(ev.sessionId).toBe('s1');
      expect(ev.tool).toBe('Bash');
      expect(ev.input).toEqual({ command: 'ls' });
    }
  });

  it('falls back to messageId when Go message.id is absent', () => {
    const ev = parseDarvinEvent({ type: 'tool_start', messageId: 'm-7', tool: 'Read', input: {} });
    expect(ev).not.toBeNull();
    if (ev && ev.type === 'tool_start') {
      expect(ev.toolUseId).toBe('m-7');
      expect(ev.messageId).toBe('m-7');
    }
  });

  it('converges tool_end output from tool field (Go puts content there)', () => {
    const ev = parseDarvinEvent({
      type: 'tool_end',
      tool: 'total 12\ndrwxr-xr-x',
      message: { id: 'call-1' },
    });
    expect(ev).not.toBeNull();
    if (ev && ev.type === 'tool_end') {
      expect(ev.toolUseId).toBe('call-1');
      expect(ev.output).toBe('total 12\ndrwxr-xr-x');
    }
  });

  it('prefers explicit output field over tool fallback for tool_end', () => {
    const ev = parseDarvinEvent({
      type: 'tool_end',
      tool: 'Read',
      output: { ok: true },
      message: { id: 'call-2' },
    });
    expect(ev).not.toBeNull();
    if (ev && ev.type === 'tool_end') {
      expect(ev.toolUseId).toBe('call-2');
      expect(ev.output).toEqual({ ok: true });
    }
  });

  it('returns null for unknown types', () => {
    expect(parseDarvinEvent({ type: 'bogus_event' })).toBeNull();
  });

  it('passes through text_delta unchanged', () => {
    const ev = parseDarvinEvent({ type: 'text_delta', messageId: 'm-1', delta: 'hi' });
    expect(ev).toEqual({ type: 'text_delta', messageId: 'm-1', delta: 'hi' });
  });

  it('passes through compaction with before/after tokens', () => {
    const ev = parseDarvinEvent({
      type: 'compaction',
      sessionId: 's1',
      runId: 'r1',
      reason: 'manual',
      checkpointId: 'cp-1',
      createdAt: 1722522000000,
      beforeTokens: 80000,
      afterTokens: 30000,
    });
    expect(ev).not.toBeNull();
    if (ev && ev.type === 'compaction') {
      expect(ev.reason).toBe('manual');
      expect(ev.beforeTokens).toBe(80000);
      expect(ev.afterTokens).toBe(30000);
    }
  });
});

// agent.skills.changed 通知路由到 skills.onChanged 监听器,
// 不再走 agent.event 路径(避免和 session event 混淆)。
function makeClient(): AgentClient {
  return new AgentClient({ logger: { warn: () => {} } });
}

describe('AgentClient skills notifications', () => {
  it('onChanged receives skills payload from agent.skills.changed frames', () => {
    const client = makeClient();
    const received: { count: number; ids: string[] } = { count: 0, ids: [] };
    const off = client.skills.onChanged((skills) => {
      received.count += 1;
      received.ids = skills.map((s) => s.id);
    });
    (client as unknown as { handleIncoming: (msg: unknown) => void }).handleIncoming({
      jsonrpc: '2.0',
      method: 'agent.skills.changed',
      params: {
        skills: [
          { id: 'code-review', enabled: true },
          { id: 'web-search', enabled: false },
        ],
      },
    });
    expect(received.count).toBe(1);
    expect(received.ids).toEqual(['code-review', 'web-search']);
    off();
  });

  it('unsubscribe stops further notifications', () => {
    const client = makeClient();
    let count = 0;
    const off = client.skills.onChanged(() => {
      count += 1;
    });
    off();
    (client as unknown as { handleIncoming: (msg: unknown) => void }).handleIncoming({
      jsonrpc: '2.0',
      method: 'agent.skills.changed',
      params: { skills: [{ id: 'foo' }] },
    });
    expect(count).toBe(0);
  });

  it('skips on missing params', () => {
    const client = makeClient();
    let count = 0;
    client.skills.onChanged(() => {
      count += 1;
    });
    (client as unknown as { handleIncoming: (msg: unknown) => void }).handleIncoming({
      jsonrpc: '2.0',
      method: 'agent.skills.changed',
    });
    expect(count).toBe(0);
  });
});
