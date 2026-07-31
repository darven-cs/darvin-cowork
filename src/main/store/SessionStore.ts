/**
 * SessionStore — renderer 视角的 session / 消息数据所有者。
 *
 * 数据所有权归主进程：
 * - 单一 SQLite db（`userData / darvin-cowork.sqlite`），独立于 Go agent
 *   自己的 sessions.db。两库各管各的，避免跨语言并发写。
 * - session id 在 main 端用 `crypto.randomUUID()` 生成，renderer 不参与
 * - 当前激活 session id 是 process-local 状态（不落库），重启时按
 *   "最近一次 updated_at 的 session" 兜底
 */

// eslint-disable-next-line import/no-named-as-default
import Database, { type Statement as SqliteStatement, type Database as SqliteDb } from 'better-sqlite3';
import path from 'node:path';
import fs from 'node:fs';
import { randomUUID } from 'node:crypto';
import type {
  DarvinMessage,
  DarvinSession,
  DarvinSessionStatus,
} from '../../shared/darvin-api';

interface SessionRowRaw {
  id: string;
  title: string;
  claude_session_id: string | null;
  status: string;
  created_at: number;
  updated_at: number;
}

interface MessageRowRaw {
  id: string;
  session_id: string;
  role: string;
  content: string;
  done: number;
  error: string | null;
  tool_label: string | null;
  created_at: number;
}

const SCHEMA = `
CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  claude_session_id TEXT,
  status TEXT NOT NULL DEFAULT 'active',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS messages (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  role TEXT NOT NULL,
  content TEXT NOT NULL,
  done INTEGER NOT NULL DEFAULT 0,
  error TEXT,
  tool_label TEXT,
  created_at INTEGER NOT NULL,
  FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_sessions_updated ON sessions(updated_at DESC);
`;

function toSession(row: SessionRowRaw): DarvinSession {
  return {
    id: row.id,
    title: row.title,
    updatedAt: row.updated_at,
    status: row.status as DarvinSessionStatus,
    claudeSessionId: row.claude_session_id,
  };
}

function toMessage(row: MessageRowRaw): DarvinMessage {
  return {
    id: row.id,
    sessionId: row.session_id,
    role: row.role as 'user' | 'assistant',
    content: row.content,
    done: row.done === 1,
    error: row.error ?? undefined,
    toolLabel: row.tool_label ?? undefined,
    createdAt: row.created_at,
  };
}

export class SessionStore {
  private db: SqliteDb;
  private activeSessionId: string | null = null;

  private readonly stmts: {
    insertSession: SqliteStatement;
    selectAllSessions: SqliteStatement;
    selectSession: SqliteStatement;
    deleteSession: SqliteStatement;
    touchSession: SqliteStatement;
    updateTitle: SqliteStatement;
    updateClaudeSessionId: SqliteStatement;
    updateStatus: SqliteStatement;
    insertMessage: SqliteStatement;
    selectMessages: SqliteStatement;
    updateMessageContent: SqliteStatement;
    markMessageDone: SqliteStatement;
    markMessageError: SqliteStatement;
    lastUpdatedSession: SqliteStatement;
  };

