<template>
  <!--
    Brand mark "DC" — DC 字母标，红色渐变圆角方块。
    PR-2 简化为静态展示（PR-3 之后可加交互态）。
    尺寸 96×96，scale 动画由父层 wrapper 控制。
  -->
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

const props = withDefaults(defineProps<{
  size?: number;
}>(), {
  size: 96,
});

const src = computed(() => {
  // vite 资源引用：?url 拿到 URL，build 时复制到 dist
  return new URL('../../assets/icons/mascot-full.svg', import.meta.url).href;
});
</script>