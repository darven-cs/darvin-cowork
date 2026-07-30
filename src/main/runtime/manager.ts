/**
 * darvin-agent 子进程的生命周期管理。
 *
 * 契约（S3 gateway/server.go）：子进程启动后向 stdout 写唯一一行
 * `<port>NNNNN</port>\n`，随后 WS 服务在 `ws://localhost:{port}/ws` 就绪。
 * `RuntimeMgr.start()` 解析这行拿到端口，`AgentClient` 负责建连。
 */

import { app } from 'electron';
import { spawn, type ChildProcess } from 'node:child_process';
import { EventEmitter } from 'node:events';
import path from 'node:path';
import fs from 'node:fs';

/** 启动超时：超过则视为子进程没能进入 listen 状态。 */
const START_TIMEOUT_MS = 5000;

/** SIGTERM 后给 Go graceful shutdown 的宽限期，超时补 SIGKILL。 */
const STOP_GRACE_MS = 4000;

export interface ResolvedAgent {
  port: number;
  pid: number;
}

interface PendingStart {
  resolve: (r: ResolvedAgent) => void;
  reject: (e: Error) => void;
  timer: NodeJS.Timeout;
}

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
    // vite.main.config.ts 用 lib 模式把整个 main 打成 .vite/build/main/index.js，
    // 所以 __dirname 是 .vite/build/main/，回溯三级到仓库根。
    p = path.join(__dirname, '..', '..', '..', 'bin', name);
  }

  return fs.existsSync(p) ? p : undefined;
}

/**
 * 解析 config.yaml 的绝对路径，供 DARVIN_CONFIG 注入。
 *
 * Go 侧 configPath() 的查找顺序是 DARVIN_CONFIG → <exe-dir>/config.yaml →
 * ./config.yaml。开发态二进制在 bin/、cwd 是仓库根，两处都没有配置文件，
 * 不显式注入子进程会直接 `failed to load config` 退出。
 * 打包态由 <exe-dir>/config.yaml 兜住，返回 undefined 不干预。
 */
export function resolveAgentConfigPath(): string | undefined {
  if (app.isPackaged) return undefined;
  const p = path.join(
    __dirname, '..', '..', '..', 'src', 'darvin-agent', 'config.yaml',
  );
  return fs.existsSync(p) ? p : undefined;
}

/**
 * darvin-agent 子进程管理器。
 *
 * 事件：
 * - `'exit'` `{ code, signal }` —— 子进程退出（正常退出与 crash 都发）
 */
export class RuntimeMgr extends EventEmitter {
  private proc: ChildProcess | undefined;
  private resolvedPort: number | undefined;
  private pending: PendingStart | undefined;
  private stdoutBuf = '';

  /**
   * spawn 子进程，在 stdout 出现 `<port>NNNNN</port>` 时 resolve；
   * START_TIMEOUT_MS 内没读到则 reject 并 SIGKILL 兜底。
   *
   * v0 只允许单个子进程，重复调用直接 reject。
   */
  start(): Promise<ResolvedAgent> {
    if (this.proc) {
      return Promise.reject(new Error('runtime: 子进程已在运行'));
    }
    const bin = resolveAgentBinaryPath();
    if (!bin) {
      return Promise.reject(
        new Error('darvin-agent 二进制不存在；运行 `npm run build:agent` 编译'),
      );
    }

    return new Promise<ResolvedAgent>((resolve, reject) => {
      const env: NodeJS.ProcessEnv = {
        ...process.env,
        DARVIN_DEV: app.isPackaged ? '0' : '1',
      };
      const cfg = resolveAgentConfigPath();
      if (cfg) env.DARVIN_CONFIG = cfg;

      const proc = spawn(bin, [], { stdio: ['ignore', 'pipe', 'pipe'], env });
      this.proc = proc;
      this.stdoutBuf = '';

      const timer = setTimeout(() => {
        this.settleStart(
          new Error(`启动超时：${START_TIMEOUT_MS}ms 内未读到 <port>...</port>`),
        );
        // 端口没出来说明进程卡死在 listen 前，直接硬杀
        try {
          proc.kill('SIGKILL');
        } catch {
          /* 已退出 */
        }
      }, START_TIMEOUT_MS);

      this.pending = { resolve, reject, timer };

      // spawn 本身失败（ENOENT / EACCES）走 'error'，不会走 'exit'
      proc.on('error', (err) => {
        this.settleStart(new Error(`spawn 失败：${err.message}`));
      });

      proc.on('exit', (code, signal) => {
        // 还没 resolve 就退了 → 算启动失败
        this.settleStart(new Error(`启动失败 exit=${code} signal=${signal}`));
        this.proc = undefined;
        this.resolvedPort = undefined;
        this.emit('exit', { code, signal });
      });

      proc.stderr?.on('data', (chunk: Buffer) => {
        // Playwright / 测试 runner 关闭 main 进程时会先把 main 的 stderr
        // 关掉，这时再 write 会 EPIPE。writable=false 时直接丢弃。
        if (!process.stderr.writable) return;
        try {
          process.stderr.write(`[darvin-agent] ${chunk.toString('utf8')}`);
        } catch {
          /* EPIPE — main 的 stderr 已关，忽略 */
        }
      });

      proc.stdout?.on('data', (chunk: Buffer) => {
        if (this.resolvedPort !== undefined) return;
        // 端口行可能跨 chunk 到达，累积后再匹配
        this.stdoutBuf += chunk.toString('utf8');
        const m = this.stdoutBuf.match(/<port>(\d+)<\/port>/);
        if (!m) return;
        this.stdoutBuf = '';
        this.resolvedPort = Number(m[1]);
        this.settleStart(undefined, {
          port: this.resolvedPort,
          pid: proc.pid ?? -1,
        });
      });
    });
  }

  /**
   * 结束 start() 的 pending promise（成功或失败），并清掉超时定时器。
   * 已 settle 过则是 noop —— exit / error / timeout 三路都会调它。
   */
  private settleStart(err?: Error, ok?: ResolvedAgent): void {
    const p = this.pending;
    if (!p) return;
    this.pending = undefined;
    clearTimeout(p.timer);
    if (ok) p.resolve(ok);
    else p.reject(err ?? new Error('启动失败'));
  }

  /** SIGTERM 请求 graceful shutdown；STOP_GRACE_MS 后仍在跑则 SIGKILL。 */
  stop(): Promise<void> {
    const proc = this.proc;
    if (!proc) return Promise.resolve();
    return new Promise<void>((resolve) => {
      const timer = setTimeout(() => {
        try {
          proc.kill('SIGKILL');
        } catch {
          /* 已退出 */
        }
        resolve();
      }, STOP_GRACE_MS);
      proc.once('exit', () => {
        clearTimeout(timer);
        resolve();
      });
      try {
        proc.kill('SIGTERM');
      } catch {
        clearTimeout(timer);
        resolve();
      }
    });
  }

  pid(): number | undefined {
    return this.proc?.pid;
  }

  port(): number | undefined {
    return this.resolvedPort;
  }

  /** 端口已解析 = 子进程进入 listen 状态。 */
  isResolved(): boolean {
    return this.resolvedPort !== undefined;
  }
}
