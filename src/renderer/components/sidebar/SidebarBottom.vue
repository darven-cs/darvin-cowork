<template>
  <div class="grid gap-px border-t border-border bg-border" :class="collapsed ? 'grid-cols-1' : 'grid-cols-2'">
    <button
      type="button"
      :title="collapsed ? t('sidebar.login') : undefined"
      class="flex items-center justify-center gap-1.5 bg-surface py-3 text-[13px] text-text-muted transition-colors hover:bg-surface-hover hover:text-text"
      @click="emit('login')"
    >
      <Icon name="log-in" :size="14" />
      <span v-if="!collapsed">{{ t('sidebar.login') }}</span>
    </button>
    <button
      type="button"
      :title="collapsed ? t('sidebar.settings') : undefined"
      data-testid="sidebar-settings"
      class="flex items-center justify-center gap-1.5 bg-surface py-3 text-[13px] text-text-muted transition-colors hover:bg-surface-hover hover:text-text"
      @click="emit('settings')"
    >
      <Icon name="cog" :size="14" />
      <span v-if="!collapsed">{{ t('sidebar.settings') }}</span>
    </button>
  </div>
</template>

<script setup lang="ts">
import { t } from '../../services/i18n';
import Icon from '../common/Icon.vue';

defineProps<{ collapsed: boolean }>();
const emit = defineEmits<{ login: []; settings: [] }>();
</script>
