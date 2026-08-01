<template>
  <div class="h-full w-full overflow-auto p-3">
    <MarkdownContent :content="artifact.content" :done="true" />
  </div>
</template>

<script setup lang="ts">
import MarkdownContent from '../../chat/MarkdownContent.vue';
import type { Artifact } from '../../../composables/useArtifacts';

defineProps<{ artifact: Artifact }>();
</script>
