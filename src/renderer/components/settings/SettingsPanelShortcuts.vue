<template>
  <section class="flex flex-col gap-3">
    <h3 class="font-sans text-[15px] font-semibold text-text">{{ t('settings.shortcuts.title') }}</h3>
    <p class="font-sans text-[12.5px] text-text-muted">{{ t('settings.shortcuts.desc') }}</p>

    <div class="mt-2 flex flex-col gap-1.5">
      <div
        v-for="item in shortcuts"
        :key="item.id"
        class="flex items-center justify-between rounded-md border border-border bg-surface px-3 py-2"
      >
        <span class="font-sans text-[13px] text-text">{{ item.label }}</span>
        <kbd class="rounded border border-border bg-surface-2 px-2 py-0.5 font-mono text-[11.5px] text-text-muted">
          {{ item.keys }}
        </kbd>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { t } from '../../services/i18n';

interface ShortcutItem {
  id: string;
  label: string;
  keys: string;
}

const shortcuts: ShortcutItem[] = [
  { id: 'new-task',  label: '新建任务',     keys: '⌘ N' },
  { id: 'search',    label: '搜索任务',     keys: '⌘ F' },
  { id: 'theme',     label: '切换主题',     keys: '⌘ D' },
  { id: 'settings',  label: '打开设置',     keys: '⌘ ,' },
  { id: 'sidepanel', label: '切换工具面板', keys: '⌘ J' },
  { id: 'send',      label: '发送消息',     keys: 'Enter' },
];
</script>