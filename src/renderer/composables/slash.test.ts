import { describe, expect, it } from 'vitest';
import { parseSlashCommand, translateSkillError } from './slash';

describe('parseSlashCommand', () => {
  it('parses /skill-name with args', () => {
    expect(parseSlashCommand('/code-review src/api/handler.go')).toEqual({
      skillId: 'code-review',
      args: 'src/api/handler.go',
    });
  });

  it('parses /skill-name without args', () => {
    expect(parseSlashCommand('/code-review')).toEqual({ skillId: 'code-review', args: '' });
    expect(parseSlashCommand('/code-review ')).toEqual({ skillId: 'code-review', args: '' });
  });

  it('parses only the first line (attachment lines ignored)', () => {
    expect(parseSlashCommand('/code-review\n文件: /tmp/a.go')).toEqual({
      skillId: 'code-review',
      args: '',
    });
  });

  it('returns null for bare slash', () => {
    expect(parseSlashCommand('/')).toBeNull();
    expect(parseSlashCommand('/ ')).toBeNull();
  });

  it('returns null for escaped // prefix (would re-trigger otherwise)', () => {
    expect(parseSlashCommand('//skill-name is a library')).toBeNull();
  });

  it('returns null when slash is not at the start', () => {
    expect(parseSlashCommand('hello /code-review')).toBeNull();
  });

  it('rejects uppercase first char', () => {
    expect(parseSlashCommand('/Code-Review x')).toBeNull();
  });

  it('allows digit-first + hyphenated skill id (matches Go regex)', () => {
    expect(parseSlashCommand('/9-lives x')).toEqual({ skillId: '9-lives', args: 'x' });
  });
});

describe('translateSkillError', () => {
  it('maps Go error messages to i18n text', () => {
    expect(translateSkillError(new Error('skill: not found'), 'nope')).toBe('Skill 不存在：nope');
    expect(translateSkillError(new Error('skill: disabled'), 'web-search')).toBe('Skill 已禁用：web-search');
    expect(translateSkillError(new Error('skill: not user invocable'), 'secret')).toBe('Skill 不可手动触发：secret');
  });

  it('falls back to unknown for unrecognized errors', () => {
    const msg = translateSkillError(new Error('boom'), 'code-review');
    expect(msg).toContain('触发失败');
    expect(msg).toContain('boom');
  });
});
