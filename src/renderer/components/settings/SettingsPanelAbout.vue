<template>
  <section class="flex flex-col gap-4" data-testid="settings-about">
    <div class="flex flex-col gap-2">
      <h3 class="font-sans text-[15px] font-semibold text-text">{{ t('settings.about.title') }}</h3>
      <p class="font-sans text-[12.5px] text-text-muted">{{ t('settings.about.desc') }}</p>
    </div>

    <div class="flex flex-col gap-1.5 rounded-md border border-border bg-surface px-3 py-2.5">
      <div class="flex items-center justify-between">
        <span class="font-sans text-[12.5px] text-text-muted">{{ t('settings.about.version') }}</span>
        <span class="font-mono text-[12.5px] text-text">{{ version || '—' }}</span>
      </div>
      <div class="flex items-center justify-between">
        <span class="font-sans text-[12.5px] text-text-muted">{{ t('settings.about.runtime') }}</span>
        <span class="font-mono text-[12.5px] text-text">darvin-agent (Go)</span>
      </div>
      <div class="flex items-center justify-between">
        <span class="font-sans text-[12.5px] text-text-muted">{{ t('settings.about.electron') }}</span>
        <span class="font-mono text-[12.5px] text-text">{{ electron || '—' }}</span>
      </div>
      <div class="flex items-center justify-between">
        <span class="font-sans text-[12.5px] text-text-muted">{{ t('settings.about.platform') }}</span>
        <span class="font-mono text-[12.5px] text-text">{{ platformLabel }}</span>
      </div>
    </div>

    <div class="flex flex-col gap-1.5">
      <h4 class="font-sans text-[13px] font-medium text-text">{{ t('settings.about.compaction_title') }}</h4>
      <div class="flex flex-col gap-1.5 rounded-md border border-border bg-surface px-3 py-2.5">
        <div class="flex items-center justify-between">
          <span class="font-sans text-[12.5px] text-text-muted">{{ t('settings.about.compaction_count') }}</span>
          <span class="font-mono text-[12.5px] text-text">{{ compactionCount }}</span>
        </div>
        <div class="flex items-center justify-between">
          <span class="font-sans text-[12.5px] text-text-muted">{{ t('settings.about.compaction_latest') }}</span>
          <span class="font-mono text-[12.5px] text-text">{{ latestCompactionLabel }}</span>
        </div>
      </div>
    </div>

    <div class="flex flex-col gap-1.5">
      <h4 class="font-sans text-[13px] font-medium text-text">{{ t('settings.about.logs_title') }}</h4>
      <p class="font-sans text-[12.5px] text-text-muted">{{ t('settings.about.logs_desc') }}</p>
      <button
        type="button"
        class="self-start rounded-md border border-border px-3 py-1.5 font-sans text-[13px] text-text-muted transition-colors hover:bg-surface-hover hover:text-text"
        data-testid="settings-about-export-logs"
        @click="onExport"
      >
        {{ t('settings.about.export_logs') }}
      </button>
    </div>

    <div class="flex flex-col gap-1.5">
      <h4 class="font-sans text-[13px] font-medium text-text">{{ t('settings.about.arch_title') }}</h4>
      <p class="font-sans text-[12.5px] leading-[1.6] text-text-muted">
        {{ t('settings.about.arch_desc') }}
      </p>
    </div>

    <div class="flex flex-col gap-1.5">
      <h4 class="font-sans text-[13px] font-medium text-text">{{ t('settings.about.licenses_title') }}</h4>
      <ul class="flex flex-col gap-0.5 font-sans text-[12.5px] text-text-muted">
        <li>Vue 3 — MIT</li>
        <li>Tailwind CSS v4 — MIT</li>
        <li>Electron — MIT</li>
        <li>darvin-agent — MIT</li>
      </ul>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useMessages } from '../../composables/useMessages';
import { formatDate, getLang, t } from '../../services/i18n';
import { showToast } from '../../services/toast';

const { compactionsBySessionId } = useMessages();

const version = ref('');
const electron = ref('');
const platform = ref('');
const arch = ref('');

const platformLabel = computed(() => {
  if (!platform.value) return '—';
  return `${platform.value} ${arch.value}`;
});

const compactionCount = computed(() => {
  let total = 0;
  for (const markers of Object.values(compactionsBySessionId.value)) {
    total += markers.length;
  }
  return total;
});

const latestCompactionLabel = computed(() => {
  let latest = 0;
  for (const markers of Object.values(compactionsBySessionId.value)) {
    for (const m of markers) {
      if (m.createdAt > latest) latest = m.createdAt;
    }
  }
  if (!latest) return '—';
  return formatDate(latest, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  });
});

async function onExport() {
  const lines = [
    `darvin-cowork v${version.value || '?'}`,
    `Electron ${electron.value || '?'}`,
    `Platform ${platformLabel.value}`,
    `Compactions ${compactionCount.value}`,
    `Locale ${getLang()}`,
  ];
  try {
    await navigator.clipboard.writeText(lines.join('\n'));
    showToast(t('settings.about.logs_copied'), 'success');
  } catch {
    showToast(t('settings.about.logs_copy_failed'), 'error');
  }
}

onMounted(async () => {
  try {
    const info = await window.darvin.getAppInfo();
    version.value = info.version;
    electron.value = info.electron;
    platform.value = info.platform;
    arch.value = info.arch;
  } catch {
    /* IPC 失败则保持占位 */
  }
});
</script>
