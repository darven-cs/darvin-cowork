/**
 * Mock agent runtime：模拟流式 event 序列。
 *
 * event 形状必须与 `DarvinEvent` union 完全一致（preload 不做转换）。
 */

import type { DarvinEvent } from '../../shared/darvin-api';

export interface MockPromptResult {
  sessionId: string;
  messageId: string;
  events: AsyncIterable<DarvinEvent>;
}

function newId(prefix: string): string {
  return `${prefix}-${Math.random().toString(36).slice(2, 10)}`;
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function* streamEvents(content: string, messageId: string): AsyncGenerator<DarvinEvent> {
  const reply = `Pong. Agent runtime is ready. You said: "${content}".`;
  // 50ms 间隔逐字符
  for (let i = 0; i < reply.length; i += 1) {
    await delay(50);
    yield { type: 'text_delta', messageId, delta: reply[i]! };
  }
  yield { type: 'done', messageId };
  yield { type: 'agent_end' };
}

export async function mockPrompt(content: string, sessionId?: string): Promise<MockPromptResult> {
  const sid = sessionId ?? newId('s');
  const mid = newId('m');
  return {
    sessionId: sid,
    messageId: mid,
    events: streamEvents(content, mid),
  };
}
