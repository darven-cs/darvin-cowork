import { beforeEach, describe, expect, it } from 'vitest';
import { buildConversationTurns, useMessages } from './useMessages';

const messages = useMessages();

beforeEach(() => {
  messages.reset();
});

describe('useMessages tool event pairing', () => {
  it('creates a tool_use message from tool_start event', () => {
    messages.appendEvent({
      type: 'tool_start',
      sessionId: 's1',
      messageId: 'call-1',
      toolUseId: 'call-1',
      tool: 'Bash',
      input: { command: 'ls -la' },
    });
    const bucket = messages.messagesBySessionId.value.s1;
    expect(bucket).toHaveLength(1);
    expect(bucket[0].kind).toBe('tool_use');
    expect(bucket[0].toolUseId).toBe('call-1');
    expect(bucket[0].tool).toBe('Bash');
    expect(bucket[0].toolKind).toBe('bash');
    expect(bucket[0].input).toEqual({ command: 'ls -la' });
    expect(bucket[0].done).toBe(false);
  });

  it('pairs tool_end to the matching tool_use by toolUseId', () => {
    messages.appendEvent({ type: 'tool_start', sessionId: 's1', messageId: 'call-1', toolUseId: 'call-1', tool: 'Bash', input: {} });
    messages.appendEvent({ type: 'tool_end', sessionId: 's1', messageId: 'call-1', toolUseId: 'call-1', tool: 'total 12', output: 'total 12' });
    const bucket = messages.messagesBySessionId.value.s1;
    expect(bucket).toHaveLength(2);
    expect(bucket[1].kind).toBe('tool_result');
    expect(bucket[1].output).toBe('total 12');
    expect(bucket[1].isError).toBe(false);

    const turns = buildConversationTurns(bucket);
    expect(turns).toHaveLength(1);
    expect(turns[0].assistantItems).toHaveLength(1);
    const item = turns[0].assistantItems[0];
    if (item.type !== 'tool_group') throw new Error('expected tool_group');
    expect(item.toolUse.tool).toBe('Bash');
    expect(item.toolResult?.output).toBe('total 12');
  });

  it('infers error from tool_end output (object)', () => {
    messages.appendEvent({ type: 'tool_start', sessionId: 's1', messageId: 'c1', toolUseId: 'c1', tool: 'Bash', input: {} });
    messages.appendEvent({
      type: 'tool_end', sessionId: 's1', messageId: 'c1', toolUseId: 'c1', tool: '',
      output: { exitCode: 1, stderr: 'command not found' },
    });
    const bucket = messages.messagesBySessionId.value.s1;
    expect(bucket[1].isError).toBe(true);
  });

  it('infers error from darvin-agent string error texts', () => {
    const errorTexts = [
      'tool "shell": argument "command" must be one of [awk cat], got nonexistent_cmd_xyz',
      'command not allowed: nonexistent_cmd_xyz',
      'bash: nonexistent_cmd_xyz: command not found',
      'read: /tmp/a: no such file or directory',
      'old_text not found in foo.ts',
    ];
    for (const text of errorTexts) {
      messages.reset();
      messages.appendEvent({ type: 'tool_start', sessionId: 's1', messageId: 'c1', toolUseId: 'c1', tool: 'Bash', input: {} });
      messages.appendEvent({ type: 'tool_end', sessionId: 's1', messageId: 'c1', toolUseId: 'c1', tool: '', output: text });
      expect(messages.messagesBySessionId.value.s1[1].isError, text).toBe(true);
    }
  });

  it('does not flag ordinary shell output as error', () => {
    messages.appendEvent({ type: 'tool_start', sessionId: 's1', messageId: 'c1', toolUseId: 'c1', tool: 'Bash', input: {} });
    messages.appendEvent({ type: 'tool_end', sessionId: 's1', messageId: 'c1', toolUseId: 'c1', tool: '', output: 'total 12\ndrwxr-xr-x 2 user user 4096 .' });
    expect(messages.messagesBySessionId.value.s1[1].isError).toBe(false);
  });

  it('tool_end is idempotent for repeated tool_start', () => {
    messages.appendEvent({ type: 'tool_start', sessionId: 's1', messageId: 'c1', toolUseId: 'c1', tool: 'Read', input: { path: '/tmp/a' } });
    messages.appendEvent({ type: 'tool_start', sessionId: 's1', messageId: 'c1', toolUseId: 'c1', tool: 'Read', input: { path: '/tmp/a' } });
    const bucket = messages.messagesBySessionId.value.s1;
    expect(bucket.filter((m) => m.kind === 'tool_use')).toHaveLength(1);
  });

  it('marks session streaming on tool_start and clears on agent_end', () => {
    messages.appendEvent({ type: 'tool_start', sessionId: 's1', messageId: 'c1', toolUseId: 'c1', tool: 'Bash', input: {} });
    expect(messages.streamingSessionIds.value.has('s1')).toBe(true);
    messages.appendEvent({ type: 'agent_end', sessionId: 's1' });
    expect(messages.streamingSessionIds.value.has('s1')).toBe(false);
  });

  it('keeps multiple tool calls grouped as separate tool_groups', () => {
    messages.appendEvent({ type: 'tool_start', sessionId: 's1', messageId: 'c1', toolUseId: 'c1', tool: 'Bash', input: {} });
    messages.appendEvent({ type: 'tool_end', sessionId: 's1', messageId: 'c1', toolUseId: 'c1', tool: 'Bash', output: 'a' });
    messages.appendEvent({ type: 'tool_start', sessionId: 's1', messageId: 'c2', toolUseId: 'c2', tool: 'Read', input: {} });
    messages.appendEvent({ type: 'tool_end', sessionId: 's1', messageId: 'c2', toolUseId: 'c2', tool: 'Read', output: 'b' });
    const bucket = messages.messagesBySessionId.value.s1;
    const turns = buildConversationTurns(bucket);
    expect(turns[0].assistantItems).toHaveLength(2);
    const [g1, g2] = turns[0].assistantItems as Array<{ type: 'tool_group'; toolUse: { tool?: string } }>;
    expect(g1.toolUse.tool).toBe('Bash');
    expect(g2.toolUse.tool).toBe('Read');
  });
});

