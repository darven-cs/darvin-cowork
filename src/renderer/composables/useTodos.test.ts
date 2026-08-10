import { beforeEach, describe, expect, it } from 'vitest';
import { useTodos, type TodoItem, type TodoSignOff } from './useTodos';
import { useMessages, type Message } from './useMessages';
import { useSession } from './useSession';

const todos = useTodos();
const messages = useMessages();
const session = useSession();

function toolUseMsg(sid: string, tool: string, input: unknown, createdAt: number, id: string): Message {
  return {
    id,
    sessionId: sid,
    role: 'assistant',
    content: '',
    done: true,
    kind: 'tool_use',
    toolUseId: id,
    tool,
    input,
    createdAt,
  };
}

function todoWrite(sid: string, items: TodoItem[], createdAt: number): Message {
  return toolUseMsg(sid, 'todo_write', { todos: items }, createdAt, `w-${createdAt}`);
}

function completeStep(sid: string, content: string, evidence: unknown[], createdAt: number): Message {
  return toolUseMsg(sid, 'complete_step', { step_id: createdAt, content, evidence }, createdAt, `c-${createdAt}`);
}

function setBucket(sid: string, msgs: Message[]): void {
  messages.messagesBySessionId.value = { ...messages.messagesBySessionId.value, [sid]: msgs };
}

beforeEach(() => {
  messages.reset();
  session.activeSessionId.value = 's1';
});

describe('useTodos derivation', () => {
  it('returns empty state when no todo_write', () => {
    setBucket('s1', []);
    expect(todos.hasList.value).toBe(false);
    expect(todos.items.value).toEqual([]);
    expect(todos.signOffs.value).toEqual([]);
    expect(todos.updatedAt.value).toBe(0);
  });

  it('takes the last todo_write as the current list', () => {
    setBucket('s1', [
      todoWrite('s1', [{ content: 'A', status: 'pending', level: 0 }], 100),
      todoWrite('s1', [{ content: 'B', status: 'in_progress', activeForm: 'Doing B', level: 0 }], 200),
    ]);
    expect(todos.hasList.value).toBe(true);
    expect(todos.items.value).toEqual([
      { content: 'B', status: 'in_progress', activeForm: 'Doing B', level: 0 },
    ]);
    expect(todos.updatedAt.value).toBe(200);
  });

  it('todos: [] keeps hasList true with empty items', () => {
    setBucket('s1', [todoWrite('s1', [], 100)]);
    expect(todos.hasList.value).toBe(true);
    expect(todos.items.value).toEqual([]);
  });

  it('collects complete_step sign-offs and dedupes by content, last wins', () => {
    setBucket('s1', [
      todoWrite('s1', [
        { content: 'A', status: 'pending', level: 0 },
        { content: 'B', status: 'pending', level: 0 },
      ], 100),
      completeStep('s1', 'A', [{ kind: 'test', description: 't1' }], 200),
      completeStep('s1', 'B', [{ kind: 'diff', description: 'd1' }, { kind: 'test', description: 't2' }], 300),
      completeStep('s1', 'A', [{ kind: 'test', description: 't3' }, { kind: 'test', description: 't4' }], 400),
    ]);
    expect(todos.signOffs.value).toHaveLength(2);
    const a = todos.signOffs.value.find((s) => s.content === 'A') as TodoSignOff;
    expect(a.evidenceCount).toBe(2);
    expect(a.createdAt).toBe(400);
    const b = todos.signOffs.value.find((s) => s.content === 'B') as TodoSignOff;
    expect(b.evidenceCount).toBe(2);
  });

  it('ignores complete_step with blank content', () => {
    setBucket('s1', [completeStep('s1', '   ', [{ kind: 'test', description: 'x' }], 100)]);
    expect(todos.signOffs.value).toEqual([]);
  });

  it('switches with the active session', () => {
    setBucket('s1', [todoWrite('s1', [{ content: 'A', status: 'pending', level: 0 }], 100)]);
    setBucket('s2', [todoWrite('s2', [{ content: 'B', status: 'pending', level: 0 }], 200)]);
    expect(todos.items.value.map((i) => i.content)).toEqual(['A']);
    session.activeSessionId.value = 's2';
    expect(todos.items.value.map((i) => i.content)).toEqual(['B']);
    expect(todos.updatedAt.value).toBe(200);
  });

  it('normalizes bad status to pending and missing level to 0', () => {
    setBucket('s1', [todoWrite('s1', [{ content: 'X', status: 'weird' } as unknown as TodoItem], 100)]);
    expect(todos.items.value[0]).toMatchObject({ content: 'X', status: 'pending', level: 0 });
  });

  it('ignores user messages and other tools', () => {
    setBucket('s1', [
      { id: 'u', sessionId: 's1', role: 'user', content: 'hi', done: true, createdAt: 10 },
      toolUseMsg('s1', 'bash', { command: 'ls' }, 20, 'b1'),
    ]);
    expect(todos.hasList.value).toBe(false);
    expect(todos.signOffs.value).toEqual([]);
  });
});
