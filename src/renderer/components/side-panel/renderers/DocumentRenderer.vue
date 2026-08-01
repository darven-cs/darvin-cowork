<template>
  <pre v-if="artifact.content.trim()" class="h-full w-full overflow-auto whitespace-pre-wrap break-words p-3 font-mono text-xs text-text">{{ artifact.content }}</pre>
  <div v-else class="flex h-full items-center justify-center px-4 text-center text-sm text-text-muted">
    {{ t('artifact.render.unsupported') }}
  </div>
</template>

<script setup lang="ts">
import type { Artifact } from '../../../composables/useArtifacts';
import { t } from '../../../services/i18n';

defineProps<{ artifact: Artifact }>();
</script>
