/**
 * spec 37 — McpServerFormModal 的纯文本解析 helpers 测试。
 *
 * vitest 仅 include *.test.ts，Vue 组件不挂测试；spec 33 SkillCard.test.ts
 * 也是同样处理。这里测 formModal 的纯函数（args / env / headers 解析）。
 */

import { describe, expect, it } from 'vitest';
import { formatKv, parseArgs, parseKv } from './mcpForm';

describe('parseArgs', () => {
  it('splits on whitespace and drops empties', () => {
    expect(parseArgs('-y @scope/pkg@latest')).toEqual(['-y', '@scope/pkg@latest']);
  });

  it('collapses multi-space runs', () => {
    expect(parseArgs('-y   foo\tbar')).toEqual(['-y', 'foo', 'bar']);
  });

  it('returns empty for empty input', () => {
    expect(parseArgs('')).toEqual([]);
    expect(parseArgs('   ')).toEqual([]);
  });
});

describe('parseKv', () => {
  it('parses one KEY=val per line', () => {
    expect(parseKv('A=1\nB=2')).toEqual({ A: '1', B: '2' });
  });

  it('skips blank lines and # comments', () => {
    expect(parseKv('A=1\n\n# comment\nB=2')).toEqual({ A: '1', B: '2' });
  });

  it('skips lines without = or with empty key', () => {
    expect(parseKv('=novalue\nNOEQ\n=foo')).toEqual({});
  });

  it('preserves CRLF', () => {
    expect(parseKv('A=1\r\nB=2')).toEqual({ A: '1', B: '2' });
  });

  it('trims surrounding whitespace per entry', () => {
    expect(parseKv('  A = 1 \n  B = 2 ')).toEqual({ A: '1', B: '2' });
  });
});

describe('formatKv', () => {
  it('renders one KEY=val per line in insertion order', () => {
    expect(formatKv({ A: '1', B: '2' })).toBe('A=1\nB=2');
  });

  it('returns empty string for empty object', () => {
    expect(formatKv({})).toBe('');
  });
});