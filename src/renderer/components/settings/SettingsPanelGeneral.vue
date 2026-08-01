<template>
  <section class="flex flex-col gap-4" data-testid="settings-general">
    <h3 class="font-sans text-[15px] font-semibold text-text">{{ t('settings.general.title') }}</h3>
    <p class="font-sans text-[12.5px] text-text-muted">{{ t('settings.general.desc') }}</p>

    <div class="mt-2 flex flex-col gap-3">
      <label class="flex items-center justify-between gap-3 rounded-md border border-border bg-surface px-3 py-2.5">
        <span class="flex flex-col gap-0.5">
          <span class="font-sans text-[13px] font-medium text-text">{{ t('settings.general.auto_launch') }}</span>
          <span class="font-sans text-[11.5px] text-text-muted">{{ t('settings.general.auto_launch_desc') }}</span>
        </span>
        <input
          type="checkbox"
          :checked="autoLaunch"
          class="h-4 w-4 cursor-pointer accent-primary"
          data-testid="settings-general-autolaunch"
          @change="onAutoLaunch"
        />
      </label>

      <label class="flex items-center justify-between gap-3 rounded-md border border-border bg-surface px-3 py-2.5">
        <span class="flex flex-col gap-0.5">
          <span class="font-sans text-[13px] font-medium text-text">{{ t('settings.general.notifications') }}</span>
          <span class="font-sans text-[11.5px] text-text-muted">{{ t('settings.general.notifications_desc') }}</span>
        </span>
        <input
          type="checkbox"
          :checked="notifications"
          class="h-4 w-4 cursor-pointer accent-primary"
          data-testid="settings-general-notifications"
          @change="onNotifications"
        />
      </label>

      <label class="flex flex-col gap-1">
        <span class="font-sans text-[12.5px] text-text-muted">{{ t('settings.general.proxy') }}</span>
        <input
          v-model="proxy"
          type="text"
          spellcheck="false"
          :placeholder="t('settings.general.proxy_placeholder')"
          class="rounded-md border border-border bg-surface px-3 py-1.5 font-mono text-[13px] text-text outline-none focus:border-border-strong"
          data-testid="settings-general-proxy"
          @change="onProxy"
        />
      </label>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { t } from '../../services/i18n';
import { showToast } from '../../services/toast';

const autoLaunch = ref(false);
const notifications = ref(true);
const proxy = ref('');
const saving = ref(false);

async function persist(patch: Parameters<typeof window.darvin.setAppPreferences>[0], okKey: string) {
  if (saving.value) return;
  saving.value = true;
  try {
    await window.darvin.setAppPreferences(patch);
    showToast(t(okKey), 'success');
  } catch {
    showToast(t('settings.general.save_failed'), 'error');
  } finally {
    saving.value = false;
  }
}

function onAutoLaunch(e: Event) {
  const next = (e.target as HTMLInputElement).checked;
  autoLaunch.value = next;
  void persist({ autoLaunch: next }, 'settings.general.saved');
}

function onNotifications(e: Event) {
  const next = (e.target as HTMLInputElement).checked;
  notifications.value = next;
  void persist({ notifications: next }, 'settings.general.saved');
}

function onProxy() {
  void persist({ proxy: proxy.value }, 'settings.general.saved');
}

onMounted(async () => {
  try {
    const prefs = await window.darvin.getAppPreferences();
    autoLaunch.value = prefs.autoLaunch;
    notifications.value = prefs.notifications;
    proxy.value = prefs.proxy;
  } catch {
    showToast(t('settings.general.load_failed'), 'error');
  }
});
</script>
