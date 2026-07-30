<template>
  <section class="flex flex-col gap-3">
    <h3 class="font-sans text-[15px] font-semibold text-text">{{ t('settings.appearance.title') }}</h3>
    <p class="font-sans text-[12.5px] text-text-muted">{{ t('settings.appearance.desc') }}</p>

    <div class="mt-2 flex flex-col gap-2">
      <label
        v-for="opt in options"
        :key="opt.id"
        class="flex cursor-pointer items-start gap-3 rounded-md border border-border bg-surface px-3 py-2.5 transition-colors hover:border-border-strong"
        :class="opt.id === current ? 'border-primary bg-primary-soft' : ''"
      >
        <input
          type="radio"
          name="theme"
          :value="opt.id"
          :checked="opt.id === current"
          class="mt-0.5 h-4 w-4 cursor-pointer accent-primary"
          @change="onPick(opt.id)"
        />
        <span class="flex flex-col gap-0.5">
          <span class="font-sans text-[13px] font-medium text-text">{{ opt.label }}</span>
          <span class="font-sans text-[11.5px] text-text-muted">{{ opt.desc }}</span>
        </span>
      </label>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { t } from '../../services/i18n';
import { useTheme } from '../../composables/useTheme';

const { theme, apply } = useTheme();
const current = computed(() => theme.value);

const options: { id: 'light' | 'dark'; label: string; desc: string }[] = [
  { id: 'light', label: '浅色', desc: '默认白底 + 红色龙虾品牌色' },
  { id: 'dark',  label: '深色', desc: '深底 + 亮色龙虾品牌色' },
];

function onPick(id: 'light' | 'dark') {
  apply(id);
}
</script>