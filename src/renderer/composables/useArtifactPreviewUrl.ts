/**
 * useArtifactPreviewUrl — 为 filePath 型 artifact 建本地预览会话。
 *
 * 被 Html/Svg/Image/Video 渲染器共享：拿到 workspace 内文件的 HTTP URL
 * （经 artifact-preview-server，仅监听 127.0.0.1），组件卸载时销毁会话
 * （无会话时服务器整体关闭）。
 */
import { onBeforeUnmount, ref, watch } from 'vue';
import type { Artifact } from './useArtifacts';
import { t } from '../services/i18n';

export function useArtifactPreviewUrl(artifact: () => Artifact | null) {
  const url = ref<string | null>(null);
  const loadError = ref<string | null>(null);
  let sessionId: string | null = null;

  async function setup(): Promise<void> {
    const filePath = artifact()?.filePath;
    if (!filePath) return;
    try {
      const r = await window.darvin.createArtifactPreviewSession(filePath);
      if (r.success && r.url && r.sessionId) {
        sessionId = r.sessionId;
        url.value = r.url;
        loadError.value = null;
      } else {
        loadError.value = r.error ?? t('artifact.render.loadFailed');
      }
    } catch {
      loadError.value = t('artifact.render.loadFailed');
    }
  }

  watch(artifact, () => { void setup(); }, { immediate: true });

  onBeforeUnmount(() => {
    if (sessionId) {
      void window.darvin.destroyArtifactPreviewSession(sessionId);
      sessionId = null;
    }
  });

  return { url, loadError };
}
