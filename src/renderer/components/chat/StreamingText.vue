<template>
  <!--
    done=true  → 渲染完整文本
    done=false → 渲染三点脉冲（PR-2 视觉：替换原本的光标闪烁）
  -->
  <span v-if="done" class="whitespace-pre-wrap text-md leading-relaxed">{{ content }}</span>
  <span v-else class="inline-flex items-center gap-1 py-1" aria-label="思考中">
    <span class="inline-block h-1.5 w-1.5 rounded-full bg-text-subtle animate-cursor-blink" />
    <span class="inline-block h-1.5 w-1.5 rounded-full bg-text-subtle animate-cursor-blink" style="animation-delay: 0.18s" />
    <span class="inline-block h-1.5 w-1.5 rounded-full bg-text-subtle animate-cursor-blink" style="animation-delay: 0.36s" />
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue';

const props = defineProps<{ deltas?: string[]; content?: string; done: boolean }>();
const content = computed(() => {
  if (Array.isArray(props.deltas)) return props.deltas.join('');
  return props.content ?? '';
});
</script>