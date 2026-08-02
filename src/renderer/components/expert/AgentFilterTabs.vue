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
      {{ t(tab.labelKey) }}
    </button>
  </div>
</template>

<script setup lang="ts">
import { t } from '../../services/i18n';

export type FilterTabId = 'all' | 'free' | 'creative' | 'productivity' | 'technical' | 'business';

const tabs: { id: FilterTabId; labelKey: string }[] = [
  { id: 'all',         labelKey: 'expert.filter.all' },
  { id: 'free',        labelKey: 'expert.filter.free' },
  { id: 'creative',    labelKey: 'expert.filter.creative' },
  { id: 'productivity',labelKey: 'expert.filter.productivity' },
  { id: 'technical',   labelKey: 'expert.filter.technical' },
  { id: 'business',    labelKey: 'expert.filter.business' },
];

defineProps<{ active: FilterTabId }>();
const emit = defineEmits<{ select: [id: FilterTabId] }>();
</script>