<template>
  <section class="flex flex-col gap-4" data-testid="settings-runtime">
    <h3 class="font-sans text-[15px] font-semibold text-text">{{ t('settings.runtime.title') }}</h3>
    <p class="font-sans text-[12.5px] text-text-muted">{{ t('settings.runtime.desc') }}</p>

    <div class="flex flex-col gap-1.5 rounded-md border border-border bg-surface px-3 py-2.5">
      <div class="flex items-center justify-between">
        <span class="font-sans text-[12.5px] text-text-muted">{{ t('settings.runtime.status') }}</span>
        <span class="flex items-center gap-1.5">
          <span class="h-2 w-2 rounded-full" :class="statusDotClass" />
          <span class="font-sans text-[12.5px]" :class="statusTextClass">{{ statusLabel }}</span>
        </span>
      </div>
      <div class="flex items-center justify-between">
        <span class="font-sans text-[12.5px] text-text-muted">{{ t('settings.runtime.provider') }}</span>
        <span class="font-mono text-[12.5px] text-text">{{ providerLabel }}</span>
      </div>
      <div class="flex items-center justify-between">
        <span class="font-sans text-[12.5px] text-text-muted">{{ t('settings.runtime.workspace') }}</span>
        <span class="font-mono text-[12.5px] text-text">{{ workspaceLabel }}</span>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import type { DarvinRuntimeStatus } from '../../../shared/darvin-api';
import { t } from '../../services/i18n';

const status = ref<DarvinRuntimeStatus | null>(null);
const provider = ref('');
const workspace = ref('');

const STATUS_KEY: Record<DarvinRuntimeStatus, string> = {
  ready: 'settings.runtime.status.ready',
  online: 'settings.runtime.status.online',
  offline: 'settings.runtime.status.offline',
  'no-binary': 'settings.runtime.status.no_binary',
};

const statusLabel = computed(() => (status.value ? t(STATUS_KEY[status.value]) : '…'));

const statusDotClass = computed(() => {
  switch (status.value) {
    case 'ready':
    case 'online':
      return 'bg-success';
    case 'offline':
      return 'bg-warning';
    case 'no-binary':
      return 'bg-danger';
    default:
      return 'bg-border-strong';
  }
});

const statusTextClass = computed(() => {
  switch (status.value) {
    case 'ready':
    case 'online':
      return 'text-success';
    case 'offline':
      return 'text-warning';
    case 'no-binary':
      return 'text-danger';
    default:
      return 'text-text-muted';
  }
});

const providerLabel = computed(() => provider.value || '—');

const workspaceLabel = computed(() => workspace.value || '—');

onMounted(async () => {
  try {
    status.value = await window.darvin.status();
  } catch {
    status.value = 'offline';
  }
  try {
    const cfg = await window.darvin.getLLMConfig();
    provider.value = cfg.activeProvider;
  } catch {
    provider.value = '';
  }
  try {
    const info = await window.darvin.getWorkspaceInfo();
    workspace.value = info.label ?? '';
  } catch {
    workspace.value = '';
  }
});
</script>
