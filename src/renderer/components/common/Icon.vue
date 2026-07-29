<template>
  <span
    v-if="svg"
    class="inline-flex items-center justify-center"
    v-html="html"
  />
  <span
    v-else
    class="inline-block"
    :style="{ width: `${size}px`, height: `${size}px` }"
  />
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { SVG_SOURCES } from '../../assets/icons';

const props = withDefaults(defineProps<{
  name: string;
  size?: number;
}>(), {
  size: 18,
});

const svg = computed(() => SVG_SOURCES[props.name]);

const html = computed(() => {
  const raw = svg.value;
  if (!raw) return '';
  return raw
    .replace(/width="\d+"/, `width="${props.size}"`)
    .replace(/height="\d+"/, `height="${props.size}"`);
});
</script>
