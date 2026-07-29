<template>
  <div class="flex h-10 shrink-0 items-stretch border-b border-border bg-bg">
    <button
      v-for="item in items"
      :key="item.id"
      type="button"
      class="relative flex flex-1 items-center justify-center font-mono text-[11px] uppercase tracking-[0.08em] transition-colors"
      :class="item.id === active ? 'text-text' : 'text-text-muted hover:text-text'"
      @click="emit('switch', item.id)"
    >
      {{ item.label }}
      <span
        v-if="item.id === active"
        class="absolute inset-x-3 bottom-0 h-px bg-accent"
      />
    </button>
  </div>
</template>

<script setup lang="ts">
import { t } from '../../services/i18n';
import type { SidePanelTab } from '../../composables/useSidePanel';

defineProps<{ active: SidePanelTab }>();
const emit = defineEmits<{ switch: [tab: SidePanelTab] }>();

const items: { id: SidePanelTab; label: string }[] = [
  { id: 'tools',     label: t('sidepanel.tabs.tools') },
  { id: 'thinking',  label: t('sidepanel.tabs.thinking') },
  { id: 'artifact',  label: t('sidepanel.tabs.artifact') },
];
</script>
