/**
 * workspace 目录遍历 / 文件读取（main 侧）。
 *
 * 不依赖 Go agent：直接走 Node fs，Go 离线时文件列表恒可用。路径安全：
 * 只从 readdir 迭代目录树（无路径拼接），读/定位/打开走 `resolveWorkspacePath`
 * realpath 校验，越界一律拒绝，且绝对路径不下发到 renderer。
 */
import fs from 'node:fs/promises';
import path from 'node:path';
import type {
  DarvinArtifactKind,
  DarvinReadWorkspaceFileResponse,
  DarvinWorkspaceFileInfo,
} from '../../shared/darvin-api';

/** 文件列表深度上限（相对 workspace 根，根自身为 0）。 */
export const WALK_MAX_DEPTH = 3;
/** 文件列表条数上限（超出静默截断，返回已收集部分）。 */
export const WALK_MAX_FILES = 500;
/** 文本文件读取上限（256KiB）。 */
export const MAX_READ_BYTES = 256 * 1024;

/** 扩展名 → DarvinArtifactKind 映射（命中顺序敏感，靠前优先）。 */
const FILE_KIND_MAP: Array<[RegExp, DarvinArtifactKind]> = [
  [/\.html?$/i, 'html'],
  [/\.svg$/i, 'svg'],
  [/\.(png|jpe?g|gif|webp|avif|bmp|ico)$/i, 'image'],
  [/\.(mp4|webm|og[gv]|mov|mkv|avi)$/i, 'video'],
  [/\.mermaid$/i, 'mermaid'],
  [/\.md$/i, 'markdown'],
  [/\.(txt|log|conf|ini)$/i, 'text'],
  [/\.(js|mjs|cjs|ts|tsx|jsx|py|go|rs|java|rb|c|cpp|cc|cs|sh|bash|json|ya?ml|toml|xml|css|scss|sql|graphql|vue|svelte)$/i, 'code'],
];

/** 按文件名（含扩展名）推断 artifact kind；未知扩展回落 document。 */
export function kindForPath(name: string): DarvinArtifactKind {
  for (const [re, kind] of FILE_KIND_MAP) {
    if (re.test(name)) return kind;
  }
  return 'document';
}

/** 文本可读的 kind 集合（read_workspace_file 只允许这些）。 */
const TEXT_KINDS = new Set<DarvinArtifactKind>(['html', 'svg', 'markdown', 'text', 'code', 'document']);

export function isTextKind(kind: DarvinArtifactKind): boolean {
  return TEXT_KINDS.has(kind);
}

/**
 * realpath 校验相对路径确实落在 workspace 根内，返回绝对路径；
 * 越界 / 不存在返回 null。
 */
export async function resolveWorkspacePath(root: string, relativePath: string): Promise<string | null> {
  const realRoot = await fs.realpath(root).catch(() => null);
  if (!realRoot) return null;
  const abs = path.resolve(root, relativePath);
  const realAbs = await fs.realpath(abs).catch(() => null);
  if (!realAbs) return null;
  if (realAbs !== realRoot && !realAbs.startsWith(realRoot + path.sep)) return null;
  return realAbs;
}

/**
 * 递归列 workspace 目录（深度 ≤ WALK_MAX_DEPTH、条数 ≤ WALK_MAX_FILES），
 * 目录优先、按名排序，返回相对路径（`/` 分隔）。单目录读失败静默跳过。
 */
export async function walkWorkspace(root: string): Promise<DarvinWorkspaceFileInfo[]> {
  const out: DarvinWorkspaceFileInfo[] = [];

  async function walkDir(dir: string, depth: number): Promise<void> {
    if (depth > WALK_MAX_DEPTH || out.length >= WALK_MAX_FILES) return;
    let entries;
    try {
      entries = await fs.readdir(dir, { withFileTypes: true });
    } catch {
      return;
    }
    entries.sort((a, b) => {
      if (a.isDirectory() !== b.isDirectory()) return a.isDirectory() ? -1 : 1;
      return a.name.localeCompare(b.name);
    });
    for (const ent of entries) {
      if (out.length >= WALK_MAX_FILES) return;
      const abs = path.join(dir, ent.name);
      if (ent.isDirectory()) {
        await walkDir(abs, depth + 1);
        continue;
      }
      if (!ent.isFile()) continue;
      let stat;
      try {
        stat = await fs.stat(abs);
      } catch {
        continue;
      }
      const relativePath = path.relative(root, abs).split(path.sep).join('/');
      out.push({
        relativePath,
        name: ent.name,
        kind: kindForPath(ent.name),
        size: stat.size,
        modifiedAt: Math.round(stat.mtimeMs),
      });
    }
  }

  await walkDir(root, 0);
  return out;
}

/** 读 workspace 内文本文件（≤256KiB；非文本 kind 返回 unsupported）。 */
export async function readWorkspaceTextFile(
  root: string,
  relativePath: string,
): Promise<DarvinReadWorkspaceFileResponse> {
  // 词法越界预检：resolve 后不在根内 → invalid_path（不暴露绝对路径）
  const rootResolved = path.resolve(root);
  const resolved = path.resolve(root, relativePath);
  if (resolved !== rootResolved && !resolved.startsWith(rootResolved + path.sep)) {
    return { success: false, error: 'invalid_path' };
  }
  const abs = await resolveWorkspacePath(root, relativePath);
  if (!abs) return { success: false, error: 'not_found' };
  let stat;
  try {
    stat = await fs.stat(abs);
  } catch {
    return { success: false, error: 'not_found' };
  }
  if (!stat.isFile()) return { success: false, error: 'not_found' };
  if (stat.size > MAX_READ_BYTES) return { success: false, error: 'too_large' };
  if (!isTextKind(kindForPath(relativePath))) return { success: false, error: 'unsupported' };
  try {
    const content = await fs.readFile(abs, 'utf-8');
    return { success: true, content, size: stat.size };
  } catch {
    return { success: false, error: 'unreadable' };
  }
}
