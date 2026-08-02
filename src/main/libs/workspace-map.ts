/**
 * 会话工作目录映射持久化（spec 12）。
 *
 * sessionId → 用户自选工作目录绝对路径。落在
 * `<userData>/darvin-agent/workspace-mapping.json`，与 Go 侧
 * config.UserDataDir() 对齐。未配置的 session 由 resolveWorkspaceRoot
 * 回退到默认 `workspaces/<sid>`。
 */
import { app } from 'electron';
import fs from 'node:fs';
import path from 'node:path';

export type WorkspaceMap = Record<string, string>;

function workspaceMapPath(): string {
  return path.join(app.getPath('userData'), 'darvin-agent', 'workspace-mapping.json');
}

/** 读取映射；文件缺失 / 损坏时返回空表。 */
export function readWorkspaceMap(): WorkspaceMap {
  try {
    const raw = fs.readFileSync(workspaceMapPath(), 'utf-8');
    const parsed = JSON.parse(raw) as Record<string, unknown>;
    const out: WorkspaceMap = {};
    for (const [sid, root] of Object.entries(parsed)) {
      if (typeof root === 'string' && root.trim() !== '') out[sid] = root;
    }
    return out;
  } catch {
    return {};
  }
}

/** 整体写回映射。 */
export function writeWorkspaceMap(m: WorkspaceMap): void {
  fs.mkdirSync(path.dirname(workspaceMapPath()), { recursive: true });
  fs.writeFileSync(workspaceMapPath(), JSON.stringify(m, null, 2), 'utf-8');
}
