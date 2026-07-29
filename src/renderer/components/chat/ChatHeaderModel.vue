<template>
  <Dropdown>
    <template #trigger>
      <button
        type="button"
        class="flex items-center gap-1.5 rounded-md border border-border bg-surface-2 px-2.5 py-1 font-mono text-[11px] uppercase tracking-[0.08em] text-text-muted transition-colors hover:border-border-strong hover:text-text"
      >
        <span>{{ currentLabel }}</span>
        <Icon name="chevron-down" :size="12" />
      </button>
    </template>
    <template #menu>
      <ul class="min-w-[180px] rounded-lg border border-border bg-surface py-1">
        <li
          v-for="opt in options"
          :key="opt.id"
          class="flex h-7 cursor-pointer items-center justify-between px-2.5 font-mono text-[11px] uppercase tracking-[0.06em] text-text-muted hover:bg-accent-soft hover:text-text"
          :class="opt.id === currentModel ? 'text-text' : ''"
          @click="onSelect(opt.id)"
        >
          <span>{{ opt.label }}</span>
          <span
            v-if="opt.id === currentModel"
            class="inline-block h-1.5 w-1.5 rounded-full bg-accent"
          />
        </li>
      </ul>
    </template>
  </Dropdown>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { DarvinModelId } from '../../../shared/darvin-api';
import { useModel } from '../../composables/useModel';
import Dropdown from '../common/Dropdown.vue';

const { currentModel, options, selectModel } = useModel();

const currentLabel = computed(() => {
  const m = options.find((o) => o.id === currentModel.value);
  return m?.label ?? currentModel.value;
});

function onSelect(id: DarvinModelId) {
  selectModel(id);
}
</script>
