<template>
  <Icon name="subagents" :size="size" class="shrink-0" />
</template>

<script setup lang="ts">
import Icon from './Icon.vue';

withDefaults(defineProps<{ size?: number }>(), { size: 16 });
</script>
