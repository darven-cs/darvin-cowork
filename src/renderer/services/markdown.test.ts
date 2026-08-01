import { describe, expect, it } from 'vitest';
import {
  formatContentSize,
  getLargeMarkdownPreview,
  renderMarkdownSegments,
  shouldUseLargeMarkdownPreview,
} from './markdown';

describe('markdown truncation helpers', () => {
  it('marks content over 8KB as large preview', () => {
    expect(shouldUseLargeMarkdownPreview('a'.repeat(8192))).toBe(false);
    expect(shouldUseLargeMarkdownPreview('a'.repeat(8193))).toBe(true);
  });

  it('returns content as-is when under head+tail budget', () => {
    const content = 'abc';
    expect(getLargeMarkdownPreview(content)).toBe(content);
  });

  it('slices head 4KB + tail 8KB with ... placeholder', () => {
    const content = 'A'.repeat(4096) + 'B'.repeat(4096) + 'C'.repeat(8192);
    const preview = getLargeMarkdownPreview(content);
    expect(preview).toContain('...');
    expect(preview.startsWith('A'.repeat(4096))).toBe(true);
    expect(preview.endsWith('C'.repeat(8192))).toBe(true);
  });

  it('formats KB and MB sizes', () => {
    expect(formatContentSize(1024)).toBe('1 KB');
    expect(formatContentSize(12 * 1024 * 1024)).toBe('12.0 MB');
  });
});

describe('renderMarkdownSegments', () => {
  it('extracts fenced code blocks into code segments', () => {
    const segments = renderMarkdownSegments('# T\n\n```ts\nconst a = 1;\n```\n\nafter');
    const codes = segments.filter((s) => s.type === 'code');
    expect(codes).toHaveLength(1);
    if (codes[0].type === 'code') {
      expect(codes[0].lang).toBe('ts');
      expect(codes[0].code).toBe('const a = 1;\n');
    }
    const html = segments.filter((s) => s.type === 'html').map((s) => s.html).join('');
    expect(html).toContain('<h1>T</h1>');
    expect(html).toContain('<p>after</p>');
  });

  it('renders tables, task lists and katex inline math', () => {
    const segments = renderMarkdownSegments('- [x] done\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n$E = mc^2$');
    const html = segments.filter((s) => s.type === 'html').map((s) => s.html).join('');
    expect(html).toContain('contains-task-list');
    expect(html).toContain('<table>');
    expect(html).toContain('katex');
  });
});
