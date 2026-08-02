<template>
  <section class="flex flex-col gap-3" data-testid="settings-appearance">
    <h3 class="font-sans text-[15px] font-semibold text-text">{{ t('settings.appearance.title') }}</h3>
    <p class="font-sans text-[12.5px] text-text-muted">{{ t('settings.appearance.desc') }}</p>

    <div class="mt-2 flex flex-col gap-2">
      <label
        v-for="opt in themeOptions"
        :key="opt.id"
        class="flex cursor-pointer items-start gap-3 rounded-md border border-border bg-surface px-3 py-2.5 transition-colors hover:border-border-strong"
        :class="opt.id === theme ? 'border-primary bg-primary-soft' : ''"
      >
        <input
          type="radio"
          name="theme"
          :value="opt.id"
          :checked="opt.id === theme"
          class="mt-0.5 h-4 w-4 cursor-pointer accent-primary"
          @change="applyTheme(opt.id)"
        />
        <span class="flex flex-col gap-0.5">
          <span class="font-sans text-[13px] font-medium text-text">{{ t(opt.labelKey) }}</span>
          <span class="font-sans text-[11.5px] text-text-muted">{{ t(opt.descKey) }}</span>
        </span>
      </label>
    </div>

    <h3 class="mt-4 font-sans text-[15px] font-semibold text-text">{{ t('settings.appearance.accent') }}</h3>
    <p class="font-sans text-[12.5px] text-text-muted">{{ t('settings.appearance.accent_desc') }}</p>

    <div class="flex flex-col gap-2">
      <label
        v-for="opt in accentOptions"
        :key="opt.id"
        class="flex cursor-pointer items-start gap-3 rounded-md border border-border bg-surface px-3 py-2.5 transition-colors hover:border-border-strong"
        :class="opt.id === accentColor ? 'border-primary bg-primary-soft' : ''"
      >
        <input
          type="radio"
          name="accent"
          :value="opt.id"
          :checked="opt.id === accentColor"
          class="mt-0.5 h-4 w-4 cursor-pointer accent-primary"
          @change="applyAccent(opt.id)"
        />
        <span class="flex items-center gap-2">
          <span class="h-4 w-4 rounded-full" :class="`bg-accent-${opt.id}`" />
          <span class="font-sans text-[13px] font-medium text-text">{{ t(opt.labelKey) }}</span>
        </span>
      </label>
    </div>

    <h3 class="mt-4 font-sans text-[15px] font-semibold text-text">{{ t('settings.appearance.font_size') }}</h3>
    <p class="font-sans text-[12.5px] text-text-muted">{{ t('settings.appearance.font_size_desc') }}</p>

    <div class="mt-1 flex flex-col gap-3 rounded-md border border-border bg-surface px-3 py-2.5">
      <label class="flex items-center justify-between gap-3">
        <span class="font-sans text-[13px] text-text">{{ t('settings.appearance.ui_font') }}</span>
        <span class="font-mono text-[12.5px] text-text-muted">{{ uiFontSize }}px</span>
      </label>
      <input
        type="range"
        :value="uiFontSize"
        min="11"
        max="16"
        step="1"
        class="w-full cursor-pointer accent-primary"
        data-testid="settings-appearance-ui-font"
        @input="onUiFont"
      />
      <label class="flex items-center justify-between gap-3">
        <span class="font-sans text-[13px] text-text">{{ t('settings.appearance.code_font') }}</span>
        <span class="font-mono text-[12.5px] text-text-muted">{{ codeFontSize }}px</span>
      </label>
      <input
        type="range"
        :value="codeFontSize"
        min="8"
        max="24"
        step="1"
        class="w-full cursor-pointer accent-primary"
        data-testid="settings-appearance-code-font"
        @input="onCodeFont"
      />
    </div>

    <h3 class="mt-4 font-sans text-[15px] font-semibold text-text">{{ t('settings.appearance.language') }}</h3>
    <p class="font-sans text-[12.5px] text-text-muted">{{ t('settings.appearance.language_desc') }}</p>

    <div class="flex flex-col gap-2">
      <label
        v-for="opt in langOptions"
        :key="opt.id"
        class="flex cursor-pointer items-start gap-3 rounded-md border border-border bg-surface px-3 py-2.5 transition-colors hover:border-border-strong"
        :class="opt.id === lang ? 'border-primary bg-primary-soft' : ''"
      >
        <input
          type="radio"
          name="language"
          :value="opt.id"
          :checked="opt.id === lang"
          class="mt-0.5 h-4 w-4 cursor-pointer accent-primary"
          @change="applyLang(opt.id)"
        />
        <span class="flex flex-col gap-0.5">
          <span class="font-sans text-[13px] font-medium text-text">{{ t(opt.labelKey) }}</span>
        </span>
      </label>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { useAppearance, type AccentColor } from '../../composables/useAppearance';
import { useTheme } from '../../composables/useTheme';
import { getLang, setLang, t } from '../../services/i18n';

const { theme, apply: applyTheme } = useTheme();
const { uiFontSize, codeFontSize, accentColor, setUiFontSize, setCodeFontSize, setAccent } = useAppearance();

const lang = computed(() => getLang());

const themeOptions: { id: 'light' | 'dark'; labelKey: string; descKey: string }[] = [
  { id: 'light', labelKey: 'settings.appearance.theme.light', descKey: 'settings.appearance.theme.light_desc' },
  { id: 'dark',  labelKey: 'settings.appearance.theme.dark',  descKey: 'settings.appearance.theme.dark_desc' },
];

const accentOptions: { id: AccentColor; labelKey: string }[] = [
  { id: 'orange', labelKey: 'settings.appearance.accent.orange' },
  { id: 'blue',   labelKey: 'settings.appearance.accent.blue' },
  { id: 'green',  labelKey: 'settings.appearance.accent.green' },
];

const langOptions: { id: 'zh' | 'en'; labelKey: string }[] = [
  { id: 'zh', labelKey: 'settings.appearance.lang.zh' },
  { id: 'en', labelKey: 'settings.appearance.lang.en' },
];

function onUiFont(e: Event) {
  setUiFontSize(Number((e.target as HTMLInputElement).value));
}

function onCodeFont(e: Event) {
  setCodeFontSize(Number((e.target as HTMLInputElement).value));
}

function applyAccent(id: AccentColor) {
  setAccent(id);
}

async function applyLang(id: 'zh' | 'en') {
  if (id === lang.value) return;
  await window.darvin.setLocale({ locale: id });
  setLang(id);
}
</script>
