<template>
  <button
    type="button"
    :aria-label="label"
    :title="label"
    class="flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-text-muted transition-colors hover:bg-surface-2 hover:text-text"
    @click="$emit('click')"
  >
    <Icon name="mic" :size="16" />
  </button>
</template>

<script setup lang="ts">
import Icon from '../common/Icon.vue';

defineProps<{ label: string }>();
defineEmits<{ click: [] }>();
</script>