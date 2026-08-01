<template>
  <nav class="mt-1 flex flex-col gap-0.5 px-2" :class="collapsed ? 'items-center px-1' : ''">
    <button
      v-for="item in items"
      :key="item.id"
      type="button"
      :title="collapsed ? t(item.labelKey) : undefined"
      class="flex items-center rounded-md transition-all cursor-pointer"
      :class="[
        collapsed ? 'h-9 w-9 justify-center' : 'gap-2.5 px-3 py-2 text-[13px] w-full',
        item.id === activeId
          ? 'bg-primary-muted text-primary font-medium'
          : 'text-text-muted hover:bg-surface-hover hover:text-text',
      ]"
      @click="emit('navigate', item.id)"
    >
      <Icon :name="item.icon" :size="16" class="shrink-0" />
      <span v-if="!collapsed">{{ t(item.labelKey) }}</span>
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

defineProps<{ activeId: NavId | null; collapsed: boolean }>();
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
