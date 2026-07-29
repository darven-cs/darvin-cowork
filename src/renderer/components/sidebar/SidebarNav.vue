<template>
  <nav class="mt-1 flex flex-col gap-0.5 px-2">
    <button
      v-for="item in items"
      :key="item.id"
      type="button"
      class="flex items-center gap-2.5 rounded-md px-3 py-2 text-[13px] transition-all cursor-pointer"
      :class="item.id === activeId
        ? 'bg-primary-muted text-primary font-medium'
        : 'text-text-muted hover:bg-surface-hover hover:text-text'"
      @click="emit('navigate', item.id)"
    >
      <Icon :name="item.icon" :size="16" />
      <span>{{ t(item.labelKey) }}</span>
    </button>
  </nav>
</template>

<script setup lang="ts">
import { t } from '../../services/i18n';
import Icon from '../common/Icon.vue';

type NavId = 'new_task' | 'search' | 'scheduled' | 'suite' | 'skill' | 'mcp';

interface NavItem {
  id: NavId;
  labelKey: string;
  icon: string;
}

defineProps<{ activeId: NavId }>();
const emit = defineEmits<{ navigate: [id: NavId] }>();

const items: NavItem[] = [
  { id: 'new_task',  labelKey: 'sidebar.nav.new_task',  icon: 'plus' },
  { id: 'search',    labelKey: 'sidebar.nav.search',    icon: 'search' },
  { id: 'scheduled', labelKey: 'sidebar.nav.scheduled', icon: 'clock' },
  { id: 'suite',     labelKey: 'sidebar.nav.suite',     icon: 'layout' },
  { id: 'skill',     labelKey: 'sidebar.nav.skill',     icon: 'star' },
  { id: 'mcp',       labelKey: 'sidebar.nav.mcp',       icon: 'link' },
];
</script>
