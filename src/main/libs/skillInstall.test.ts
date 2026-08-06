import { describe, expect, it, beforeEach } from 'vitest';
import { existsSync, mkdirSync, writeFileSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import os from 'node:os';
import { installSkillFromFolder, uninstallSkill } from './skillInstall';

const VALID_SKILL = `---
name: testing
description: Suggest useful test coverage
---
body
`;

function tmpRoot(label: string): string {
  return join(os.tmpdir(), `darvin-${label}-${process.pid}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`);
}

function writeSource(dir: string, files: Record<string, string>): void {
  mkdirSync(dir, { recursive: true });
  for (const [rel, content] of Object.entries(files)) {
    const full = join(dir, rel);
    if (rel.endsWith('/')) {
      mkdirSync(full, { recursive: true });
    } else {
      mkdirSync(join(full, '..'), { recursive: true });
      writeFileSync(full, content, 'utf8');
    }
  }
}

describe('installSkillFromFolder', () => {
  let source: string;
  let root: string;

  beforeEach(() => {
    source = join(tmpRoot('parent'), 'testing');
    root = tmpRoot('root');
  });

  it('rejects when source is not a directory', async () => {
    mkdirSync(source, { recursive: true });
    const file = join(source, 'file.md');
    writeFileSync(file, VALID_SKILL, 'utf8');
    await expect(installSkillFromFolder(file, root)).rejects.toThrow(/not a directory/);
  });

  it('rejects when SKILL.md is missing', async () => {
    mkdirSync(source, { recursive: true });
    await expect(installSkillFromFolder(source, root)).rejects.toThrow(/SKILL\.md/);
  });

  it('rejects when frontmatter is invalid', async () => {
    writeSource(source, { 'SKILL.md': '# No frontmatter\n' });
    await expect(installSkillFromFolder(source, root)).rejects.toThrow(/frontmatter/);
  });

  it('rejects when target already exists', async () => {
    writeSource(source, { 'SKILL.md': VALID_SKILL });
    await installSkillFromFolder(source, root);
    await expect(installSkillFromFolder(source, root)).rejects.toThrow(/already installed/);
  });

  it('copies SKILL.md + siblings + nested directories into <root>/<basename>', async () => {
    writeSource(source, {
      'SKILL.md': VALID_SKILL,
      'references/rest.md': 'rest',
      'scripts/check.sh': '#!/bin/sh\n',
    });
    const r = await installSkillFromFolder(source, root);
    expect(r.skill.id).toBe('testing');
    expect(r.riskLevel).toBe('safe');
    expect(r.skill.path).toBe(join(root, 'testing', 'SKILL.md'));
    expect(existsSync(join(root, 'testing', 'SKILL.md'))).toBe(true);
    expect(existsSync(join(root, 'testing', 'references', 'rest.md'))).toBe(true);
    expect(existsSync(join(root, 'testing', 'scripts', 'check.sh'))).toBe(true);
    expect(readFileSync(join(root, 'testing', 'references', 'rest.md'), 'utf8')).toBe('rest');
  });

  it('parses userInvocable from frontmatter into summary', async () => {
    writeSource(source, {
      'SKILL.md': `---
name: hook-test
description: Trigger via /hook-test
invocation:
  userInvocable: true
---
`,
    });
    const r = await installSkillFromFolder(source, root);
    expect(r.skill.userInvocable).toBe(true);
  });
});

describe('uninstallSkill', () => {
  let root: string;
  beforeEach(() => {
    root = tmpRoot('root');
  });

  it('returns false when target does not exist', async () => {
    expect(await uninstallSkill('nope', root)).toBe(false);
  });

  it('removes the folder and returns true', async () => {
    const dir = join(root, 'web-search');
    mkdirSync(join(dir, 'references'), { recursive: true });
    writeFileSync(join(dir, 'SKILL.md'), VALID_SKILL, 'utf8');
    writeFileSync(join(dir, 'references', 'r.md'), 'r', 'utf8');
    expect(existsSync(dir)).toBe(true);
    expect(await uninstallSkill('web-search', root)).toBe(true);
    expect(existsSync(dir)).toBe(false);
  });
});