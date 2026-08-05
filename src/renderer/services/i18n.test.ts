import { describe, expect, it, beforeEach, vi } from 'vitest';
import { assertSameKeys, dictEn, dictZh, formatDate, formatNumber, formatRelativeTime, getLang, setLang, t } from './i18n';

describe('t() interpolation', () => {
  beforeEach(() => setLang('zh'));

  it('replaces a single {name} placeholder', () => {
    expect(t('context.usage.percent', { percent: 78 })).toBe('78%');
  });

  it('replaces multiple placeholders in one call', () => {
    expect(t('context.usage.tokens', { used: '1.2k', total: '100k' })).toBe('已用 1.2k / 上下文 100k');
  });

  it('returns the raw value when no params are passed', () => {
    expect(t('chat.send')).toBe('发送');
  });

  it('interpolates after switching to en', () => {
    setLang('en');
    expect(t('context.usage.tokens', { used: '1.2k', total: '100k' })).toBe('1.2k used / 100k context');
    expect(t('chat.send')).toBe('Send');
  });
});

describe('t() missing-key guard', () => {
  beforeEach(() => setLang('zh'));

  it('returns the key and warns once for a missing key in dev', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    expect(t('nonexistent.key.a')).toBe('nonexistent.key.a');
    expect(t('nonexistent.key.a')).toBe('nonexistent.key.a');
    expect(warn).toHaveBeenCalledTimes(1);
    warn.mockRestore();
  });

  it('does not warn for keys present in the dict', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    t('chat.send');
    expect(warn).not.toHaveBeenCalled();
    warn.mockRestore();
  });
});

describe('formatRelativeTime', () => {
  beforeEach(() => setLang('zh'));
  const now = Date.now();

  it('returns justNow for < 1 minute', () => {
    expect(formatRelativeTime(now)).toBe('现在');
    expect(formatRelativeTime(now - 59_000)).toBe('现在');
  });

  it('returns compact minutes', () => {
    expect(formatRelativeTime(now - 5 * 60_000)).toBe('5m');
  });

  it('returns compact hours', () => {
    expect(formatRelativeTime(now - 3 * 3_600_000)).toBe('3h');
  });

  it('returns yesterday label for ~1 day', () => {
    expect(formatRelativeTime(now - 24 * 3_600_000)).toBe('昨');
  });

  it('returns compact days and weeks beyond', () => {
    expect(formatRelativeTime(now - 4 * 24 * 3_600_000)).toBe('4d');
    expect(formatRelativeTime(now - 14 * 24 * 3_600_000)).toBe('2w');
  });

  it('localizes the justNow / yesterday labels in en', () => {
    setLang('en');
    expect(formatRelativeTime(now)).toBe('now');
    expect(formatRelativeTime(now - 24 * 3_600_000)).toBe('yest.');
  });
});

describe('formatNumber / formatDate', () => {
  beforeEach(() => setLang('zh'));

  it('groups numbers per locale', () => {
    expect(formatNumber(1234567)).toBe('1,234,567');
  });

  it('formats percentages', () => {
    expect(formatNumber(0.78, { style: 'percent' })).toBe('78%');
  });

  it('formats a timestamp into a localized date', () => {
    const out = formatDate(1722522000000); // 2024-08-01
    expect(out).toContain('2024');
    expect(out.length).toBeGreaterThan(0);
  });
});

describe('dict key parity', () => {
  it('assertSameKeys passes for zh and en', () => {
    expect(() => assertSameKeys(dictZh, dictEn)).not.toThrow();
  });

  it('getLang reflects setLang', () => {
    setLang('en');
    expect(getLang()).toBe('en');
    setLang('zh');
  });
});
