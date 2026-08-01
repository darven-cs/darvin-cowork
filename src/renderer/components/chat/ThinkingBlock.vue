<template>
  <div class="mb-2 overflow-hidden rounded-lg border border-border bg-surface-2/50">
    <button
      type="button"
      class="flex w-full items-center gap-2 px-3 py-2 text-left transition-colors hover:bg-surface-raised/50"
      @click="expanded = !expanded"
    >
      <span
        class="h-2 w-2 shrink-0 rounded-full bg-thinking animate-pulse"
        :class="{ 'opacity-40': !isCurrentlyStreaming }"
      />
      <span class="text-xs font-medium text-text-muted">{{ t('chat.thinking') }}</span>
      <Icon
        name="chevron-down"
        :size="14"
        class="ml-auto text-text-subtle transition-transform duration-200"
        :class="expanded ? 'rotate-180' : ''"
      />
    </button>
    <div v-if="expanded" class="max-h-[300px] overflow-y-auto border-t border-border/50 px-3 pb-3 pt-2">
      <div class="whitespace-pre-wrap pt-0.5 text-xs leading-relaxed text-text-muted">{{ content }}</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue';
import { t } from '../../services/i18n';

const props = defineProps<{ content: string; isCurrentlyStreaming: boolean }>();

// 流式时自动展开；结束后保持展开，不强制折叠（用户可手动折叠）。
const expanded = ref(props.isCurrentlyStreaming);
watch(
  () => props.isCurrentlyStreaming,
  (v) => {
    if (v) expanded.value = true;
  },
);
</script>
