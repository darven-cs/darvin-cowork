import { app } from 'electron';
import path from 'node:path';
import fs from 'node:fs';

/**
 * 解析 darvin-agent 二进制的绝对路径。
 *
 * - 生产（app.isPackaged）：从 extraResources 落地的 resources/bin/
 * - 开发：仓库根的 bin/
 *
 * 若二进制不存在返回 undefined，由 caller 决定降级（打 warning）还是抛错。
 */
export function resolveAgentBinaryPath(): string | undefined {
  const { platform, arch } = process;
  const exeSuffix = platform === 'win32' ? '.exe' : '';
  const name = `darvin-agent-${platform}-${arch}${exeSuffix}`;

  let p: string;
  if (app.isPackaged) {
    p = path.join(process.resourcesPath, 'bin', name);
  } else {
    // __dirname 在开发态位于 .vite/build/main/，回溯三级到仓库根
    p = path.join(__dirname, '..', '..', '..', 'bin', name);
  }

  return fs.existsSync(p) ? p : undefined;
}

/**
 * 启动 darvin-agent 子进程。
 *
 * 二进制不存在时打印 warning 并跳过（不抛错）。`AgentClient` 负责
 * 把 stdio 桥接到 preload 的 `window.darvin` IPC。
 */
export async function startAgentRuntime(): Promise<void> {
  const bin = resolveAgentBinaryPath();
  if (!bin) {
    console.warn(
      `[runtime] darvin-agent 二进制不存在，已跳过启动。` +
        `运行 \`npm run build:agent\` 编译。`,
    );
    return;
  }

  // TODO: spawn(bin, [], { stdio: 'pipe' }) 与 src/main/runtime/client.ts 配合
  console.log(`[runtime] would start: ${bin}`);
}
