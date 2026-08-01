import { describe, expect, it } from 'vitest';
import { deriveContextStatus, formatTokenCount } from './tokenFormat';

describe('formatTokenCount', () => {
  it('returns raw number below 1k', () => {
    expect(formatTokenCount(0)).toBe('0');
    expect(formatTokenCount(1)).toBe('1');
    expect(formatTokenCount(999)).toBe('999');
  });

  it('formats thousands as k with one decimal, stripping trailing .0', () => {
    expect(formatTokenCount(1_000)).toBe('1k');
    expect(formatTokenCount(1_234)).toBe('1.2k');
    expect(formatTokenCount(9_999)).toBe('10k');
    expect(formatTokenCount(49_900)).toBe('49.9k');
  });

  it('formats millions as M', () => {
    expect(formatTokenCount(1_000_000)).toBe('1M');
    expect(formatTokenCount(1_500_000)).toBe('1.5M');
    expect(formatTokenCount(2_450_000)).toBe('2.5M');
  });

  it('clamps invalid / negative input to 0', () => {
    expect(formatTokenCount(Number.NaN)).toBe('0');
    expect(formatTokenCount(-5)).toBe('0');
  });
});

describe('deriveContextStatus', () => {
  it('explicit status wins', () => {
    expect(deriveContextStatus('compacting', 10)).toBe('compacting');
    expect(deriveContextStatus('normal', 99)).toBe('normal');
    expect(deriveContextStatus('warning', 30)).toBe('warning');
    expect(deriveContextStatus('danger', 10)).toBe('danger');
  });

  it('derives normal below 60% when status is unknown', () => {
    expect(deriveContextStatus(undefined, 0)).toBe('normal');
    expect(deriveContextStatus('unknown', 45)).toBe('normal');
    expect(deriveContextStatus(undefined, 59)).toBe('normal');
  });

  it('derives warning in 60-85% band', () => {
    expect(deriveContextStatus(undefined, 60)).toBe('warning');
    expect(deriveContextStatus(undefined, 78)).toBe('warning');
    expect(deriveContextStatus(undefined, 85)).toBe('warning');
  });

  it('derives danger above 85%', () => {
    expect(deriveContextStatus(undefined, 86)).toBe('danger');
    expect(deriveContextStatus(undefined, 100)).toBe('danger');
  });

  it('unknown when no status and no percent', () => {
    expect(deriveContextStatus(undefined, undefined)).toBe('unknown');
    expect(deriveContextStatus('unknown', undefined)).toBe('unknown');
  });
});
