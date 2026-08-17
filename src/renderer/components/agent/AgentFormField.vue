<template>
  <label class="flex flex-col gap-1">
    <span class="font-sans text-[12.5px] text-text-muted">{{ label }}</span>
    <textarea
      v-if="multiline"
      :value="modelValue"
      rows="3"
      class="rounded-md border border-border bg-surface px-3 py-1.5 font-sans text-[13px] text-text outline-none focus:border-primary"
      @input="onInput"
    />
    <input
      v-else
      :value="modelValue"
      type="text"
      class="rounded-md border border-border bg-surface px-3 py-1.5 font-sans text-[13px] text-text outline-none focus:border-primary"
      @input="onInput"
    />
  </label>
</template>

<script setup lang="ts">
defineProps<{ label: string; modelValue: string; multiline?: boolean }>();
const emit = defineEmits<{ 'update:modelValue': [string] }>();
function onInput(e: Event): void {
  emit('update:modelValue', (e.target as HTMLInputElement | HTMLTextAreaElement).value);
}
</script>