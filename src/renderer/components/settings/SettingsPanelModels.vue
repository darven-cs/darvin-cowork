<template>
  <section class="flex flex-col gap-4" data-testid="settings-models">
    <h3 class="font-sans text-[15px] font-semibold text-text">{{ t('settings.models.title') }}</h3>
    <p class="font-sans text-[12.5px] text-text-muted">{{ t('settings.models.desc') }}</p>

    <div class="mt-2 flex flex-col gap-3" data-testid="settings-models-form">
      <label class="flex flex-col gap-1">
        <span class="font-sans text-[12.5px] text-text-muted">{{ t('settings.models.provider') }}</span>
        <select
          :value="selectedProvider"
          class="rounded-md border border-border bg-surface px-3 py-1.5 font-sans text-[13px] text-text outline-none focus:border-border-strong"
          data-testid="settings-models-provider"
          @change="onProviderChange"
        >
          <option value="anthropic">Anthropic</option>
          <option value="openai">OpenAI</option>
          <option value="custom">Custom</option>
        </select>
      </label>

      <p
        v-if="selectedProvider !== activeProvider"
        class="rounded-md border border-warning/30 bg-warning/10 px-3 py-2 font-sans text-[12px] text-warning"
        data-testid="settings-models-pending-note"
      >
        {{ t('settings.models.pending_note') }}
      </p>

      <label class="flex flex-col gap-1">
        <span class="font-sans text-[12.5px] text-text-muted">{{ t('settings.models.api_key') }}</span>
        <input
          v-model="apiKey"
          type="password"
          :placeholder="t('settings.models.api_key_placeholder')"
          autocomplete="off"
          spellcheck="false"
          class="rounded-md border border-border bg-surface px-3 py-1.5 font-mono text-[13px] text-text outline-none focus:border-border-strong"
          data-testid="settings-models-apikey"
          @input="dirty = true"
        />
      </label>

      <label class="flex flex-col gap-1">
        <span class="font-sans text-[12.5px] text-text-muted">{{ t('settings.models.base_url') }}</span>
        <input
          v-model="baseUrl"
          type="text"
          :placeholder="t('settings.models.base_url_placeholder')"
          autocomplete="off"
          spellcheck="false"
          class="rounded-md border border-border bg-surface px-3 py-1.5 font-mono text-[13px] text-text outline-none focus:border-border-strong"
          data-testid="settings-models-baseurl"
          @input="dirty = true"
        />
      </label>

      <label class="flex flex-col gap-1">
        <span class="font-sans text-[12.5px] text-text-muted">{{ t('settings.models.default_model') }}</span>
        <input
          v-model="defaultModel"
          type="text"
          :placeholder="t('settings.models.default_model_placeholder')"
          autocomplete="off"
          spellcheck="false"
          class="rounded-md border border-border bg-surface px-3 py-1.5 font-mono text-[13px] text-text outline-none focus:border-border-strong"
          data-testid="settings-models-model"
          @input="dirty = true"
        />
      </label>

      <p
        v-if="status"
        class="font-sans text-[12px]"
        :class="statusOk ? 'text-success' : 'text-danger'"
        data-testid="settings-models-status"
      >
        {{ status }}
      </p>
    </div>

    <div class="mt-1 flex items-center gap-2">
      <button
        type="button"
        :disabled="!dirty || saving"
        class="rounded-md bg-primary px-3 py-1.5 font-sans text-[13px] font-medium text-white transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-50"
        data-testid="settings-models-save"
        @click="onSave"
      >
        {{ saving ? t('settings.models.saving') : t('settings.models.save') }}
      </button>
      <button
        type="button"
        :disabled="!dirty || saving"
        class="rounded-md border border-border px-3 py-1.5 font-sans text-[13px] text-text-muted transition-colors hover:bg-surface-hover hover:text-text disabled:cursor-not-allowed disabled:opacity-50"
        data-testid="settings-models-reset"
        @click="onReset"
      >
        {{ t('settings.models.reset') }}
      </button>
    </div>

    <p class="mt-2 font-sans text-[11.5px] text-text-subtle">
      {{ t('settings.models.path_hint') }}
      <code class="font-mono text-[11.5px] text-text-muted">{{ pathHint }}</code>
    </p>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue';
