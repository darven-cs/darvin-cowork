<template>
  <div v-if="src" class="flex h-full w-full items-center justify-center overflow-auto p-3">
    <video :src="src" class="max-h-full max-w-full" controls :title="artifact.name ?? 'video'" />
  </div>
  <div v-else-if="loadError" class="flex h-full items-center justify-center px-4 text-center text-sm text-text-muted">
    {{ loadError }}
  </div>
  <div v-else class="flex h-full items-center justify-center px-4 text-center text-sm text-text-muted">
    {{ t('artifact.render.unsupported') }}
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { Artifact } from '../../../composables/useArtifacts';
import { useArtifactPreviewUrl } from '../../../composables/useArtifactPreviewUrl';
import { t } from '../../../services/i18n';

const props = defineProps<{ artifact: Artifact }>();

const inlineSrc = computed(() => {
  const c = props.artifact.content.trim();
  if (c.startsWith('data:') || /^https?:\/\//.test(c)) return c;
  return '';
});

const { url, loadError } = useArtifactPreviewUrl(() => props.artifact);

const src = computed(() => url.value ?? inlineSrc.value);
</script>
