<template>
  <div class="flex h-full w-full flex-col">
    <div class="flex shrink-0 items-center justify-between gap-2 border-b border-border px-3 py-1.5">
      <span class="truncate font-mono text-xs text-text-muted" :title="url">{{ url }}</span>
      <a
        :href="url"
        target="_blank"
        rel="noopener noreferrer"
        class="shrink-0 text-xs text-accent hover:text-accent-hover"
      >
        {{ t('artifact.render.openNewWindow') }}
      </a>
    </div>
    <iframe v-if="url" :src="url" class="min-h-0 flex-1 border-0" :title="artifact.name ?? 'local-service'" />
  </div>
</template>

<script setup lang="ts">
import type { Artifact } from '../../../composables/useArtifacts';
import { t } from '../../../services/i18n';

const props = defineProps<{ artifact: Artifact }>();

const url = props.artifact.content.trim();
</script>
