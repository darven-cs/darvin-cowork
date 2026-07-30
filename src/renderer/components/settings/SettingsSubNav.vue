<template>
  <nav class="flex w-56 flex-col gap-1 border-r border-border px-3 py-4" aria-label="设置子导航">
    <button
      v-for="item in items"
      :key="item.id"
      type="button"
      :aria-current="item.id === active ? 'page' : undefined"
      class="rounded-md px-3 py-2 text-left font-sans text-[13px] transition-colors"
      :class="item.id === active
        ? 'bg-primary-muted font-medium text-primary'
        : 'text-text-muted hover:bg-surface-hover'"
      @click="emit('select', item.id)"
    >
      {{ item.label }}
    </button>
  </nav>
</template>

<script setup lang="ts">
export type SettingsSectionId = 'account' | 'appearance' | 'shortcuts' | 'about';

const items: { id: SettingsSectionId; label: string }[] = [
  { id: 'account',    label: '账户' },
  { id: 'appearance', label: '外观' },
  { id: 'shortcuts',  label: '快捷键' },
  { id: 'about',      label: '关于' },
];

defineProps<{ active: SettingsSectionId }>();
const emit = defineEmits<{ select: [id: SettingsSectionId] }>();
</script>