describe('useMessages context usage (spec 03)', () => {
  it('records context_usage event into contextUsageBySessionId', () => {
    messages.appendEvent({
      type: 'context_usage',
      sessionId: 's1',
      usage: {
        sessionId: 's1',
        usedTokens: 45_000,
        contextTokens: 100_000,
        percent: 45,
        status: 'normal',
        updatedAt: 1,
      },
    });
    const cu = messages.contextUsageBySessionId.value.s1;
    expect(cu).toBeDefined();
    expect(cu.percent).toBe(45);
    expect(cu.status).toBe('normal');
  });

  it('keys by usage.sessionId when present, falling back to event sessionId', () => {
    messages.appendEvent({
      type: 'context_usage',
      sessionId: 's2',
      usage: { sessionId: 's2', status: 'warning', percent: 70, updatedAt: 1 },
    });
    messages.appendEvent({
      type: 'context_usage',
      sessionId: 's3',
      usage: { status: 'danger', percent: 92, updatedAt: 1 },
    });
    expect(messages.contextUsageBySessionId.value.s2.status).toBe('warning');
    expect(messages.contextUsageBySessionId.value.s3.status).toBe('danger');
  });

  it('overwrites previous usage snapshot for the same session', () => {
    messages.appendEvent({ type: 'context_usage', sessionId: 's1', usage: { sessionId: 's1', status: 'normal', percent: 30, updatedAt: 1 } });
    messages.appendEvent({ type: 'context_usage', sessionId: 's1', usage: { sessionId: 's1', status: 'compacting', percent: 100, updatedAt: 2 } });
    expect(messages.contextUsageBySessionId.value.s1.status).toBe('compacting');
    expect(messages.contextUsageBySessionId.value.s1.percent).toBe(100);
  });

  it('does not mark session as unread on context_usage', () => {
    messages.appendEvent({ type: 'context_usage', sessionId: 's9', usage: { sessionId: 's9', status: 'normal', percent: 10, updatedAt: 1 } });
    expect(messages.unreadSessionIds.value.has('s9')).toBe(false);
  });

  it('clears context usage on removeSession and reset', () => {
    messages.appendEvent({ type: 'context_usage', sessionId: 's1', usage: { sessionId: 's1', status: 'normal', percent: 40, updatedAt: 1 } });
    messages.removeSession('s1');
    expect(messages.contextUsageBySessionId.value.s1).toBeUndefined();

    messages.appendEvent({ type: 'context_usage', sessionId: 's2', usage: { sessionId: 's2', status: 'normal', percent: 40, updatedAt: 1 } });
    messages.reset();
    expect(messages.contextUsageBySessionId.value.s2).toBeUndefined();
  });

  it('writes usage onto the message from done event', () => {
    messages.startAssistantMessage('s1', 'm1');
    messages.appendEvent({
      type: 'done',
      sessionId: 's1',
      messageId: 'm1',
      usage: { inputTokens: 1200, outputTokens: 300, cacheReadTokens: 500, totalTokens: 2000 },
    });
    const msg = messages.messagesBySessionId.value.s1[0];
    expect(msg.done).toBe(true);
    expect(msg.usage?.inputTokens).toBe(1200);
    expect(msg.usage?.cacheReadTokens).toBe(500);
  });

  it('leaves usage undefined when done event has no usage', () => {
    messages.startAssistantMessage('s1', 'm1');
    messages.appendEvent({ type: 'done', sessionId: 's1', messageId: 'm1' });
    expect(messages.messagesBySessionId.value.s1[0].usage).toBeUndefined();
  });
});
