<template>
  <button
    v-if="shouldRender"
    type="button"
    class="context-usage-indicator group relative inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-lg transition-colors"
    :class="isClickable ? 'cursor-pointer hover:bg-surface-raised' : 'cursor-default'"
    :disabled="!isClickable"
    :title="tooltipText"
    :aria-label="tooltipText"
    @click="emit('compact', props.sessionId)"
  >
    <svg
      viewBox="0 0 20 20"
      class="h-6 w-6"
      :class="isCompacting ? 'animate-spin' : ''"
      shape-rendering="geometricPrecision"
      aria-hidden="true"
    >
      <circle
        cx="10" cy="10" :r="RADIUS" fill="none" stroke="currentColor"
        stroke-width="2" opacity="0.2" class="text-text-subtle"
      />
      <circle
        cx="10" cy="10" :r="RADIUS" fill="none" stroke="currentColor"
        stroke-width="2" opacity="1" stroke-linecap="round"
        :stroke-dasharray="CIRCUMFERENCE" :stroke-dashoffset="dashOffset"
        transform="rotate(-90 10 10)" :class="statusClass"
      />
    </svg>
    <span
      v-if="percentLabel"
      class="pointer-events-none absolute text-[9px] font-semibold leading-none"
      :class="statusClass"
    >
      {{ percentLabel }}
    </span>
    <span
      class="pointer-events-none absolute bottom-full left-1/2 z-50 mb-2 hidden min-w-max -translate-x-1/2 whitespace-nowrap rounded-lg border border-border bg-surface px-2.5 py-1.5 text-left text-[11px] leading-5 text-text shadow-lg group-hover:block"
    >
      <span v-for="(line, i) in tooltipLines" :key="i">
        {{ line }}<br v-if="i < tooltipLines.length - 1" />
      </span>
    </span>
  </button>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { DarvinContextUsage } from '../../../shared/darvin-api';
import { useMessages } from '../../composables/useMessages';
import { t } from '../../services/i18n';
import { deriveContextStatus, formatTokenCount } from '../../services/tokenFormat';

/**
 * Chat header 圆环：session 上下文占比可视化（spec 03）。
 * - 数据源 useMessages.contextUsageBySessionId[sessionId]（Go `context_usage` 事件）
 * - 5 态颜色 unknown / normal / warning / danger / compacting
 * - tooltip 显示百分比 + 数字 + 上下文窗口；compacting 时持续旋转
 * - 点击事件仅占位：手动压缩 IPC 由 04 spec 落地，本组件只 emit('compact')
 */
const props = defineProps<{ sessionId?: string | null }>();
const emit = defineEmits<{ compact: [sessionId?: string | null] }>();

const RADIUS = 7;
const CIRCUMFERENCE = 2 * Math.PI * RADIUS;

const messages = useMessages();

const usage = computed<DarvinContextUsage | undefined>(() => {
  const sid = props.sessionId;
  if (!sid) return undefined;
  return messages.contextUsageBySessionId.value[sid];
});

const status = computed(() => deriveContextStatus(usage.value?.status, usage.value?.percent));

const isCompacting = computed(() => status.value === 'compacting');

const percent = computed(() => usage.value?.percent);

/** 无任何用量数据时整颗不渲染（与 LobsterAI 一致）；compacting 单独兜底。 */
const shouldRender = computed(() => isCompacting.value || typeof percent.value === 'number');

const dashOffset = computed(() => {
  const p = percent.value;
  if (typeof p !== 'number') return CIRCUMFERENCE;
  const clamped = Math.min(Math.max(p, 0), 100);
  return CIRCUMFERENCE * (1 - clamped / 100);
});

const statusClass = computed(() => {
  switch (status.value) {
    case 'compacting': return 'text-accent';
    case 'warning': return 'text-warning';
    case 'danger': return 'text-danger';
    case 'unknown': return 'text-text-subtle';
    default: return 'text-text-muted';
  }
});

// 压缩中 / 无 percent 时不可点（本 spec 只占位，04 才真正连 IPC）
const isClickable = computed(() => !isCompacting.value && typeof percent.value === 'number');

const tooltipLines = computed<string[]>(() => {
  if (isCompacting.value) return [t('context.usage.compacting')];
  const p = percent.value;
  if (typeof p !== 'number') return [t('context.usage.unknown')];
  const lines: string[] = [t('context.usage.percent').replace('{percent}', String(Math.round(p)))];
  const used = usage.value?.usedTokens;
  const total = usage.value?.contextTokens;
  if (typeof used === 'number' && typeof total === 'number') {
    lines.push(
      t('context.usage.tokens')
        .replace('{used}', formatTokenCount(used))
        .replace('{total}', formatTokenCount(total)),
    );
  }
  if (isClickable.value && (status.value === 'warning' || status.value === 'danger')) {
    lines.push(t('context.usage.compact_hint'));
  }
  return lines;
});

const tooltipText = computed(() => tooltipLines.value.join('\n'));

const percentLabel = computed(() =>
  typeof percent.value === 'number' ? String(Math.round(percent.value)) : '',
);
</script>
