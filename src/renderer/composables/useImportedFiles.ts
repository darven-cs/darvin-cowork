/**
 * useImportedFiles — 当前 session workspace 导入文件状态的单一来源。
 *
 * files / workspaceBytes 是模块级单例，但内容随 active session 切换：
 * watch activeSessionId 变化时先清空再 refetch，避免切会话瞬间闪现上一个
 * session 的文件。push 事件为权威更新源（按 sessionId 过滤），
 * importFiles / remove 完成后也主动 refetch 一次兜底。
 */
import { ref, watch } from 'vue';
import type { DarvinImportedFile } from '../../shared/darvin-api';
import { t } from '../services/i18n';
import { useSession } from './useSession';

/** 与 main 端 user-paths.MAX_WORKSPACE_BYTES 对齐。 */
export const MAX_WORKSPACE_BYTES = 500 * 1024 * 1024;

const files = ref<DarvinImportedFile[]>([]);
const workspaceBytes = ref(0);
const busy = ref(false);
/** 最近一次 import 的跳过汇总（几秒后自动清空），无 toast 基础设施时的轻量反馈。 */
const notice = ref<string | null>(null);
/** 已随消息消费、待 run 结束后清理的文件相对路径快照（null = 无待清理）。 */
let armedRelPaths: string[] | null = null;

const session = useSession();
let initialized = false;

// active session 切换时立即清空旧数据再 refetch 当前会话文件，防止跨会话
// 数据串扰。模块级注册一次，不随组件生命周期卸载。
watch(
  () => session.activeSessionId.value,
  (newId) => {
    if (newId === null) return;
    files.value = [];
    workspaceBytes.value = 0;
    notice.value = null;
    void (async () => {
      try {
        const r = await window.darvin.listImportedFiles();
        files.value = r.files;
        workspaceBytes.value = r.workspaceBytes;
        await refreshWorkspaceBytes();
      } catch {
        /* agent offline */
      }
    })();
  },
);

function formatBytes(b: number): string {
  if (b < 1024) return `${b} B`;
  const kb = b / 1024;
  if (kb < 1024) return `${kb.toFixed(1)} KiB`;
  return `${(kb / 1024).toFixed(1)} MiB`;
}

async function refreshWorkspaceBytes(): Promise<void> {
  try {
    const info = await window.darvin.getWorkspaceInfo();
    workspaceBytes.value = info.workspaceBytes;
  } catch {
    /* agent offline */
  }
}

function skipMessage(s: { reason: string; message: string }): string {
  const key = `import.error.${s.reason}`;
  const localized = t(key);
  if (localized !== key) return localized;
  return s.message;
}

function ensureSubscribed(): void {
  if (initialized || typeof window === 'undefined' || !window.darvin) return;
  initialized = true;

  window.darvin.onWorkspaceChanged((info) => {
    // 只接收当前 active session 的推送，跨会话广播直接丢弃
    if (info.sessionId !== session.activeSessionId.value) return;
    files.value = info.files;
    void refreshWorkspaceBytes();
  });

  void (async () => {
    try {
      const r = await window.darvin.listImportedFiles();
      files.value = r.files;
      workspaceBytes.value = r.workspaceBytes;
      await refreshWorkspaceBytes();
    } catch {
      /* agent offline */
    }
  })();
}

export function useImportedFiles() {
  ensureSubscribed();

  async function importFiles(): Promise<{ skipped: Array<{ reason: string; message: string }> }> {
    busy.value = true;
    try {
      const res = await window.darvin.importFiles();
      if (res.skipped.length > 0) {
        notice.value = res.skipped.map(skipMessage).join('；');
        setTimeout(() => {
          if (notice.value !== null) notice.value = null;
        }, 6000);
      }
      const r = await window.darvin.listImportedFiles();
      files.value = r.files;
      workspaceBytes.value = r.workspaceBytes;
      return { skipped: res.skipped };
    } finally {
      busy.value = false;
    }
  }

  async function remove(relativePath: string): Promise<void> {
    await window.darvin.removeImportedFile(relativePath);
    const r = await window.darvin.listImportedFiles();
    files.value = r.files;
    workspaceBytes.value = r.workspaceBytes;
  }

  /** 待发送附件的相对路径（供 prompt 携带）；不消费、不删除。 */
  function pendingPaths(): string[] {
    return files.value.map((f) => f.relativePath);
  }

  /**
   * 发送前（prompt 已被 agent 接受后）调用：快照本次消费的文件路径。
   * 真正的清理在 run 结束（agent_end）由 flushAfterSend 触发，避免在
   * agent 异步读取文件前就删掉 workspace 拷贝。
   */
  function armClearAfterSend(): void {
    armedRelPaths = files.value.map((f) => f.relativePath);
  }

  /** run 结束（agent_end）时调用：删除已消费的 workspace 文件 + 行并清空 UI。 */
  async function flushAfterSend(): Promise<void> {
    if (armedRelPaths === null) return;
    const rels = armedRelPaths;
    armedRelPaths = null;
    for (const rel of rels) {
      try {
        await window.darvin.removeImportedFile(rel);
      } catch {
        /* agent offline 等：忽略 */
      }
    }
    try {
      const r = await window.darvin.listImportedFiles();
      files.value = r.files;
      workspaceBytes.value = r.workspaceBytes;
    } catch {
      files.value = files.value.filter((f) => !rels.includes(f.relativePath));
    }
  }

  return {
    files,
    workspaceBytes,
    busy,
    notice,
    importFiles,
    remove,
    pendingPaths,
    armClearAfterSend,
    flushAfterSend,
    formatBytes,
  };
}
