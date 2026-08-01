<template>
  <iframe
    v-if="previewUrl"
    :src="previewUrl"
    class="h-full w-full border-0"
    sandbox="allow-scripts"
    :title="artifact.name ?? 'html'"
  />
  <div v-else-if="loadError" class="flex h-full items-center justify-center px-4 text-center text-sm text-text-muted">
    {{ loadError }}
  </div>
  <iframe
    v-else
    class="h-full w-full border-0"
    :srcdoc="srcdoc"
    sandbox="allow-scripts"
    :title="artifact.name ?? 'html'"
  />
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import type { Artifact } from '../../../composables/useArtifacts';
import { injectHashNavInterceptor } from '../../../services/artifactHtml';
import { t } from '../../../services/i18n';

const props = defineProps<{ artifact: Artifact }>();

const previewUrl = ref<string | null>(null);
const loadError = ref<string | null>(null);
let previewSessionId: string | null = null;

const srcdoc = computed(() => injectHashNavInterceptor(props.artifact.content));

async function setupPreview(): Promise<void> {
  if (!props.artifact.filePath) return;
  try {
    const r = await window.darvin.createArtifactPreviewSession(props.artifact.filePath);
    if (r.success && r.url && r.sessionId) {
      previewSessionId = r.sessionId;
      previewUrl.value = r.url;
      loadError.value = null;
    } else {
      loadError.value = r.error ?? t('artifact.render.loadFailed');
    }
  } catch {
    loadError.value = t('artifact.render.loadFailed');
  }
}

watch(() => props.artifact.filePath, () => { void setupPreview(); }, { immediate: true });

onBeforeUnmount(() => {
  if (previewSessionId) {
    void window.darvin.destroyArtifactPreviewSession(previewSessionId);
    previewSessionId = null;
  }
});
</script>
