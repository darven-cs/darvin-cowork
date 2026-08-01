<template>
  <iframe
    class="h-full w-full border-0"
    :srcdoc="srcdoc"
    sandbox="allow-scripts"
    :title="artifact.name ?? 'svg'"
  />
</template>

<script setup lang="ts">
import { computed } from 'vue';
import dompurify from 'dompurify';
import type { Artifact } from '../../../composables/useArtifacts';

const props = defineProps<{ artifact: Artifact }>();

const srcdoc = computed(() => {
  const sanitized = dompurify.sanitize(props.artifact.content, {
    USE_PROFILES: { svg: true, svgFilters: true },
    ADD_TAGS: ['use'],
  });
  return `<!doctype html><html><head><style>html,body{margin:0;height:100%;overflow:hidden}svg{width:100%;height:100%;display:block}</style></head><body>${sanitized}</body></html>`;
});
</script>
