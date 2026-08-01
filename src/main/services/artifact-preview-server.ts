/**
 * file-based HTML artifact 的本地预览服务：在 127.0.0.1 起一个静态 HTTP 服务器，
 * 把 workspace 内的 html（及相对资源）以独立 origin 提供给 iframe 内嵌预览。
 *
 * 安全模型：
 * - 服务器只监听 127.0.0.1，不对外网开放。
 * - 每个预览会话注册一个随机 sessionId；请求路径按会话映射到 entry 所在目录，
 *   解析结果必须仍在 workspace 根内，越界返回 403。
 */

import { createServer, type IncomingMessage, type ServerResponse } from 'node:http';
import { readFile, access } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import { basename, dirname, extname, normalize, resolve, sep } from 'node:path';

const MIME: Record<string, string> = {
  '.html': 'text/html; charset=utf-8',
  '.htm': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.jpeg': 'image/jpeg',
  '.gif': 'image/gif',
  '.webp': 'image/webp',
  '.ico': 'image/x-icon',
  '.txt': 'text/plain; charset=utf-8',
  '.woff': 'font/woff',
  '.woff2': 'font/woff2',
  '.ttf': 'font/ttf',
};

interface PreviewSession {
  rootDir: string;
  /** entry 所在目录，作为该会话的 URL 挂载根。 */
  baseDir: string;
  entryFile: string;
}

function withinRoot(rootDir: string, target: string): boolean {
  return target === rootDir || target.startsWith(rootDir + sep);
}

/** 把请求的相对路径解析到 baseDir，越出 rootDir 返回 null。 */
export function resolveWithinRoot(rootDir: string, baseDir: string, rel: string): string | null {
  const target = resolve(baseDir, rel || '.');
  return withinRoot(resolve(rootDir), target) ? target : null;
}

class ArtifactPreviewServer {
  private server: ReturnType<typeof createServer> | null = null;
  private port = 0;
  private readonly sessions = new Map<string, PreviewSession>();

  private async ensureStarted(): Promise<number> {
    if (this.server) return this.port;
    await new Promise<void>((resolvePort, reject) => {
      const server = createServer((req, res) => void this.handle(req, res));
      server.on('error', reject);
      server.listen(0, '127.0.0.1', () => {
        const addr = server.address();
        if (addr && typeof addr === 'object') {
          this.port = addr.port;
          this.server = server;
          resolvePort();
        } else {
          reject(new Error('preview server bind failed'));
        }
      });
    });
    return this.port;
  }

  /**
   * 为 workspace 内一个 html 文件创建预览会话。relativeEntry 相对 workspace 根，
   * entry 所在目录作为该会话的挂载根，html 的相对资源（css/js/img）据此解析。
   */
  async createPreviewSession(rootDir: string, relativeEntry: string): Promise<{ sessionId: string; url: string }> {
    const root = resolve(rootDir);
    const entry = normalize(relativeEntry);
    if (!entry || entry.startsWith('..')) throw new Error('invalid relative path');
    const entryFile = resolve(root, entry);
    if (!withinRoot(root, entryFile)) throw new Error('path escapes workspace');
    await access(entryFile);
    const port = await this.ensureStarted();
    const sessionId = randomUUID();
    this.sessions.set(sessionId, { rootDir: root, baseDir: dirname(entryFile), entryFile });
    return { sessionId, url: `http://127.0.0.1:${port}/${sessionId}/${basename(entryFile)}` };
  }

  destroyPreviewSession(sessionId: string): void {
    this.sessions.delete(sessionId);
    if (this.sessions.size === 0 && this.server) {
      this.server.close();
      this.server = null;
      this.port = 0;
    }
  }

  private async handle(req: IncomingMessage, res: ServerResponse): Promise<void> {
    try {
      const url = new URL(req.url ?? '/', 'http://127.0.0.1');
      const parts = url.pathname.split('/').filter(Boolean);
      const sessionId = parts[0];
      const session = sessionId ? this.sessions.get(sessionId) : undefined;
      if (!session) {
        res.writeHead(404, { 'Content-Type': 'text/plain' });
        res.end('Not Found');
        return;
      }
      const rel = parts.slice(1).join('/');
      const target = rel
        ? resolveWithinRoot(session.rootDir, session.baseDir, rel)
        : session.entryFile;
      if (!target) {
        res.writeHead(403, { 'Content-Type': 'text/plain' });
        res.end('Forbidden');
        return;
      }
      const data = await readFile(target);
      res.writeHead(200, { 'Content-Type': MIME[extname(target).toLowerCase()] ?? 'application/octet-stream' });
      res.end(data);
    } catch {
      res.writeHead(404, { 'Content-Type': 'text/plain' });
      res.end('Not Found');
    }
  }
}

export const artifactPreviewServer = new ArtifactPreviewServer();
