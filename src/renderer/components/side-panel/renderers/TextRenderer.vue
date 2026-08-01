<template>
  <pre class="h-full w-full overflow-auto whitespace-pre-wrap break-words p-3 font-mono text-xs text-text">{{ artifact.content }}</pre>
</template>

<script setup lang="ts">
import type { Artifact } from '../../../composables/useArtifacts';

defineProps<{ artifact: Artifact }>();
</script>
