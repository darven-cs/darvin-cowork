import { describe, it, expect } from 'vitest';
import { DARVIN_PROVIDERS, darvinProviderPreset, darvinProviderModels } from './providers';

describe('provider preset catalog', () => {
  it('has unique ids and only supported wire formats', () => {
    const ids = new Set<string>();
    for (const p of DARVIN_PROVIDERS) {
      expect(ids.has(p.id)).toBe(false);
      ids.add(p.id);
      expect(['anthropic', 'openai', 'gemini']).toContain(p.apiFormat);
      expect(['china', 'global']).toContain(p.region);
    }
    expect(ids.size).toBeGreaterThanOrEqual(15);
  });

  it('requires a base URL for the custom escape hatch', () => {
    const custom = darvinProviderPreset('custom');
    expect(custom?.requiresBaseUrl).toBe(true);
    expect(custom?.apiKeyRequired).toBe(false);
  });

  it('keyless local ollama and key-required cloud providers differ', () => {
    expect(darvinProviderPreset('ollama')?.apiKeyRequired).toBe(false);
    expect(darvinProviderPreset('lm-studio')?.apiKeyRequired).toBe(false);
    expect(darvinProviderPreset('deepseek')?.apiKeyRequired).toBe(true);
    expect(darvinProviderPreset('anthropic')?.apiKeyRequired).toBe(true);
  });

  it('every preset that ships models has well-formed entries', () => {
    for (const p of DARVIN_PROVIDERS) {
      for (const m of darvinProviderModels(p.id)) {
        expect(m.id.length).toBeGreaterThan(0);
        expect(m.label.length).toBeGreaterThan(0);
      }
    }
    // 除 lm-studio（本地服务器无预设模型）外都应有默认模型。
    for (const p of DARVIN_PROVIDERS) {
      if (p.id === 'lm-studio') continue;
      expect(darvinProviderModels(p.id).length).toBeGreaterThan(0);
    }
  });

  it('deepseek endpoint matches LobsterAI (bare host, openai format)', () => {
    const ds = darvinProviderPreset('deepseek');
    expect(ds?.defaultBaseUrl).toBe('https://api.deepseek.com');
    expect(ds?.apiFormat).toBe('openai');
    expect(ds?.defaultModels.map((m) => m.id)).toContain('deepseek-v4-flash');
  });

  it('unknown preset returns undefined', () => {
    expect(darvinProviderPreset('nope')).toBeUndefined();
    expect(darvinProviderModels('nope')).toEqual([]);
  });
});
