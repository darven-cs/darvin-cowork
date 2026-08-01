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

import { resolveWorkspaceRoot, ensureWorkspaceRoot, MAX_WORKSPACE_BYTES } from './user-paths';

describe('user-paths workspace helpers', () => {
  beforeAll(() => {
    electronMock.setUserData(fs.mkdtempSync(path.join(os.tmpdir(), 'darvin-ws-')));
  });

  it('resolveWorkspaceRoot places each session under userData/workspaces', () => {
    const a = resolveWorkspaceRoot('s1');
    const b = resolveWorkspaceRoot('s2');
    expect(a.rootPath).toBe(path.join(electronMock.getUserData(), 'workspaces', 's1'));
    expect(a.workspaceId).toBe('s1');
    expect(b.rootPath).not.toBe(a.rootPath);
  });

  it('ensureWorkspaceRoot creates the directory recursively', async () => {
    const loc = resolveWorkspaceRoot('s-deep');
    await ensureWorkspaceRoot(loc);
    const st = fs.statSync(loc.rootPath);
    expect(st.isDirectory()).toBe(true);
  });

  it('MAX_WORKSPACE_BYTES is 500 MiB', () => {
    expect(MAX_WORKSPACE_BYTES).toBe(500 * 1024 * 1024);
  });
});
