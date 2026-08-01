import { describe, expect, it } from 'vitest';
import { parseDarvinEvent } from './client';

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
    if (ev) {
      expect(ev.type).toBe('tool_start');
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
});
