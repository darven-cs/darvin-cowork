<template>
  <section class="flex flex-col gap-3">
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
          <span class="font-sans text-[13px] font-medium text-text">{{ opt.label }}</span>
          <span class="font-sans text-[11.5px] text-text-muted">{{ opt.desc }}</span>
        </span>
      </label>
    </div>

    <h3 class="mt-6 font-sans text-[15px] font-semibold text-text">{{ t('settings.appearance.language') }}</h3>
    <p class="font-sans text-[12.5px] text-text-muted">{{ t('settings.appearance.language_desc') }}</p>

    <div class="mt-2 flex flex-col gap-2">
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
          <span class="font-sans text-[13px] font-medium text-text">{{ opt.label }}</span>
        </span>
      </label>
    </div>
  </section>
</template>

<script setup lang="ts">
import { useTheme } from '../../composables/useTheme';
import { getLang, setLang } from '../../services/i18n';

const { theme, apply: applyTheme } = useTheme();

const lang = getLang();

const themeOptions: { id: 'light' | 'dark'; label: string; desc: string }[] = [
  { id: 'light', label: '浅色', desc: '默认白底 + 红色龙虾品牌色' },
  { id: 'dark',  label: '深色', desc: '深底 + 亮色龙虾品牌色' },
];

// 语言标签保持原语种显示，避免翻译导致无法识别；描述可走 t()。
const langOptions: { id: 'zh' | 'en'; label: string }[] = [
  { id: 'zh', label: '中文' },
  { id: 'en', label: 'English' },
];

async function applyLang(id: 'zh' | 'en') {
  if (id === lang) return;
  await window.darvin.setLocale({ locale: id });
  setLang(id);
}
</script>