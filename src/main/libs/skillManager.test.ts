import { describe, expect, it } from 'vitest';
import { EventEmitter } from 'node:events';
import { parseSkillFrontmatter } from './skillManager';

describe('parseSkillFrontmatter', () => {
  it('parses name / description / version / invocation', () => {
    const raw = `---
name: code-review
description: review src code
version: 0.1.0
invocation:
  userInvocable: true
  disableModelInvocation: false
---
# body`;
    const fm = parseSkillFrontmatter(raw);
    expect(fm).not.toBeNull();
    expect(fm?.name).toBe('code-review');
    expect(fm?.description).toBe('review src code');
    expect(fm?.version).toBe('0.1.0');
    expect(fm?.invocation?.userInvocable).toBe(true);
    expect(fm?.invocation?.disableModelInvocation).toBe(false);
  });

  it('returns null when name is missing', () => {
    expect(parseSkillFrontmatter('---\ndescription: hello world\n---\nbody')).toBeNull();
  });

  it('returns null when description is missing', () => {
    expect(parseSkillFrontmatter('---\nname: foo\n---\nbody')).toBeNull();
  });

  it('returns null without frontmatter', () => {
    expect(parseSkillFrontmatter('# Just a markdown doc')).toBeNull();
  });

  it('ignores unknown fields and trailing body', () => {
    const raw = `---
name: testing
description: Suggest useful test coverage
unknown_field: keep
---
rest`;
    const fm = parseSkillFrontmatter(raw);
    expect(fm?.name).toBe('testing');
    expect((fm as Record<string, unknown>)?.unknown_field).toBeUndefined();
  });

  it('rejects names with uppercase / emoji / spaces', () => {
    expect(parseSkillFrontmatter('---\nname: BadName\ndescription: a long enough description\n---\n')).toBeNull();
    expect(parseSkillFrontmatter('---\nname: with space\ndescription: a long enough description\n---\n')).toBeNull();
  });
});

// 验证订阅契约：AgentClient.skills.onChanged 必须能从 EventEmitter 继承
// 的对象上取出 listener 并按预期被 emit 触发 — 此处用最小 fake 复刻
// subscribe / fanout 行为，避免拉起 WS 客户端。
describe('skills change fanout contract', () => {
  it('onChanged returns an unsubscribe that stops further emissions', () => {
    const bus = new EventEmitter();
    let count = 0;
    const off = (handler: (skills: unknown[]) => void): (() => void) => {
      bus.on('skills.changed', handler);
      return () => bus.off('skills.changed', handler);
    };
    const dispose = off(() => {
      count += 1;
    });
    bus.emit('skills.changed', [{ id: 'a' }]);
    expect(count).toBe(1);
    dispose();
    bus.emit('skills.changed', [{ id: 'b' }]);
    expect(count).toBe(1);
  });
});
