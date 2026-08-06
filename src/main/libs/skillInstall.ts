/**
 * Skill 安装 / 卸载的 main 端实现。
 *
 * 安装流程：
 *   1. 校验 source 是目录且含 SKILL.md
 *   2. 把 source 整个目录递归复制到 <root>/<basename>/
 *   3. 解析 SKILL.md frontmatter，返回 renderer-friendly summary
 *
 * 卸载流程：
 *   删除 <root>/<id>/ 整个目录
 *
 * 风险扫描：main 端不跑（架构约定——Go 端 internal/skills/scanner.go
 * 在加载时跑）；install 返回 riskLevel='safe'，Go bootstrap 时会把
 * 真实风险等级通过 SkillsChanged 推回 renderer。
 */
import {
  copyFile,
  mkdir,
  readdir,
  readFile,
  rm,
  stat,
} from 'node:fs/promises';
import { existsSync } from 'node:fs';
import path from 'node:path';
import type { DarvinSkillSummary } from '../../shared/darvin-api';
import { parseSkillFrontmatter } from './skillManager';

export interface InstallResult {
  skill: DarvinSkillSummary;
  riskLevel: 'safe';
}

export async function installSkillFromFolder(
  source: string,
  root: string,
): Promise<InstallResult> {
  const st = await stat(source);
  if (!st.isDirectory()) {
    throw new Error('source is not a directory');
  }

  const skillMdPath = path.join(source, 'SKILL.md');
  if (!existsSync(skillMdPath)) {
    throw new Error('source folder does not contain SKILL.md');
  }

  const name = path.basename(source);
  const target = path.join(root, name);
  if (existsSync(target)) {
    throw new Error(`skill "${name}" already installed at ${target}`);
  }

  await mkdir(root, { recursive: true });
  await copyDir(source, target);

  const raw = await readFile(skillMdPath, 'utf8');
  const fm = parseSkillFrontmatter(raw);
  if (!fm) {
    throw new Error('SKILL.md frontmatter is invalid');
  }

  const skillMdStat = await stat(skillMdPath);
  return {
    skill: {
      id: fm.name,
      name: fm.name,
      description: fm.description,
      version: fm.version,
      enabled: true,
      userInvocable: fm.invocation?.userInvocable ?? false,
      isOfficial: false,
      isBuiltIn: false,
      path: path.join(target, 'SKILL.md'),
      source: 'user',
      updatedAt: skillMdStat.mtimeMs,
    },
    riskLevel: 'safe',
  };
}

export async function uninstallSkill(id: string, root: string): Promise<boolean> {
  const target = path.join(root, id);
  if (!existsSync(target)) {
    return false;
  }
  await rm(target, { recursive: true });
  return true;
}

async function copyDir(src: string, dst: string): Promise<void> {
  await mkdir(dst, { recursive: true });
  const entries = await readdir(src, { withFileTypes: true });
  for (const e of entries) {
    const s = path.join(src, e.name);
    const d = path.join(dst, e.name);
    if (e.isDirectory()) {
      await copyDir(s, d);
    } else if (e.isFile()) {
      await copyFile(s, d);
    }
  }
}