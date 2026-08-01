import { describe, expect, it } from 'vitest';
import {
  computeDiffLines,
  extractDiffFromToolInput,
  formatToolInput,
  getToolDisplayName,
  getToolKind,
  getToolResultCollapsedDisplay,
  parseTodoWriteItems,
} from './toolDisplay';

describe('getToolDisplayName', () => {
  it('normalizes file tool aliases to short names', () => {
    expect(getToolDisplayName('Read')).toBe('Read');
    expect(getToolDisplayName('ReadFile')).toBe('Read');
    expect(getToolDisplayName('WriteFile')).toBe('Write');
    expect(getToolDisplayName('EditFile')).toBe('Edit');
  });

  it('normalizes bash aliases', () => {
    expect(getToolDisplayName('Bash')).toBe('Bash');
    expect(getToolDisplayName('Exec')).toBe('Bash');
    expect(getToolDisplayName('Shell')).toBe('Bash');
    expect(getToolDisplayName('Run')).toBe('Bash');
  });

  it('normalizes web aliases', () => {
    expect(getToolDisplayName('WebSearch')).toBe('Web Search');
    expect(getToolDisplayName('web_fetch')).toBe('Web Fetch');
  });

  it('falls back to the raw name for unknown tools', () => {
    expect(getToolDisplayName('my_custom_tool')).toBe('my_custom_tool');
    expect(getToolDisplayName(undefined)).toBe('Tool');
  });
});

describe('getToolKind', () => {
  it('maps aliases to DarvinToolKind', () => {
    expect(getToolKind('ReadFile')).toBe('read');
    expect(getToolKind('Exec')).toBe('bash');
    expect(getToolKind('Shell')).toBe('bash');
    expect(getToolKind('WebSearch')).toBe('web_search');
    expect(getToolKind('WebFetch')).toBe('web_fetch');
    expect(getToolKind('TodoWrite')).toBe('todowrite');
  });

  it('keeps known kinds as-is', () => {
    expect(getToolKind('bash')).toBe('bash');
    expect(getToolKind('read')).toBe('read');
  });

  it('falls back to raw string for unknown tools', () => {
    expect(getToolKind('custom_tool')).toBe('custom_tool');
  });
});

describe('getToolResultCollapsedDisplay', () => {
  it('formats short string output', () => {
    const d = getToolResultCollapsedDisplay('total 12\ndrwxr-xr-x');
    expect(d.preview).toBe('total 12\ndrwxr-xr-x');
    expect(d.sizeLabel).toBe('19 B');
    expect(d.lineCount).toBe(2);
    expect(d.isTruncated).toBe(false);
  });

  it('stringifies object output as pretty JSON', () => {
    const d = getToolResultCollapsedDisplay({ ok: true });
    expect(d.preview).toBe('{\n  "ok": true\n}');
    expect(d.isTruncated).toBe(false);
  });

  it('truncates >4KB with KB size summary and first 12 lines', () => {
    const big = `${Array.from({ length: 20 }, () => 'line').join('\n')}`.padEnd(5 * 1024, 'x');
    const d = getToolResultCollapsedDisplay(big);
    expect(d.isTruncated).toBe(true);
    expect(d.sizeLabel).toBe('5 KB');
    expect(d.lineCount).toBe(20);
    expect(d.preview.split('\n').length).toBe(12);
  });

  it('empty output is not truncated', () => {
    const d = getToolResultCollapsedDisplay('');
    expect(d.isTruncated).toBe(false);
    expect(d.sizeLabel).toBe('0 B');
  });
});

describe('formatToolInput', () => {
  it('returns bash command summary', () => {
    expect(formatToolInput('Bash', { command: 'ls -la' })).toBe('ls -la');
  });

  it('returns file path for edit', () => {
    expect(formatToolInput('Edit', { file_path: 'foo.ts', old_string: 'a', new_string: 'b' })).toBe('foo.ts');
  });

  it('falls back to JSON for unknown input', () => {
    expect(formatToolInput('MyTool', { a: 1 })).toBe('{\n  "a": 1\n}');
  });
});

describe('parseTodoWriteItems', () => {
  it('parses three-state todos', () => {
    const items = parseTodoWriteItems({
      todos: [
        { activeForm: 'write spec', status: 'completed' },
        { content: 'implement', activeForm: 'implement', status: 'in_progress' },
        { content: 'test' },
      ],
    });
    expect(items).not.toBeNull();
    expect(items).toHaveLength(3);
    expect(items![0].status).toBe('completed');
    expect(items![1].status).toBe('in_progress');
    expect(items![2].status).toBe('pending');
    expect(items![1].primaryText).toBe('implement');
    expect(items![1].secondaryText).toBeNull();
  });

  it('returns null when no todos array', () => {
    expect(parseTodoWriteItems({ command: 'ls' })).toBeNull();
    expect(parseTodoWriteItems(null)).toBeNull();
  });
});

describe('computeDiffLines / extractDiffFromToolInput', () => {
  it('computes red/green lines for a simple diff', () => {
    const lines = computeDiffLines('a\nb\nc', 'a\nB\nc');
    expect(lines.filter((l) => l.type === 'removed').map((l) => l.text)).toContain('b');
    expect(lines.filter((l) => l.type === 'added').map((l) => l.text)).toContain('B');
    expect(lines.filter((l) => l.type === 'context')).toHaveLength(2);
  });

  it('extracts old/new from edit input', () => {
    const diffs = extractDiffFromToolInput('Edit', {
      file_path: 'foo.ts',
      old_string: 'a',
      new_string: 'b',
    });
    expect(diffs).toEqual([{ filePath: 'foo.ts', oldStr: 'a', newStr: 'b' }]);
  });

  it('returns null for non-edit tools', () => {
    expect(extractDiffFromToolInput('Bash', { command: 'ls' })).toBeNull();
  });
});
