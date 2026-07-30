<template>
  <!--
    v-html 内容来自 assets/icons 的构建期静态 SVG 表，不含任何用户输入或远程数据，
    因此不存在 XSS 面。若将来 SVG 改为运行时/远程来源，必须先做 sanitize 再放开。
  -->
  <!-- eslint-disable-next-line vue/no-v-html -->
  <span v-if="svg" class="inline-flex items-center justify-center" v-html="html" />
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
