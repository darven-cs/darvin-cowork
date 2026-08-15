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
          <optgroup v-for="region in regions" :key="region.key" :label="region.label">
            <option v-for="p in region.providers" :key="p.id" :value="p.id">{{ p.label }}</option>
          </optgroup>
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
          :placeholder="selectedPreset?.apiKeyPlaceholder ?? t('settings.models.api_key_placeholder')"
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
          :placeholder="selectedPreset?.defaultBaseUrl || t('settings.models.base_url_placeholder')"
          autocomplete="off"
          spellcheck="false"
          class="rounded-md border border-border bg-surface px-3 py-1.5 font-mono text-[13px] text-text outline-none focus:border-border-strong"
          data-testid="settings-models-baseurl"
          @input="dirty = true"
        />
      </label>

      <label v-if="availableFormats.length > 1" class="flex flex-col gap-1">
        <span class="font-sans text-[12.5px] text-text-muted">{{ t('settings.models.api_format') }}</span>
        <select
          :value="apiFormat"
          class="rounded-md border border-border bg-surface px-3 py-1.5 font-mono text-[13px] text-text outline-none focus:border-border-strong"
          data-testid="settings-models-apiformat"
          @change="onApiFormatChange"
        >
          <option v-for="f in availableFormats" :key="f" :value="f">{{ f }}</option>
        </select>
      </label>

      <label v-if="selectedProvider !== 'custom'" class="flex flex-col gap-1">
        <span class="font-sans text-[12.5px] text-text-muted">{{ t('settings.models.default_model') }}</span>
        <select
          v-model="defaultModel"
          class="rounded-md border border-border bg-surface px-3 py-1.5 font-mono text-[13px] text-text outline-none focus:border-border-strong"
          data-testid="settings-models-model"
          @change="dirty = true"
        >
          <option v-for="m in modelOptions" :key="m.id" :value="m.id">{{ m.label }}</option>
        </select>
      </label>
      <label v-else class="flex flex-col gap-1">
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
        v-if="validationError"
        class="font-sans text-[12px] text-danger"
        data-testid="settings-models-validation-error"
      >
        {{ validationError }}
      </p>

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
import { computed, onMounted, ref } from 'vue';
import type { DarvinLLMConfig, DarvinModelInfo } from '../../../shared/darvin-api';
import {
  DARVIN_PROVIDERS,
  darvinProviderModels,
  darvinProviderPreset,
  type DarvinProviderPreset,
} from '../../../shared/providers';
import { t } from '../../services/i18n';

const regions = computed(() => {
  const keys: Array<'china' | 'global'> = ['china', 'global'];
  return keys.map((key) => ({
    key,
    label: t(`settings.models.region.${key}`),
    providers: DARVIN_PROVIDERS.filter((p) => p.region === key),
  }));
});

const selectedProvider = ref<string>('anthropic');
const activeProvider = ref('anthropic');
const selectedPreset = computed<DarvinProviderPreset | undefined>(() => darvinProviderPreset(selectedProvider.value));
const apiKey = ref<string>('');
const baseUrl = ref<string>('');
const defaultModel = ref<string>('');
const apiFormat = ref<string>('anthropic');
const catalogModels = ref<DarvinModelInfo[]>([]);
const dirty = ref<boolean>(false);
const saving = ref<boolean>(false);
const status = ref<string>('');
const statusOk = ref<boolean>(false);
const validationError = ref<string>('');
const pathHint = ref<string>('');

let savedSnapshot = { provider: 'anthropic', apiKey: '', baseUrl: '', defaultModel: '', apiFormat: 'anthropic' };

/** 该 provider 可选的 wire 格式：默认 + switchableBaseUrls 的键。 */
const availableFormats = computed<string[]>(() => {
  const preset = selectedPreset.value;
  if (!preset) return [apiFormat.value];
  const set = new Set<string>([preset.apiFormat]);
  for (const f of Object.keys(preset.switchableBaseUrls ?? {})) set.add(f);
  return [...set];
});

/** 模型下拉：Go 目录中属于当前 provider 的 + 该 preset 的 defaultModels。 */
const modelOptions = computed(() => {
  const preset = selectedPreset.value;
  if (!preset) return [];
  const out: Array<{ id: string; label: string }> = [];
  const seen = new Set<string>();
  for (const m of catalogModels.value) {
    if (m.provider !== preset.id) continue;
    out.push({ id: m.id, label: m.name });
    seen.add(m.id);
  }
  for (const m of darvinProviderModels(preset.id)) {
    if (seen.has(m.id)) continue;
    out.push({ id: m.id, label: m.label });
  }
  return out;
});

