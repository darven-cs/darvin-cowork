import { describe, it, expect, vi, beforeAll } from 'vitest';
import path from 'node:path';
import fs from 'node:fs';
import os from 'node:os';

const electronMock = vi.hoisted(() => {
  let userData = '';
  return {
    setUserData: (p: string) => {
      userData = p;
    },
    getUserData: () => userData,
  };
});

vi.mock('electron', () => ({
  app: { getPath: () => electronMock.getUserData() },
}));

import { readUserSettingsYAML, writeUserSettingsYAML, type UserSettings } from './user-settings';

describe('user-settings yaml round-trip', () => {
  beforeAll(() => {
    electronMock.setUserData(fs.mkdtempSync(path.join(os.tmpdir(), 'darvin-settings-')));
  });

  it('writes and reads back the extended schema (llm.provider / app / memory / providers)', async () => {
    const input: UserSettings = {
      llm: { provider: 'anthropic', api_key: 'sk-ant-1', base_url: 'https://api.anthropic.com', default_model: 'claude-sonnet-4-5' },
      app: { auto_launch: true, notifications: false, proxy: 'http://127.0.0.1:7890' },
      memory: { enabled: true, embedding_provider: 'openai', api_key: 'sk-openai' },
      providers: {
        openai: { api_format: 'openai', api_key: 'sk-openai', base_url: 'https://api.openai.com', default_model: 'gpt-4o' },
        custom: { api_format: 'openai', api_key: '', base_url: 'http://localhost:11434', default_model: '' },
      },
      locale: 'en',
    };
    await writeUserSettingsYAML(input);
    const cfg = await readUserSettingsYAML();
    expect(cfg?.llm).toEqual({
      provider: 'anthropic',
      api_key: 'sk-ant-1',
      base_url: 'https://api.anthropic.com',
      default_model: 'claude-sonnet-4-5',
    });
    expect(cfg?.app).toEqual({ auto_launch: true, notifications: false, proxy: 'http://127.0.0.1:7890' });
    expect(cfg?.memory).toEqual({ enabled: true, embedding_provider: 'openai', api_key: 'sk-openai' });
    expect(cfg?.providers?.openai).toEqual({ api_format: 'openai', api_key: 'sk-openai', base_url: 'https://api.openai.com', default_model: 'gpt-4o' });
    expect(cfg?.providers?.custom).toEqual({ api_format: 'openai', api_key: '', base_url: 'http://localhost:11434', default_model: '' });
    expect(cfg?.locale).toBe('en');
  });

  it('merges a partial patch without clobbering existing fields', async () => {
    await writeUserSettingsYAML({ llm: { api_key: 'sk-keep' }, app: { notifications: true } });
    // 只 patch app.proxy：llm / app.notifications 应保留
    await writeUserSettingsYAML({ app: { proxy: 'http://127.0.0.1:7890' } });
    const cfg = await readUserSettingsYAML();
    expect(cfg?.llm?.api_key).toBe('sk-keep');
    expect(cfg?.app?.notifications).toBe(true);
    expect(cfg?.app?.proxy).toBe('http://127.0.0.1:7890');
  });

  it('defaults missing llm.provider to anthropic', async () => {
    await writeUserSettingsYAML({ llm: { api_key: 'sk-x' } });
    const cfg = await readUserSettingsYAML();
    expect(cfg?.llm?.provider ?? 'anthropic').toBe('anthropic');
  });
});
