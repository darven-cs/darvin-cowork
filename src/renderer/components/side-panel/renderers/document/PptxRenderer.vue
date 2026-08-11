<template>
  <div class="flex h-full flex-col" data-testid="pptx-renderer">
    <iframe
      ref="iframeRef"
      class="min-h-0 flex-1 border-0 bg-surface"
      :class="{ hidden: error }"
      data-testid="pptx-iframe"
    />
    <div v-if="error" class="flex min-h-0 flex-1 flex-col items-center justify-center gap-3 p-4 text-sm text-text-muted">
      <span>{{ t('artifact.doc.loadFailed') }}</span>
      <button
        v-if="filePath"
        type="button"
        class="rounded border border-border px-2.5 py-1 text-xs text-text transition-colors hover:bg-surface-2"
        data-testid="pptx-open-app"
        @click="openWithApp"
      >
        {{ t('artifact.doc.openWithApp') }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue';
import { init as pptxInit } from 'pptx-preview';
import type { Artifact } from '../../../../composables/useArtifacts';
import { artifactToBuffer } from '../../../../services/fileContent';
import { fixPptxData } from '../../../../services/pptxFix';
import { t } from '../../../../services/i18n';
import { showToast } from '../../../../services/toast';

const props = defineProps<{ artifact: Artifact }>();

const iframeRef = ref<HTMLIFrameElement | null>(null);
const error = ref(false);
const filePath = ref(props.artifact.filePath ?? '');
let previewer: { destroy?: () => void } | null = null;

onMounted(async () => {
  try {
    const buf = await artifactToBuffer(props.artifact.filePath, props.artifact.content);
    const fixed = await fixPptxData(buf);
    const doc = iframeRef.value?.contentDocument;
    if (!doc) {
      error.value = true;
      return;
    }
    doc.open();
    doc.write(`<!DOCTYPE html><html><head><style>
      * { margin: 0; padding: 0; box-sizing: border-box; }
      html, body { width: 100%; min-height: 100%; background: #f3f4f6; }
      body { padding: 16px; overflow: auto; }
      .pptx-preview-wrapper { background: transparent !important; width: 100% !important; max-width: 100% !important; height: auto !important; overflow: visible !important; }
      .pptx-preview-wrapper > div { margin: 0 auto 16px; box-shadow: 0 2px 8px rgba(0,0,0,0.12); border-radius: 4px; overflow: hidden; }
      .pptx-preview-wrapper > div:last-child { margin-bottom: 0; }
      canvas { width: 100% !important; height: auto !important; display: block; }
    </style></head><body><main id="pptx-main"></main></body></html>`);
    doc.close();
    const mainRoot = doc.getElementById('pptx-main');
    if (!mainRoot) {
      error.value = true;
      return;
    }
    previewer = pptxInit(mainRoot, { width: 600, mode: 'list' });
    await previewer.preview(fixed);
  } catch {
    error.value = true;
    showToast(t('artifact.doc.loadFailed'), 'error');
  }
});

onBeforeUnmount(() => {
  previewer?.destroy?.();
});

function openWithApp(): void {
  if (filePath.value) void window.darvin.openWorkspaceFile(filePath.value);
}
</script>
