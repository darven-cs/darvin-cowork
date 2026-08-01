import { describe, expect, it } from 'vitest';
import {
  assertNever,
  darvinMessageContent,
  darvinMessageRole,
  type DarvinMessage,
} from './darvin-api';

/** 老 Go 的扁平 wire shape（role 在顶层，无 type 判别）。 */
const legacy = (m: Record<string, unknown>): DarvinMessage =>
  m as unknown as DarvinMessage;

describe('darvinMessageRole', () => {
  it('maps legacy flat wire by role', () => {
    expect(darvinMessageRole(legacy({ role: 'user' }))).toBe('user');
    expect(darvinMessageRole(legacy({ role: 'assistant' }))).toBe('assistant');
  });

  it('folds legacy system notes into assistant', () => {
    expect(darvinMessageRole(legacy({ role: 'system' }))).toBe('assistant');
  });

  it('maps union members by type', () => {
    expect(darvinMessageRole({ type: 'user', content: '', done: true, id: '1', sessionId: 's', createdAt: 0 })).toBe('user');
    expect(darvinMessageRole({ type: 'assistant', content: '', isStreaming: false, done: true, id: '1', sessionId: 's', createdAt: 0 })).toBe('assistant');
    expect(darvinMessageRole({ type: 'tool_use', toolUseId: 't', tool: 'Bash', toolKind: 'bash', input: {}, id: '1', sessionId: 's', createdAt: 0 })).toBe('assistant');
    expect(darvinMessageRole({ type: 'tool_result', toolUseId: 't', tool: 'Bash', output: '', isError: false, id: '1', sessionId: 's', createdAt: 0 })).toBe('assistant');
    expect(darvinMessageRole({ type: 'system', content: 'note', id: '1', sessionId: 's', createdAt: 0 })).toBe('assistant');
  });
});

describe('darvinMessageContent', () => {
  it('reads content from legacy flat wire', () => {
    expect(darvinMessageContent(legacy({ role: 'user', content: 'hello' }))).toBe('hello');
  });

  it('reads content from text union members', () => {
    expect(darvinMessageContent({ type: 'assistant', content: 'hi', isStreaming: false, done: true, id: '1', sessionId: 's', createdAt: 0 })).toBe('hi');
    expect(darvinMessageContent({ type: 'system', content: 'note', id: '1', sessionId: 's', createdAt: 0 })).toBe('note');
  });

  it('renders tool messages by kind', () => {
    expect(darvinMessageContent({ type: 'tool_use', toolUseId: 't', tool: 'Bash', toolKind: 'bash', input: {}, id: '1', sessionId: 's', createdAt: 0 })).toBe('[Bash]');
    expect(darvinMessageContent({ type: 'tool_result', toolUseId: 't', tool: 'Read', output: '', isError: false, id: '1', sessionId: 's', createdAt: 0 })).toBe('[Read]');
  });
});

describe('assertNever', () => {
  it('throws on unexpected value', () => {
    expect(() => assertNever('unexpected' as never)).toThrow('unhandled union member');
  });
});
