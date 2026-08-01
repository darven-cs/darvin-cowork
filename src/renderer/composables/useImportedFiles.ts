/**
 * useImportedFiles — 当前 session workspace 导入文件状态的单一来源。
 *
 * v0 单 workspace per app run（由 main 启动期固定），所以 files /
 * workspaceBytes 是全局单例，不随 active session 切换。push 事件为权威
 * 更新源，importFiles / remove 完成后也主动 refetch 一次兜底。
 */
import { ref } from 'vue';
import type { DarvinImportedFile } from '../../shared/darvin-api';
import { t } from '../services/i18n';

/** 与 main 端 user-paths.MAX_WORKSPACE_BYTES 对齐。 */
export const MAX_WORKSPACE_BYTES = 500 * 1024 * 1024;

const files = ref<DarvinImportedFile[]>([]);
const workspaceBytes = ref(0);
const busy = ref(false);
/** 最近一次 import 的跳过汇总（几秒后自动清空），无 toast 基础设施时的轻量反馈。 */
const notice = ref<string | null>(null);

let initialized = false;

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
