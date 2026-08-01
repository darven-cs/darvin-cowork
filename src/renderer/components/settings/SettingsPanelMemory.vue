<template>
  <section class="flex flex-col gap-4" data-testid="settings-memory">
    <h3 class="font-sans text-[15px] font-semibold text-text">{{ t('settings.memory.title') }}</h3>
    <p class="font-sans text-[12.5px] text-text-muted">{{ t('settings.memory.desc') }}</p>

    <label class="flex items-center justify-between gap-3 rounded-md border border-border bg-surface px-3 py-2.5">
      <span class="flex flex-col gap-0.5">
        <span class="font-sans text-[13px] font-medium text-text">{{ t('settings.memory.enabled') }}</span>
        <span class="font-sans text-[11.5px] text-text-muted">{{ t('settings.memory.enabled_desc') }}</span>
      </span>
      <input
        type="checkbox"
        :checked="enabled"
        class="h-4 w-4 cursor-pointer accent-primary"
        data-testid="settings-memory-enabled"
        @change="onEnabled"
      />
    </label>

    <div class="flex flex-col gap-3">
      <label class="flex flex-col gap-1">
        <span class="font-sans text-[12.5px] text-text-muted">{{ t('settings.memory.embedding_provider') }}</span>
        <select
          :value="embeddingProvider"
          class="rounded-md border border-border bg-surface px-3 py-1.5 font-sans text-[13px] text-text outline-none focus:border-border-strong"
          data-testid="settings-memory-provider"
          @change="onProvider"
        >
          <option value="openai">OpenAI</option>
          <option value="local">Local</option>
        </select>
      </label>

      <label class="flex flex-col gap-1">
        <span class="font-sans text-[12.5px] text-text-muted">{{ t('settings.memory.api_key') }}</span>
        <input
          :value="apiKey"
          type="password"
          autocomplete="off"
          spellcheck="false"
          :placeholder="t('settings.memory.api_key_placeholder')"
          class="rounded-md border border-border bg-surface px-3 py-1.5 font-mono text-[13px] text-text outline-none focus:border-border-strong"
          data-testid="settings-memory-apikey"
          @change="onApiKey"
        />
      </label>
    </div>

    <p class="font-sans text-[11.5px] leading-relaxed text-text-subtle">
      {{ t('settings.memory.hint') }}
    </p>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { t } from '../../services/i18n';
import { showToast } from '../../services/toast';

const enabled = ref(false);
const embeddingProvider = ref('openai');
const apiKey = ref('');
const saving = ref(false);

async function persist(patch: Parameters<typeof window.darvin.setAppPreferences>[0], okKey: string) {
  if (saving.value) return;
  saving.value = true;
  try {
    await window.darvin.setAppPreferences(patch);
    showToast(t(okKey), 'success');
  } catch {
    showToast(t('settings.memory.save_failed'), 'error');
  } finally {
    saving.value = false;
  }
}

function onEnabled(e: Event) {
  enabled.value = (e.target as HTMLInputElement).checked;
  void persist({ memory: { enabled: enabled.value } }, 'settings.memory.saved');
}

function onProvider(e: Event) {
  embeddingProvider.value = (e.target as HTMLSelectElement).value;
  void persist({ memory: { embeddingProvider: embeddingProvider.value } }, 'settings.memory.saved');
}

function onApiKey(e: Event) {
  apiKey.value = (e.target as HTMLInputElement).value;
  void persist({ memory: { apiKey: apiKey.value } }, 'settings.memory.saved');
}

onMounted(async () => {
  try {
    const prefs = await window.darvin.getAppPreferences();
    enabled.value = prefs.memory.enabled;
    embeddingProvider.value = prefs.memory.embeddingProvider;
    apiKey.value = prefs.memory.apiKey;
  } catch {
    showToast(t('settings.memory.load_failed'), 'error');
  }
});
</script>
