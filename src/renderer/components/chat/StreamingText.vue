<template>
  <span class="whitespace-pre-wrap text-md leading-relaxed">{{ content }}<span
    v-if="!done"
    class="ml-0.5 inline-block h-[1em] w-[0.4em] translate-y-[0.15em] bg-accent align-baseline animate-cursor-blink"
  /></span>
</template>

<script setup lang="ts">
import { computed } from 'vue';

const props = defineProps<{ deltas?: string[]; content?: string; done: boolean }>();
const content = computed(() => {
  if (Array.isArray(props.deltas)) return props.deltas.join('');
  return props.content ?? '';
});
</script>