import type { DarvinModelProvider } from '../../../shared/darvin-api';
import { t } from '../../services/i18n';

const selectedProvider = ref<DarvinModelProvider>('anthropic');
const activeProvider = ref('anthropic');
const apiKey = ref<string>('');
const baseUrl = ref<string>('');
const defaultModel = ref<string>('');
const dirty = ref<boolean>(false);
const saving = ref<boolean>(false);
const status = ref<string>('');
const statusOk = ref<boolean>(false);
const pathHint = ref<string>('');

let savedSnapshot = { provider: 'anthropic' as DarvinModelProvider, apiKey: '', baseUrl: '', defaultModel: '' };

onMounted(async () => {
  try {
    const cfg = await window.darvin.getLLMConfig();
    activeProvider.value = cfg.activeProvider;
    loadProvider(cfg, cfg.provider);
    savedSnapshot = { provider: selectedProvider.value, apiKey: apiKey.value, baseUrl: baseUrl.value, defaultModel: defaultModel.value };
    dirty.value = false;
  } catch (e) {
    status.value = `${t('settings.models.load_failed')}: ${(e as Error).message}`;
    statusOk.value = false;
  }
});

function loadProvider(cfg: Awaited<ReturnType<typeof window.darvin.getLLMConfig>>, provider: DarvinModelProvider) {
  selectedProvider.value = provider;
  if (provider === 'anthropic') {
    apiKey.value = cfg.apiKey;
    baseUrl.value = cfg.baseUrl;
    defaultModel.value = cfg.defaultModel;
  } else {
    const entry = cfg.providers[provider] ?? { apiKey: '', baseUrl: '', defaultModel: '' };
    apiKey.value = entry.apiKey;
    baseUrl.value = entry.baseUrl;
    defaultModel.value = entry.defaultModel;
  }
}

function onProviderChange(e: Event) {
  const next = (e.target as HTMLSelectElement).value as DarvinModelProvider;
  void window.darvin.getLLMConfig().then((cfg) => {
    loadProvider(cfg, next);
    savedSnapshot = { provider: next, apiKey: apiKey.value, baseUrl: baseUrl.value, defaultModel: defaultModel.value };
    dirty.value = false;
    status.value = '';
  });
}

function onReset() {
  selectedProvider.value = savedSnapshot.provider;
  apiKey.value = savedSnapshot.apiKey;
  baseUrl.value = savedSnapshot.baseUrl;
  defaultModel.value = savedSnapshot.defaultModel;
  dirty.value = false;
  status.value = '';
}

async function onSave() {
  saving.value = true;
  status.value = '';
  try {
    const r = await window.darvin.setLLMConfig({
      provider: selectedProvider.value,
      apiKey: apiKey.value,
      baseUrl: baseUrl.value,
      defaultModel: defaultModel.value,
    });
    if (r.saved && r.restarted) {
      status.value = t('settings.models.saved_restarted');
      statusOk.value = true;
      savedSnapshot = { provider: selectedProvider.value, apiKey: apiKey.value, baseUrl: baseUrl.value, defaultModel: defaultModel.value };
      dirty.value = false;
    } else if (r.saved && selectedProvider.value !== 'anthropic') {
      status.value = t('settings.models.saved_pending');
      statusOk.value = false;
      savedSnapshot = { provider: selectedProvider.value, apiKey: apiKey.value, baseUrl: baseUrl.value, defaultModel: defaultModel.value };
      dirty.value = false;
    } else if (r.saved) {
      status.value = t('settings.models.saved_no_restart');
      statusOk.value = false;
    } else {
      status.value = t('settings.models.save_failed');
      statusOk.value = false;
    }
  } catch (e) {
    status.value = `${t('settings.models.save_failed')}: ${(e as Error).message}`;
    statusOk.value = false;
  } finally {
    saving.value = false;
  }
}
</script>
