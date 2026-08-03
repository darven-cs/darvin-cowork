<script setup lang="ts">
/**
 * spec 37 — resolver 优化后的 launch 状态徽章。
 *
 * 5 个状态：pending / installing / ready / failed / unsupported。
 * ready 状态不渲染（运行时已就绪，徽章冗余；card 用 connection 徽章代表运行态）。
 */
import { computed } from 'vue';
import type { DarvinMcpLaunchStatus } from '../../../shared/darvin-api';
import { t } from '../../services/i18n';

const props = defineProps<{
  status: DarvinMcpLaunchStatus;
  error?: string;
}>();

const visible = computed(() => props.status !== 'ready');

const statusClass = computed(() => {
  switch (props.status) {
    case 'pending':     return 'bg-surface-raised text-text-subtle';
    case 'installing':  return 'bg-warning/10 text-warning';
    case 'failed':      return 'bg-danger/10 text-danger';
    case 'unsupported': return 'bg-surface-raised text-text-subtle';
    case 'ready':
    default:            return '';
  }
});
</script>

<template>
  <span
    v-if="visible"
    :class="['inline-flex items-center rounded px-1.5 py-0.5 font-sans text-[10px] font-medium', statusClass]"
    :title="props.error"
    :data-testid="`mcp-launch-${status}`"
  >
    {{ t(`mcp.launch.${status}`) }}
  </span>
</template>