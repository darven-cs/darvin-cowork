/**
 * useImportedFiles — 当前会话待发送附件的暂存状态（spec 12）。
 *
 * 附件走 LobsterAI 式路径引用：只记原始绝对路径（path + name + size），
 * 不复制进工作区、不入库。「附加即授权」——发送时随 prompt 带绝对路径，
 * Go agent 把本次消息的附件路径加入授权读集，read_file 可免审批读取。
 * 发送后暂存清空（不删用户原文件）。
 */
import { ref, watch } from 'vue';
import type { DarvinAttachmentRef } from '../../shared/darvin-api';
import { useSession } from './useSession';

/** 已暂存、待发送的附件（原始路径引用）。 */
const attachments = ref<DarvinAttachmentRef[]>([]);
const busy = ref(false);

const session = useSession();
let initialized = false;

// active session 切换时清空暂存，避免把上一个会话的附件带到当前会话。
watch(
  () => session.activeSessionId.value,
  (newId) => {
    if (newId === null) return;
    attachments.value = [];
  },
);

function formatBytes(b: number): string {
  if (b < 1024) return `${b} B`;
  const kb = b / 1024;
  if (kb < 1024) return `${kb.toFixed(1)} KiB`;
  return `${(kb / 1024).toFixed(1)} MiB`;
}

function ensureSubscribed(): void {
  if (initialized || typeof window === 'undefined' || !window.darvin) return;
  initialized = true;
}

export function useImportedFiles() {
  ensureSubscribed();

  /** 弹系统文件选择框，把选中的文件记为待发送附件（只记路径，不复制）。 */
  async function pickAttachments(): Promise<void> {
    busy.value = true;
    try {
      const res = await window.darvin.pickAttachments();
      if (res.attachments.length > 0) {
        const seen = new Set(attachments.value.map((a) => a.path));
        const fresh = res.attachments.filter((a) => !seen.has(a.path));
        attachments.value = [...attachments.value, ...fresh];
      }
    } finally {
      busy.value = false;
    }
  }

  /** 移除一个待发送附件（只 detach，不删原文件）。 */
  function remove(path: string): void {
    attachments.value = attachments.value.filter((a) => a.path !== path);
  }

  /** 待发送附件的绝对路径数组（prompt 携带）。 */
  function pendingPaths(): string[] {
    return attachments.value.map((a) => a.path);
  }

  /** 发送后清空暂存。 */
  function clear(): void {
    attachments.value = [];
  }

  return {
    attachments,
    busy,
    pickAttachments,
    remove,
    pendingPaths,
    clear,
    formatBytes,
  };
}
