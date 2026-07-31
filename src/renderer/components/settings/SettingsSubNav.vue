<template>
  <nav class="flex w-56 flex-col gap-1 border-r border-border px-3 py-4" :aria-label="t('settings.nav.aria_label')">
    <button
      v-for="item in items"
      :key="item.id"
      type="button"
      :data-testid="`settings-nav-${item.id}`"
      :aria-current="item.id === active ? 'page' : undefined"
      class="rounded-md px-3 py-2 text-left font-sans text-[13px] transition-colors"
      :class="item.id === active
        ? 'bg-primary-muted font-medium text-primary'
        : 'text-text-muted hover:bg-surface-hover'"
      @click="emit('select', item.id)"
    >
      {{ t(item.labelKey) }}
    </button>
  </nav>
</template>

<script setup lang="ts">
import { t } from '../../services/i18n';

export type SettingsSectionId = 'account' | 'appearance' | 'shortcuts' | 'models' | 'about';

const items: { id: SettingsSectionId; labelKey: string }[] = [
  { id: 'account',    labelKey: 'settings.account.title' },
  { id: 'appearance', labelKey: 'settings.appearance.title' },
  { id: 'shortcuts',  labelKey: 'settings.shortcuts.title' },
  { id: 'models',     labelKey: 'settings.models.title' },
  { id: 'about',      labelKey: 'settings.about.title' },
];

defineProps<{ active: SettingsSectionId }>();
const emit = defineEmits<{ select: [id: SettingsSectionId] }>();
</script>