<template>
  <button
    type="button"
    :aria-label="label"
    :title="label"
    :class="wrapperClass"
    :disabled="disabled"
    @click="$emit('click', $event)"
  >
    <Icon :name="name" :size="size" />
  </button>
</template>

<script setup lang="ts">
import { computed } from 'vue';

type Variant = 'ghost' | 'outline' | 'solid';

const props = withDefaults(defineProps<{
  name: string;
  label: string;
  size?: number;
  disabled?: boolean;
  variant?: Variant;
}>(), {
  size: 16,
  variant: 'ghost',
});

defineEmits<{ click: [event: MouseEvent] }>();

const wrapperClass = computed(() => {
  const base = 'inline-flex h-7 w-7 items-center justify-center rounded-md transition-colors';
  if (props.disabled) {
    return `${base} cursor-not-allowed opacity-40`;
  }
  switch (props.variant) {
    case 'outline':
      return `${base} border border-border text-text hover:border-border-strong hover:bg-surface-2`;
    case 'solid':
      return `${base} bg-accent text-white hover:bg-accent-hover`;
    case 'ghost':
    default:
      return `${base} text-text-muted hover:bg-surface-2 hover:text-text`;
  }
});
</script>