  constructor(dbPath: string) {
    fs.mkdirSync(path.dirname(dbPath), { recursive: true });
    this.db = new Database(dbPath);
    this.db.pragma('journal_mode = WAL');
    this.db.pragma('foreign_keys = ON');
    this.db.exec(SCHEMA);

    this.stmts = {
      insertSession: this.db.prepare(
        `INSERT INTO sessions (id, title, claude_session_id, status, created_at, updated_at)
         VALUES (?, ?, NULL, 'active', ?, ?)`,
      ),
      selectAllSessions: this.db.prepare(
        `SELECT id, title, claude_session_id, status, created_at, updated_at
         FROM sessions ORDER BY updated_at DESC`,
      ),
      selectSession: this.db.prepare(
        `SELECT id, title, claude_session_id, status, created_at, updated_at
         FROM sessions WHERE id = ?`,
      ),
      deleteSession: this.db.prepare(`DELETE FROM sessions WHERE id = ?`),
      touchSession: this.db.prepare(`UPDATE sessions SET updated_at = ? WHERE id = ?`),
      updateTitle: this.db.prepare(`UPDATE sessions SET title = ?, updated_at = ? WHERE id = ?`),
      updateClaudeSessionId: this.db.prepare(
        `UPDATE sessions SET claude_session_id = ?, updated_at = ? WHERE id = ?`,
      ),
      updateStatus: this.db.prepare(`UPDATE sessions SET status = ?, updated_at = ? WHERE id = ?`),
      insertMessage: this.db.prepare(
        `INSERT INTO messages (id, session_id, role, content, done, error, tool_label, created_at)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
      ),
      selectMessages: this.db.prepare(
        `SELECT id, session_id, role, content, done, error, tool_label, created_at
         FROM messages WHERE session_id = ?
         ORDER BY created_at ASC, rowid ASC
         LIMIT ? OFFSET ?`,
      ),
      updateMessageContent: this.db.prepare(
        `UPDATE messages SET content = ? WHERE id = ?`,
      ),
      markMessageDone: this.db.prepare(
        `UPDATE messages SET done = 1, content = ? WHERE id = ?`,
      ),
      markMessageError: this.db.prepare(
        `UPDATE messages SET done = 1, error = ? WHERE id = ?`,
      ),
      lastUpdatedSession: this.db.prepare(
        `SELECT id FROM sessions ORDER BY updated_at DESC LIMIT 1`,
      ),
    };
  }

  /** 进程启动时调一次：如果之前没有 active session，把最近更新的那个定为 active。 */
  bootstrapActiveSession(): void {
    if (this.activeSessionId !== null) return;
    const row = this.stmts.lastUpdatedSession.get() as { id: string } | undefined;
    this.activeSessionId = row?.id ?? null;
  }

  createSession(title?: string): DarvinSession {
    const now = Date.now();
    const id = randomUUID();
    const safeTitle = (title && title.trim() !== '') ? title.trim() : '新建会话';
    this.stmts.insertSession.run(id, safeTitle, now, now);
    return {
      id,
      title: safeTitle,
      updatedAt: now,
      status: 'active',
      claudeSessionId: null,
    };
  }

  listSessions(): DarvinSession[] {
    const rows = this.stmts.selectAllSessions.all() as SessionRowRaw[];
    return rows.map(toSession);
  }

  getSession(id: string): DarvinSession | null {
    const row = this.stmts.selectSession.get(id) as SessionRowRaw | undefined;
    return row ? toSession(row) : null;
  }

  /**
   * 删除 session。返回 nextActiveSessionId：删除的是当前 active 时 main
   * 已经把 active 切到下一个；如果删完没剩 session，返 null（UI 退空态）。
   * caller 自行决定要不要 broadcast 给 renderer。
   */
  deleteSession(id: string): { deleted: boolean; nextActiveSessionId: string | null } {
    const existed = this.getSession(id) !== null;
    if (!existed) {
      return { deleted: false, nextActiveSessionId: this.activeSessionId };
    }
    this.stmts.deleteSession.run(id);
    let next: string | null = this.activeSessionId;
    if (this.activeSessionId === id) {
      const remaining = this.listSessions();
      next = remaining.length > 0 ? remaining[0]!.id : null;
      this.activeSessionId = next;
    }
    return { deleted: true, nextActiveSessionId: next };
  }

  setActive(id: string): boolean {
    if (this.getSession(id) === null) return false;
    this.activeSessionId = id;
    this.stmts.touchSession.run(Date.now(), id);
    return true;
  }

  getActive(): string | null {
    return this.activeSessionId;
  }

  /**
   * main 端从 renderer 接到 prompt 时，先把 user message 落库，再去调
   * agent。user message 落库保证 reload history 时能看到。
   */
  appendMessage(msg: Omit<DarvinMessage, 'createdAt'> & { createdAt?: number }): void {
    const createdAt = msg.createdAt ?? Date.now();
    this.stmts.insertMessage.run(
      msg.id,
      msg.sessionId,
      msg.role,
      msg.content,
      msg.done ? 1 : 0,
      msg.error ?? null,
      msg.toolLabel ?? null,
      createdAt,
    );
    this.stmts.touchSession.run(createdAt, msg.sessionId);
  }

  /**
   * 累加 assistant message 的 streaming 内容。EventRouter 在收到
   * text_delta 时调，按 messageId upsert。
   */
  appendMessageDelta(messageId: string, delta: string): void {
    this.stmts.updateMessageContent.run(delta, messageId);
    // 找到对应 session 以便 touch updated_at
    const row = this.db
      .prepare(`SELECT session_id FROM messages WHERE id = ?`)
      .get(messageId) as { session_id: string } | undefined;
    if (row) this.stmts.touchSession.run(Date.now(), row.session_id);
  }

  markMessageDone(messageId: string, finalContent?: string): void {
    if (finalContent !== undefined) {
      this.stmts.markMessageDone.run(finalContent, messageId);
    } else {
      this.stmts.markMessageDone.run('', messageId);
    }
  }

  markMessageError(messageId: string, errMsg: string): void {
    this.stmts.markMessageError.run(errMsg, messageId);
  }

  listMessages(sessionId: string, limit = 1000, offset = 0): DarvinMessage[] {
    const rows = this.stmts.selectMessages.all(sessionId, limit, offset) as MessageRowRaw[];
    return rows.map(toMessage);
  }

  updateTitle(sessionId: string, title: string): boolean {
    if (this.getSession(sessionId) === null) return false;
    this.stmts.updateTitle.run(title, Date.now(), sessionId);
    return true;
  }

  updateClaudeSessionId(sessionId: string, backendKey: string): boolean {
    if (this.getSession(sessionId) === null) return false;
    this.stmts.updateClaudeSessionId.run(backendKey, Date.now(), sessionId);
    return true;
  }

  updateStatus(sessionId: string, status: DarvinSessionStatus): boolean {
    if (this.getSession(sessionId) === null) return false;
    this.stmts.updateStatus.run(status, Date.now(), sessionId);
    return true;
  }

  close(): void {
    this.db.close();
  }
}

/**
 * 默认 db 路径：`app.getPath('userData') / darvin-cowork.sqlite`。
 * 调用方在 main 启动时把 app 准备好后调一次拿实例。
 */
export function defaultSessionStorePath(userDataDir: string): string {
  return path.join(userDataDir, 'darvin-cowork.sqlite');
}