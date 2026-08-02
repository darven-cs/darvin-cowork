<template>
  <div class="relative">
    <button
      type="button"
      :aria-label="t('model.label')"
      class="inline-flex h-7 items-center gap-1.5 rounded-md px-2 text-[13px] text-text-muted transition-colors hover:bg-surface-2 hover:text-text"
      @click="fp.toggle('model')"
    >
      <Icon name="bolt" :size="13" />
      <span class="font-medium">{{ currentLabel }}</span>
      <Icon name="chevron-down" :size="12" />
    </button>

    <div
      v-if="isOpen"
      class="absolute bottom-full left-0 mb-2 w-72 rounded-xl border border-border bg-surface p-2 shadow-lg"
      @click.stop
    >
      <div class="px-2 py-1.5 font-mono text-[10px] uppercase tracking-wider text-text-subtle">
        {{ t('model.menu.title') }}
      </div>
      <input
        ref="searchRef"
        v-model="query"
        type="text"
        :placeholder="t('model.search.placeholder')"
        class="mb-1 w-full rounded-md border border-border bg-surface-2 px-2.5 py-1.5 font-sans text-[13px] text-text outline-none placeholder:text-text-subtle focus:border-border-strong"
        @input="onSearchInput"
        @keydown.escape.stop="fp.close()"
      />
      <div class="max-h-56 overflow-y-auto">
        <button
          v-for="m in filteredModels"
          :key="m.id"
          type="button"
          class="flex w-full items-start gap-2.5 rounded-md px-2 py-1.5 text-left transition-colors hover:bg-surface-2"
          :class="m.id === model.currentModel.value ? 'bg-primary-soft' : ''"
          @click="onPick(m.id)"
        >
          <span
            class="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-[10px] font-bold text-white"
            :class="m.vendor === 'anthropic' ? 'bg-vendor-anthropic' : 'bg-vendor-openai'"
          >
            {{ m.vendor === 'anthropic' ? 'A' : 'O' }}
          </span>
          <span class="flex min-w-0 flex-col">
            <span class="truncate font-sans text-[13px] font-medium text-text">{{ m.label }}</span>
            <span class="truncate font-sans text-[11px] text-text-muted">{{ m.description }}</span>
          </span>
        </button>
        <div v-if="filteredModels.length === 0" class="px-2 py-3 text-center text-[12px] text-text-muted">
          {{ t('model.no_match') }}
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import { t } from '../../services/i18n';
import Icon from '../common/Icon.vue';
import { useModel } from '../../composables/useModel';
import { useFloatingPanel } from '../../composables/useFloatingPanel';
import { mockModels } from '../../services/mock-data';

const model = useModel();
const fp = useFloatingPanel();
const isOpen = computed(() => fp.isActive('model'));

const query = ref<string>('');
const searchRef = ref<HTMLInputElement | null>(null);

const currentLabel = computed(
  () => mockModels.find((m) => m.id === model.currentModel.value)?.label ?? '',
);

const filteredModels = computed(() => {
  const q = query.value.trim().toLowerCase();
  if (!q) return mockModels;
  return mockModels.filter((m) =>
    m.label.toLowerCase().includes(q) ||
    m.description.toLowerCase().includes(q) ||
    m.vendor.toLowerCase().includes(q),
  );
});

watch(isOpen, async (open) => {
  if (open) {
    query.value = '';
    await nextTick();
    searchRef.value?.focus();
  }
});

function onSearchInput() {}

function onPick(id: typeof mockModels[number]['id']) {
  model.selectModel(id);
  fp.close();
}
</script>