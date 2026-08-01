<template>
  <section class="flex flex-col gap-3" data-testid="settings-shortcuts">
    <h3 class="font-sans text-[15px] font-semibold text-text">{{ t('settings.shortcuts.title') }}</h3>
    <p class="font-sans text-[12.5px] text-text-muted">{{ t('settings.shortcuts.desc') }}</p>

    <div class="mt-2 flex flex-col gap-1.5">
      <div
        v-for="item in shortcuts"
        :key="item.id"
        class="flex items-center justify-between rounded-md border border-border bg-surface px-3 py-2"
      >
        <span class="font-sans text-[13px] text-text">{{ t(item.labelKey) }}</span>
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
  labelKey: string;
  keys: string;
}

// 与 useShortcuts（spec 06）及 Composer / PromptDock / useFloatingPanel 的实际绑定对齐。
const isMac = /Mac|iPod|iPhone|iPad/.test(navigator.platform);
const mod = isMac ? '⌘' : 'Ctrl';

const shortcuts: ShortcutItem[] = [
  { id: 'nav-home',     labelKey: 'settings.shortcuts.nav_home',     keys: `${mod}+1` },
  { id: 'nav-search',   labelKey: 'settings.shortcuts.nav_search',   keys: `${mod}+2` },
  { id: 'nav-scheduled', labelKey: 'settings.shortcuts.nav_scheduled', keys: `${mod}+3` },
  { id: 'nav-suite',    labelKey: 'settings.shortcuts.nav_suite',    keys: `${mod}+4` },
  { id: 'nav-skills',   labelKey: 'settings.shortcuts.nav_skills',   keys: `${mod}+5` },
  { id: 'send',         labelKey: 'settings.shortcuts.send',         keys: 'Enter' },
  { id: 'newline',      labelKey: 'settings.shortcuts.newline',      keys: 'Shift+Enter' },
  { id: 'send-force',   labelKey: 'settings.shortcuts.send_force',   keys: `${mod}+Enter` },
  { id: 'close-popover', labelKey: 'settings.shortcuts.close_popover', keys: 'Esc' },
];
</script>
