import { describe, it, expect, vi, beforeAll } from 'vitest';
import path from 'node:path';
import fs from 'node:fs';
import os from 'node:os';

const electronMock = vi.hoisted(() => {
  let userData = '';
  return { setUserData: (p: string) => { userData = p; }, getUserData: () => userData };
});

vi.mock('electron', () => ({
  app: { getPath: () => electronMock.getUserData() },
}));

import { runImport, resolveTargetRelative } from './importFiles';
import type { AgentClient } from '../runtime/client';
import type { DarvinImportedFile } from '../../shared/darvin-api';

describe('resolveTargetRelative', () => {
  const existing: DarvinImportedFile[] = [
    { id: '1', originalName: 'a.md', relativePath: 'a.md', size: 10, mimeType: null, sha256: 'sha-a', importedAt: 0 },
  ];

  it('same basename + same sha256 → null (silent dedupe)', () => {
    expect(resolveTargetRelative('a.md', 'sha-a', existing)).toBeNull();
  });

  it('same basename + different sha256 → "a (2).md"', () => {
    expect(resolveTargetRelative('a.md', 'sha-b', existing)).toBe('a (2).md');
  });

  it('no name conflict → basename verbatim', () => {
    expect(resolveTargetRelative('new.md', 'sha-x', existing)).toBe('new.md');
  });
});

describe('runImport', () => {
  let wsRoot: string;
  let srcDir: string;
  let loc: { rootPath: string; workspaceId: string };

  beforeAll(() => {
    wsRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'darvin-ws-'));
    srcDir = fs.mkdtempSync(path.join(os.tmpdir(), 'darvin-src-'));
    loc = { rootPath: wsRoot, workspaceId: 's1' };
  });

  function fakeClient(overrides?: Record<string, unknown>): AgentClient {
    const req = vi.fn(async (method: string) => {
      switch (method) {
        case 'agent.list_imported_files':
          return { files: [], workspaceBytes: 0 };
        case 'agent.import_files':
          return { imported: [{ id: 'i1', originalName: 'spec.md', relativePath: 'spec.md', size: 8, mimeType: null, sha256: 'sha-x', importedAt: 0 }], skipped: [] };
        case 'agent.save_message':
          return { id: 'm1' };
        default:
          return overrides?.[method] ?? {};
      }
    });
    return { request: req } as unknown as AgentClient;
  }

  it('copies a text file into the workspace and records it', async () => {
    const src = path.join(srcDir, 'spec.md');
    fs.writeFileSync(src, '# hello');

    const client = fakeClient();
    const res = await runImport(loc, [src], 's1', client);

    expect(res.imported).toHaveLength(1);
    expect(fs.existsSync(path.join(wsRoot, 'spec.md'))).toBe(true);
    expect(fs.readFileSync(path.join(wsRoot, 'spec.md'), 'utf8')).toBe('# hello');

    const req = client.request as ReturnType<typeof vi.fn>;
    const importCall = req.mock.calls.find((c: unknown[]) => c[0] === 'agent.import_files');
    expect(importCall).toBeDefined();
    expect((importCall as unknown[])[1]).toMatchObject({
      sessionId: 's1',
      workspaceRelPaths: ['spec.md'],
      originalNames: ['spec.md'],
    });
    const saveCall = req.mock.calls.find((c: unknown[]) => c[0] === 'agent.save_message');
    expect(saveCall).toBeDefined();
    expect(((saveCall as unknown[])[1] as { meta: { tag: string } }).meta.tag).toBe('workspace_event');
  });

  it('imports files of any extension (no text-only whitelist)', async () => {
    const src = path.join(srcDir, 'photo.pdf');
    fs.writeFileSync(src, '%PDF-1.4');
    const client = fakeClient();
    const res = await runImport(loc, [src], 's1', client);
    expect(res.imported).toHaveLength(1);
    expect(fs.existsSync(path.join(wsRoot, 'photo.pdf'))).toBe(true);
  });

  it('rejects a file over the per-file size limit', async () => {
    const src = path.join(srcDir, 'big.log');
    // sparse file: declared size over the limit without writing 50 MiB
    fs.writeFileSync(src, '');
    fs.truncateSync(src, 50 * 1024 * 1024 + 1);
    const client = fakeClient();
    const res = await runImport(loc, [src], 's1', client);
    expect(res.imported).toHaveLength(0);
    expect(res.skipped).toHaveLength(1);
    expect(res.skipped[0].reason).toBe('too_large');
  });

  it('rejects a symlink source', async () => {
    const real = path.join(srcDir, 'real.md');
    const link = path.join(srcDir, 'link.md');
    fs.writeFileSync(real, '# real');
    try {
      fs.symlinkSync(real, link);
    } catch {
      return; // filesystem without symlink support
    }
    const client = fakeClient();
    const res = await runImport(loc, [link], 's1', client);
    expect(res.imported).toHaveLength(0);
    expect(res.skipped).toHaveLength(1);
    expect(res.skipped[0].reason).toBe('unsupported_type');
  });

  it('applies the name-conflict suffix for a different sha', async () => {
    const src = path.join(srcDir, 'conflict.md');
    fs.writeFileSync(src, '# v2');

    const client = fakeClient();
    const req = client.request as ReturnType<typeof vi.fn>;
    req.mockImplementation(async (method: string) => {
      if (method === 'agent.list_imported_files') {
        return {
          files: [{ id: '1', originalName: 'conflict.md', relativePath: 'conflict.md', size: 5, mimeType: null, sha256: 'sha-old', importedAt: 0 }],
          workspaceBytes: 5,
        };
      }
      if (method === 'agent.import_files') {
        return { imported: [{ id: 'i2', originalName: 'conflict.md', relativePath: 'conflict (2).md', size: 5, mimeType: null, sha256: 'sha-new', importedAt: 0 }], skipped: [] };
      }
      if (method === 'agent.save_message') return { id: 'm2' };
      return {};
    });

    await runImport(loc, [src], 's1', client);
    expect(fs.existsSync(path.join(wsRoot, 'conflict (2).md'))).toBe(true);
  });
});
