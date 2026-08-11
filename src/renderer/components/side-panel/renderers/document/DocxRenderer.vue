<template>
  <div ref="containerRef" class="h-full w-full overflow-auto bg-white" data-testid="docx-renderer" />
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { renderAsync } from 'docx-preview';
import type { Artifact } from '../../../../composables/useArtifacts';
import { artifactToBuffer } from '../../../../services/fileContent';
import { t } from '../../../../services/i18n';
import { showToast } from '../../../../services/toast';

const props = defineProps<{ artifact: Artifact }>();

const containerRef = ref<HTMLElement | null>(null);

onMounted(async () => {
  try {
    const buf = await artifactToBuffer(props.artifact.filePath, props.artifact.content);
    if (!containerRef.value) return;
    await renderAsync(buf, containerRef.value, undefined, {
      className: 'docx-preview',
      inWrapper: true,
      breakPages: true,
      ignoreLastRenderedPageBreak: false,
      ignoreWidth: false,
      ignoreHeight: false,
      renderHeaders: true,
      renderFooters: true,
      renderFootnotes: true,
      renderEndnotes: true,
    });
  } catch {
    showToast(t('artifact.doc.loadFailed'), 'error');
  }
});
</script>
