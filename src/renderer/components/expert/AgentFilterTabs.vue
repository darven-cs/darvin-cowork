<template>
  <div class="flex flex-wrap gap-1" role="tablist">
    <button
      v-for="tab in tabs"
      :key="tab.id"
      type="button"
      role="tab"
      :aria-selected="tab.id === active"
      class="rounded-md px-3 py-1.5 font-sans text-[12.5px] transition-all"
      :class="tab.id === active
        ? 'bg-primary-muted font-medium text-primary'
        : 'text-text-muted hover:bg-surface-hover hover:text-text'"
      @click="emit('select', tab.id)"
    >
      {{ tab.label }}
    </button>
  </div>
</template>

<script setup lang="ts">
export type FilterTabId = 'all' | 'free' | 'creative' | 'productivity' | 'technical' | 'business';

const tabs: { id: FilterTabId; label: string }[] = [
  { id: 'all',         label: '全部' },
  { id: 'free',        label: '免费' },
  { id: 'creative',    label: '创意' },
  { id: 'productivity',label: '效率' },
  { id: 'technical',   label: '技术' },
  { id: 'business',    label: '商业' },
];

defineProps<{ active: FilterTabId }>();
const emit = defineEmits<{ select: [id: FilterTabId] }>();
</script>