onMounted(async () => {
  try {
    const [cfg, models] = await Promise.all([
      window.darvin.getLLMConfig(),
      window.darvin.getLLMModels().catch(() => [] as DarvinModelInfo[]),
    ]);
    catalogModels.value = models;
    activeProvider.value = cfg.activeProvider;
    loadProvider(cfg, cfg.activeProvider);
    savedSnapshot = { provider: selectedProvider.value, apiKey: apiKey.value, baseUrl: baseUrl.value, defaultModel: defaultModel.value, apiFormat: apiFormat.value };
    dirty.value = false;
  } catch (e) {
    status.value = `${t('settings.models.load_failed')}: ${(e as Error).message}`;
    statusOk.value = false;
  }
});

function loadProvider(cfg: DarvinLLMConfig, provider: string): void {
  selectedProvider.value = provider;
  const entry = cfg.providers[provider];
  apiFormat.value = entry?.apiFormat || darvinProviderPreset(provider)?.apiFormat || 'anthropic';
  if (entry) {
    apiKey.value = entry.apiKey;
    baseUrl.value = entry.baseUrl || darvinProviderPreset(provider)?.defaultBaseUrl || '';
    defaultModel.value = entry.defaultModel;
  } else {
    apiKey.value = cfg.apiKey;
    baseUrl.value = cfg.baseUrl || darvinProviderPreset(provider)?.defaultBaseUrl || '';
    defaultModel.value = cfg.defaultModel;
  }
  if (!defaultModel.value) {
    defaultModel.value = darvinProviderModels(provider)[0]?.id ?? '';
  }
  validationError.value = '';
}

function onProviderChange(e: Event): void {
  const next = (e.target as HTMLSelectElement).value;
  // 切换 preset：回到默认 apiFormat / 模型 / base URL。
  const preset = darvinProviderPreset(next);
  selectedProvider.value = next;
  apiFormat.value = preset?.apiFormat ?? 'anthropic';
  defaultModel.value = darvinProviderModels(next)[0]?.id ?? '';
  if (preset) {
    baseUrl.value = preset.defaultBaseUrl;
  }
  validationError.value = '';
  dirty.value = true;
}

/** 切换 wire 格式：provider 有 switchableBaseUrls 时 base URL 自动跟随。 */
function onApiFormatChange(e: Event): void {
  const next = (e.target as HTMLSelectElement).value;
  apiFormat.value = next;
  const preset = selectedPreset.value;
  if (preset && preset.switchableBaseUrls?.[next as 'openai' | 'anthropic']) {
    baseUrl.value = preset.switchableBaseUrls[next as 'openai' | 'anthropic'];
  }
  dirty.value = true;
}

function onReset(): void {
  selectedProvider.value = savedSnapshot.provider;
  apiKey.value = savedSnapshot.apiKey;
  baseUrl.value = savedSnapshot.baseUrl;
  defaultModel.value = savedSnapshot.defaultModel;
  apiFormat.value = savedSnapshot.apiFormat;
  dirty.value = false;
  validationError.value = '';
  status.value = '';
}

async function onSave(): Promise<void> {
  const preset = selectedPreset.value;
  if (!preset) {
    validationError.value = t('settings.models.save_failed');
    return;
  }
  if (preset.apiKeyRequired && !apiKey.value.trim()) {
    validationError.value = t('settings.models.api_key_required');
    return;
  }
  const effectiveBase = baseUrl.value || preset.defaultBaseUrl;
  if (preset.requiresBaseUrl && !effectiveBase) {
    validationError.value = t('settings.models.custom_base_url_required');
    return;
  }

  saving.value = true;
  status.value = '';
  validationError.value = '';
  try {
    const r = await window.darvin.setLLMConfig({
      provider: selectedProvider.value,
      apiKey: apiKey.value,
      baseUrl: effectiveBase,
      defaultModel: defaultModel.value,
      apiFormat: apiFormat.value,
    });
    if (!r.saved) {
      status.value = t('settings.models.save_failed');
      statusOk.value = false;
      return;
    }
    if (r.restarted) {
      status.value = t('settings.models.saved_restarted');
      statusOk.value = true;
      activeProvider.value = selectedProvider.value;
      savedSnapshot = { provider: selectedProvider.value, apiKey: apiKey.value, baseUrl: baseUrl.value, defaultModel: defaultModel.value, apiFormat: apiFormat.value };
      dirty.value = false;
    } else {
      status.value = t('settings.models.saved_no_restart');
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
