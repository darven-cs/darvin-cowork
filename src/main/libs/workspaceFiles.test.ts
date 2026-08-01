import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {
  kindForPath,
  MAX_READ_BYTES,
  readWorkspaceTextFile,
  resolveWorkspacePath,
  walkWorkspace,
  WALK_MAX_DEPTH,
  WALK_MAX_FILES,
} from './workspaceFiles';

let root: string;

beforeEach(async () => {
  root = await fs.mkdtemp(path.join(os.tmpdir(), 'darvin-ws-test-'));
});

afterEach(async () => {
  await fs.rm(root, { recursive: true, force: true });
});

async function write(rel: string, content: string | Buffer): Promise<string> {
  const abs = path.join(root, rel);
  await fs.mkdir(path.dirname(abs), { recursive: true });
  await fs.writeFile(abs, content);
  return abs;
}

describe('kindForPath', () => {
  it('maps common extensions to artifact kinds', () => {
    expect(kindForPath('a.html')).toBe('html');
    expect(kindForPath('b.HTM')).toBe('html');
    expect(kindForPath('pic.svg')).toBe('svg');
    expect(kindForPath('photo.png')).toBe('image');
    expect(kindForPath('clip.mp4')).toBe('video');
    expect(kindForPath('notes.md')).toBe('markdown');
    expect(kindForPath('log.txt')).toBe('text');
    expect(kindForPath('app.ts')).toBe('code');
    expect(kindForPath('unknown.xyz')).toBe('document');
  });
});

describe('walkWorkspace', () => {
  it('lists files recursively with kind, size, and modifiedAt', async () => {
    await write('a.html', '<html></html>');
    await write('nested/b.md', '# hi');
    await write('nested/deep/c.js', 'x');
    const files = await walkWorkspace(root);
    expect(files.map((f) => f.relativePath).sort()).toEqual(['a.html', 'nested/b.md', 'nested/deep/c.js']);
    const html = files.find((f) => f.relativePath === 'a.html');
    expect(html?.kind).toBe('html');
    expect(html?.size).toBeGreaterThan(0);
    expect(html?.modifiedAt).toBeGreaterThan(0);
  });

  it('truncates silently beyond the depth limit', async () => {
    for (let d = 0; d <= WALK_MAX_DEPTH + 1; d += 1) {
      const rel = Array.from({ length: d + 1 }, () => 'd').join('/') + '/f.txt';
      await write(rel, 'x');
    }
    const files = await walkWorkspace(root);
    const maxDepth = Math.max(...files.map((f) => f.relativePath.split('/').length - 1));
    expect(maxDepth).toBeLessThanOrEqual(WALK_MAX_DEPTH);
  });

  it('stops at the file cap', async () => {
    for (let i = 0; i < WALK_MAX_FILES + 20; i += 1) {
      await write(`f${i}.txt`, 'x');
    }
    const files = await walkWorkspace(root);
    expect(files.length).toBeLessThanOrEqual(WALK_MAX_FILES);
  });

  it('skips a directory that cannot be read', async () => {
    await write('good.txt', 'ok');
    await fs.mkdir(path.join(root, 'locked'));
    await fs.chmod(path.join(root, 'locked'), 0o000);
    try {
      const files = await walkWorkspace(root);
      expect(files.some((f) => f.relativePath === 'good.txt')).toBe(true);
      expect(files.some((f) => f.relativePath.startsWith('locked'))).toBe(false);
    } finally {
      await fs.chmod(path.join(root, 'locked'), 0o755);
    }
  });
});

describe('resolveWorkspacePath', () => {
  it('resolves a nested file inside the root', async () => {
    await write('sub/f.txt', 'x');
    const abs = await resolveWorkspacePath(root, 'sub/f.txt');
    expect(abs).toBe(path.join(root, 'sub', 'f.txt'));
  });

  it('rejects a path escaping the root', async () => {
    await write('f.txt', 'x');
    const abs = await resolveWorkspacePath(root, '../outside');
    expect(abs).toBeNull();
  });

  it('rejects a missing file', async () => {
    expect(await resolveWorkspacePath(root, 'nope.txt')).toBeNull();
  });
});

describe('readWorkspaceTextFile', () => {
  it('reads a small text file', async () => {
    await write('a.md', '# hello');
    const r = await readWorkspaceTextFile(root, 'a.md');
    expect(r.success).toBe(true);
    expect(r.content).toBe('# hello');
    expect(r.size).toBe(7);
  });

  it('returns too_large for files over the read cap', async () => {
    await write('big.txt', 'x'.repeat(MAX_READ_BYTES + 1));
    const r = await readWorkspaceTextFile(root, 'big.txt');
    expect(r.success).toBe(false);
    expect(r.error).toBe('too_large');
  });

  it('returns unsupported for non-text kinds (image)', async () => {
    await write('p.png', Buffer.from([0x89, 0x50, 0x4e, 0x47]));
    const r = await readWorkspaceTextFile(root, 'p.png');
    expect(r.success).toBe(false);
    expect(r.error).toBe('unsupported');
  });

  it('rejects a path escaping the root', async () => {
    const r = await readWorkspaceTextFile(root, '../evil');
    expect(r.success).toBe(false);
    expect(r.error).toBe('invalid_path');
  });

  it('rejects a missing file', async () => {
    const r = await readWorkspaceTextFile(root, 'missing.txt');
    expect(r.success).toBe(false);
    expect(r.error).toBe('not_found');
  });
});
