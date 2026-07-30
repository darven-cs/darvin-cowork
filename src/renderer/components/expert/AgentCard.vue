<template>
  <article class="agent-card flex flex-col rounded-xl border border-border bg-surface p-4 transition-all hover:border-border-strong hover:shadow-md" :data-color="agent.color">
    <div class="mb-3 flex items-start gap-3">
      <span class="agent-avatar flex h-12 w-12 shrink-0 items-center justify-center rounded-xl text-white">
        <Icon :name="agent.icon" :size="20" />
      </span>
      <div class="min-w-0 flex-1">
        <h3 class="truncate font-sans text-[15px] font-semibold text-text">{{ agent.name }}</h3>
        <span class="mt-1 inline-block rounded bg-primary-muted px-2 py-0.5 font-sans text-[10.5px] text-primary">
          {{ categoryLabel }}
        </span>
      </div>
    </div>

    <p class="mb-4 line-clamp-2 font-sans text-[12.5px] leading-[1.55] text-text-muted">
      {{ agent.description }}
    </p>

    <div class="mt-auto flex items-center justify-between">
      <span class="font-sans text-[12.5px] text-text-muted">{{ agent.price }}</span>
      <div class="flex items-center gap-1.5">
        <button
          type="button"
          class="rounded-md border border-border px-3 py-1.5 font-sans text-[12.5px] text-text-muted transition-colors hover:bg-surface-hover"
          @click="emit('details', agent)"
        >
          {{ t('expert.details') }}
        </button>
        <button
          type="button"
          class="rounded-md bg-primary px-3 py-1.5 font-sans text-[12.5px] font-medium text-white transition-colors hover:bg-primary-hover"
          @click="emit('use', agent)"
        >
          {{ t('expert.use') }}
        </button>
      </div>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import Icon from '../common/Icon.vue';
import { t } from '../../services/i18n';
import type { ExpertAgent, ExpertCategory } from '../../services/mock-data';

const props = defineProps<{ agent: ExpertAgent }>();
const emit = defineEmits<{ use: [agent: ExpertAgent]; details: [agent: ExpertAgent] }>();

const CATEGORY_LABELS: Record<ExpertCategory, string> = {
  creative: '创意',
  productivity: '效率',
  technical: '技术',
  business: '商业',
};

const categoryLabel = computed(() => CATEGORY_LABELS[props.agent.category]);
</script>