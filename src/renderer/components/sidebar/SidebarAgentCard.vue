<template>
  <div class="px-2" :class="collapsed ? 'px-1' : ''">
    <div
      v-if="!collapsed"
      class="px-2 pt-4 pb-2 text-[11px] font-medium uppercase tracking-wider text-text-subtle"
    >
      {{ t('sidebar.my_agent_label') }}
    </div>
    <button
      type="button"
      :title="collapsed ? t('sidebar.agent.main.name') : undefined"
      class="flex w-full items-center gap-2.5 rounded-lg transition-all cursor-pointer"
      :class="collapsed
        ? 'justify-center py-2'
        : 'border border-border bg-surface-raised px-3 py-2.5 hover:border-border-strong'"
    >
      <span class="grid h-8 w-8 shrink-0 place-items-center rounded-full bg-avatar-main text-white">
        <Icon name="mascot-mini" :size="18" />
      </span>
      <span v-if="!collapsed" class="flex-1 min-w-0 text-left">
        <span class="block truncate text-[13px] font-medium text-text">
          {{ t('sidebar.agent.main.name') }}
        </span>
        <span class="block truncate text-[11px] text-text-muted">
          {{ t('sidebar.agent.main.sub') }}
        </span>
      </span>
      <span v-if="!collapsed" class="h-1.5 w-1.5 shrink-0 rounded-full bg-success" />
    </button>
  </div>
</template>

<script setup lang="ts">
import { t } from '../../services/i18n';
import Icon from '../common/Icon.vue';

defineProps<{ collapsed: boolean }>();
</script>
