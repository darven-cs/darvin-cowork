/**
 * useImportedFiles — 当前会话待发送附件的暂存状态（spec 12 / 13）。
 *
 * 附件走 LobsterAI 式路径引用：只记原始绝对路径（path + name + size），
 * 不复制进工作区、不入库。「附加即授权」——发送时随 prompt 带绝对路径，
 * Go agent 把本次消息的附件路径加入授权读集，read_file 可免审批读取。
 *
 * spec 13 — 图片附件单独走 base64：识别扩展名后调 `readFileAsDataUrl`
 * 读成 dataUrl，随 prompt 发给 Go 转 image content block，模型才能真正
 * 看到图。图片路径**不**进授权读集（base64 已交付数据，避免模型 read_file
 * 读到二进制垃圾）。
 * 发送后暂存清空（不删用户原文件）。
 */
import { ref, watch } from 'vue';
import type { DarvinAttachmentRef, DarvinImageRef } from '../../shared/darvin-api';
import { useSession } from './useSession';
import { t } from '../services/i18n';
import { showToast } from '../services/toast';

/** 按扩展名识别的图片类型（对应 main 端 MIME 映射的子集）。 */
const IMAGE_EXTENSIONS = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp']);

/** 是否把该路径当图片附件处理（按扩展名判断）。 */
export function isImagePath(p: string): boolean {
  const ext = p.split('.').pop()?.toLowerCase() ?? '';
  return IMAGE_EXTENSIONS.has(ext);
}

export function formatBytes(b: number): string {
  if (b < 1024) return `${b} B`;
  const kb = b / 1024;
  if (kb < 1024) return `${kb.toFixed(1)} KiB`;
  return `${(kb / 1024).toFixed(1)} MiB`;
}

/** 已暂存、待发送的附件（原始路径引用）。 */
const attachments = ref<DarvinAttachmentRef[]>([]);
/** 图片路径 → base64 dataUrl（pick 时异步读好，发送时直接取）。 */
const imageDataByPath = ref<Record<string, string>>({});
const busy = ref(false);

const session = useSession();
let initialized = false;

// active session 切换时清空暂存，避免把上一个会话的附件带到当前会话。
watch(
  () => session.activeSessionId.value,
  (newId) => {
    if (newId === null) return;
    attachments.value = [];
    imageDataByPath.value = {};
  },
);

function ensureSubscribed(): void {
  if (initialized || typeof window === 'undefined' || !window.darvin) return;
  initialized = true;
}

/** 从暂存列表与 dataUrl 缓存移除一个附件（只 detach，不删原文件）。 */
function detach(path: string): void {
  attachments.value = attachments.value.filter((a) => a.path !== path);
  if (imageDataByPath.value[path] !== undefined) {
    const next = { ...imageDataByPath.value };
    delete next[path];
    imageDataByPath.value = next;
  }
}

/** 把刚选的图片附件读成 base64 dataUrl；失败（过大 / 损坏）toast 并剔除该图。 */
async function readImageDataUrls(refs: DarvinAttachmentRef[]): Promise<void> {
  for (const img of refs.filter((a) => isImagePath(a.path))) {
    try {
      const res = await window.darvin.readFileAsDataUrl(img.path);
      if (!res.success || !res.dataUrl) {
        const key = res.error === 'too_large' ? 'attachment.imageTooLarge' : 'attachment.imageReadFailed';
        showToast(t(key, { name: img.name }), 'error');
        detach(img.path);
        continue;
      }
      imageDataByPath.value = { ...imageDataByPath.value, [img.path]: res.dataUrl };
    } catch {
      showToast(t('attachment.imageReadFailed', { name: img.name }), 'error');
      detach(img.path);
    }
  }
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
        // 图片的 dataUrl 异步读好；pick 返回时已就绪，用户立即发送也不丢图。
        await readImageDataUrls(fresh);
      }
    } finally {
      busy.value = false;
    }
  }

  /** 移除一个待发送附件（只 detach，不删原文件）。 */
  function remove(path: string): void {
    detach(path);
  }

  /** 待发送附件的绝对路径数组（prompt 携带）。 */
  function pendingPaths(): string[] {
    return attachments.value.map((a) => a.path);
  }

  /**
   * 把暂存拆成「非图片文件」（授权读集 + `文件:` 路径行）与「图片」
   * （base64 dataUrl → image content block + `图片:` 路径行）。
   */
  function splitForSend(): { files: DarvinAttachmentRef[]; images: DarvinImageRef[] } {
    const files: DarvinAttachmentRef[] = [];
    const images: DarvinImageRef[] = [];
    for (const a of attachments.value) {
      if (isImagePath(a.path)) {
        const dataUrl = imageDataByPath.value[a.path];
        if (!dataUrl) continue; // 读取失败已剔除，理论走不到这里
        images.push({ path: a.path, name: a.name, size: a.size, dataUrl });
      } else {
        files.push(a);
      }
    }
    return { files, images };
  }

  /** 发送后清空暂存。 */
  function clear(): void {
    attachments.value = [];
    imageDataByPath.value = {};
  }

  return {
    attachments,
    busy,
    pickAttachments,
    remove,
    pendingPaths,
    splitForSend,
    clear,
    formatBytes,
  };
}
