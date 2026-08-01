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

  return {
    files,
    workspaceBytes,
    busy,
    notice,
    importFiles,
    remove,
    formatBytes,
  };
}
