<template>
  <div class="animate-mascot-breathe origin-center" style="display:inline-block">
    <img
      :src="src"
      :width="size"
      :height="size"
      alt="Darvin-Cowork"
      draggable="false"
      class="select-none"
    />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';

withDefaults(defineProps<{
  size?: number;
}>(), {
  size: 96,
});

const src = computed(() => {
  return new URL('../../assets/icons/mascot-full.svg', import.meta.url).href;
});
</script>