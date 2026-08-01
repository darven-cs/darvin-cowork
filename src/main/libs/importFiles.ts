/**
 * 文件导入 → workspace 拷贝 → Go 端入库 的 main 侧实现。
 *
 * main 独占路径解析与文件拷贝：dialog 在 main 弹（renderer 拿不到 native
 * dialog），拷贝到 workspace 后调 `agent.import_files` 由 Go 端写
 * `imported_files` 表（事务内容量检查 / sha256 dedupe）。原始源路径不留底。
 */
import fs, { createReadStream, createWriteStream } from 'node:fs';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { pipeline } from 'node:stream/promises';
import type { AgentClient } from '../runtime/client';
import type { WorkspaceLocation } from './user-paths';
import { MAX_IMPORT_BYTES } from './user-paths';
import type {
  DarvinImportErrorCode,
  DarvinImportedFile,
  DarvinImportFilesResponse,
  DarvinListImportedFilesResponse,
} from '../../shared/darvin-api';

export function formatBytes(b: number): string {
  if (b < 1024) return `${b} B`;
  const kb = b / 1024;
  if (kb < 1024) return `${kb.toFixed(1)} KiB`;
  return `${(kb / 1024).toFixed(1)} MiB`;
}

async function sha256OfFile(p: string): Promise<string> {
  const h = createHash('sha256');
  await pipeline(createReadStream(p), h);
  return h.digest('hex');
}

async function copyFileStreaming(src: string, dst: string): Promise<void> {
  await fs.promises.mkdir(path.dirname(dst), { recursive: true });
  await pipeline(createReadStream(src), createWriteStream(dst));
}

/**
 * 解析目标相对路径：
 * - 同 basename + 同 sha256 → null（静默 dedupe，不写入）；
 * - 同 basename + 不同 sha256 → `foo (2).md` 递增后缀；
 * - 无同名 → basename 原样。
 */
export function resolveTargetRelative(
  base: string,
  sha: string,
  existing: DarvinImportedFile[],
): string | null {
  const sameName = existing.filter((f) => f.originalName === base);
  if (sameName.length === 0) return base;
  if (sameName.some((f) => f.sha256 === sha)) return null;
  let n = 2;
  const ext = path.extname(base);
  const stem = path.basename(base, ext);
  const used = new Set(existing.map((f) => f.relativePath));
  while (used.has(`${stem} (${n})${ext}`)) n += 1;
  return `${stem} (${n})${ext}`;
}

/** workspace_event system note 文案：`[系统] 用户导入了文件：spec.md (4.2 KiB, sha256: ...)`. */
export function formatImportNote(imported: DarvinImportedFile[]): string {
  const parts = imported.map(
    (f) => `${f.originalName} (${formatBytes(f.size)}, sha256: ${f.sha256.slice(0, 12)})`,
  );
  const hint = imported.length === 1 ? `。你可以用 read_file 配合相对路径 "${imported[0].relativePath}" 访问` : '';
  return `[系统] 用户导入了文件：${parts.join('、')}${hint}。`;
}

function mapGoSkipReason(reason: string): DarvinImportErrorCode {
  if (reason === 'workspace_full') return 'workspace_full';
  if (reason === 'unsupported_type') return 'unsupported_type';
  return 'copy_failed';
}

/**
 * 串行处理每个源文件：普通文件 / 大小校验 → 拷贝到 workspace → 调 Go 端
 * import_files 入库。成功文件汇总后注入一条 workspace_event system note。
 */
export async function runImport(
  loc: WorkspaceLocation,
  sourcePaths: string[],
  sessionId: string,
  client: AgentClient,
): Promise<DarvinImportFilesResponse> {
  const imported: DarvinImportedFile[] = [];
  const skipped: Array<{ sourcePath: string; reason: DarvinImportErrorCode; message: string }> = [];

  let existing: DarvinImportedFile[] = [];
  try {
    const r = await client.request<DarvinListImportedFilesResponse>(
      'agent.list_imported_files',
      { sessionId },
    );
    existing = r.files;
  } catch {
    /* 离线 / 未配置 workspace → 按空处理，dedupe 失效但 import 仍可用 */
  }

  for (const src of sourcePaths) {
    try {
      const lst = await fs.promises.lstat(src);
      if (!lst.isFile() || lst.isSymbolicLink()) {
        skipped.push({ sourcePath: src, reason: 'unsupported_type', message: 'not a regular file (symlink rejected)' });
        continue;
      }
      if (lst.size > MAX_IMPORT_BYTES) {
        skipped.push({ sourcePath: src, reason: 'too_large', message: `file size ${lst.size} > ${MAX_IMPORT_BYTES}` });
        continue;
      }

      const base = path.basename(src);
      const sha = await sha256OfFile(src);
      const targetRel = resolveTargetRelative(base, sha, existing);
      if (targetRel === null) continue; // 静默 dedupe

      const targetAbs = path.join(loc.rootPath, targetRel);
      await copyFileStreaming(src, targetAbs);

      const resp = await client.request<{ imported: DarvinImportedFile[]; skipped: Array<{ reason: string; message: string }> }>(
        'agent.import_files',
        {
          sessionId,
          sourcePaths: [targetAbs],
          workspaceRelPaths: [targetRel],
          shas: [sha],
          sizes: [lst.size],
          originalNames: [base],
        },
      );
      if (resp.imported.length > 0) {
        imported.push(resp.imported[0]);
        existing = [...existing, resp.imported[0]];
      } else if (resp.skipped.length > 0) {
        skipped.push({ sourcePath: src, reason: mapGoSkipReason(resp.skipped[0].reason), message: resp.skipped[0].message });
        // Go 拒了入库（如 workspace_full），清掉刚才的拷贝避免孤儿文件。
        try {
          await fs.promises.unlink(targetAbs);
        } catch {
          /* 已不存在 */
        }
      }
    } catch (e) {
      const err = e as NodeJS.ErrnoException;
      const code: DarvinImportErrorCode =
        err.code === 'ENOENT' || err.code === 'EACCES' ? 'source_unreadable' : 'copy_failed';
      skipped.push({ sourcePath: src, reason: code, message: err.message });
    }
  }

  if (imported.length > 0) {
    try {
      await client.request('agent.save_message', {
        sessionId,
        content: formatImportNote(imported),
        meta: { tag: 'workspace_event' },
      });
    } catch {
      /* system note 是 best-effort；DB 行已写入 */
    }
  }
  return { imported, skipped };
}
