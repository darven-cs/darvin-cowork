<template>
  <button
    type="button"
    class="flex w-full items-center gap-2 px-3 py-2 text-left transition-colors hover:bg-surface-2"
    @click="$emit('toggle')"
  >
    <span class="h-2 w-2 flex-shrink-0 rounded-full" :class="dotClass" />
    <Icon :name="iconName" :size="14" class="flex-shrink-0 text-text-muted" />
    <span class="min-w-0 flex-1 truncate font-mono text-xs text-text">{{ name }}</span>
    <span v-if="isStreaming && !hasResult && !isError" class="flex-shrink-0 text-[10px] text-text-subtle">
      {{ t('tool.running') }}
    </span>
    <Icon
      name="chevron-down"
      :size="14"
      class="flex-shrink-0 text-text-muted transition-transform duration-150"
      :class="expanded ? 'rotate-180' : ''"
    />
  </button>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { DarvinToolKind } from '../../../../shared/darvin-api';
import { t } from '../../../services/i18n';

const props = defineProps<{
  name: string;
  kind: DarvinToolKind;
  isStreaming: boolean;
  hasResult: boolean;
  isError: boolean;
  expanded: boolean;
}>();

defineEmits<{ toggle: [] }>();

const ICON_BY_KIND: Record<string, string> = {
  bash: 'terminal',
  read: 'file-text',
  write: 'file-text',
  edit: 'edit',
  todowrite: 'list',
  web_search: 'search',
  web_fetch: 'search',
  image_gen: 'bolt',
  video_gen: 'bolt',
};

const iconName = computed(() => ICON_BY_KIND[props.kind] ?? 'circle-dot');

// 状态点 4 色：红（error）/ 绿（成功）/ 蓝脉冲（流中）/ 蓝实心（收尾无结果）
const dotClass = computed(() => {
  if (props.isError) return 'bg-red-500';
  if (props.hasResult) return 'bg-green-500';
  if (props.isStreaming) return 'bg-blue-500 animate-pulse';
  return 'bg-blue-500';
});
</script>
