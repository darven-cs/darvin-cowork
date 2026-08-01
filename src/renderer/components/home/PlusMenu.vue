<template>
  <div
    v-if="isOpen"
    class="absolute bottom-full left-0 mb-2 w-72 rounded-xl border border-border bg-surface p-1 shadow-lg"
    @click.stop
  >
    <button
      v-for="item in items"
      :key="item.id"
      type="button"
      :disabled="item.id === 'upload' && busy"
      class="flex w-full items-start gap-3 rounded-lg px-3 py-2.5 text-left transition-colors hover:bg-surface-2 disabled:cursor-not-allowed disabled:opacity-50"
      @click="onPick(item.id)"
    >
      <span class="flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-surface-2 text-text-muted">
        <Icon :name="item.icon" :size="16" />
      </span>
      <span class="flex min-w-0 flex-col">
        <span class="truncate font-sans text-[13px] font-medium text-text">{{ t(item.titleKey) }}</span>
        <span class="truncate font-sans text-[11px] text-text-muted">{{ t(item.descKey) }}</span>
      </span>
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { t } from '../../services/i18n';
import Icon from '../common/Icon.vue';
import { useFloatingPanel } from '../../composables/useFloatingPanel';

type PlusId = 'upload' | 'goal' | 'todo' | 'settings';

interface PlusItem {
  id: PlusId;
  icon: string;
  titleKey: string;
  descKey: string;
}

const items: PlusItem[] = [
  { id: 'upload',   icon: 'paperclip', titleKey: 'plus.upload.title',   descKey: 'plus.upload.desc'   },
  { id: 'goal',     icon: 'target',    titleKey: 'plus.goal.title',     descKey: 'plus.goal.desc'     },
  { id: 'todo',     icon: 'list',      titleKey: 'plus.todo.title',     descKey: 'plus.todo.desc'     },
  { id: 'settings', icon: 'gear',      titleKey: 'plus.settings.title', descKey: 'plus.settings.desc' },
];

const fp = useFloatingPanel();
const isOpen = computed(() => fp.isActive('plus'));

defineProps<{ busy?: boolean }>();

const emit = defineEmits<{ pick: [id: PlusId] }>();

function onPick(id: PlusId) {
  fp.close();
  emit('pick', id);
}
</script>