<script setup lang="ts">
/**
 * 单 MCP server 的运行时连接状态徽章。
 *
 * 4 个状态：disconnected / connecting / connected / error。disconnected 不
 * 渲染（卡片 toggle off 时让 toggle 自带视觉反馈；徽章冗余）。
 */
import { computed } from 'vue';
import type { DarvinMcpConnectionStatus } from '../../../shared/darvin-api';
import { t } from '../../services/i18n';

const props = defineProps<{
  status?: DarvinMcpConnectionStatus;
  error?: string;
}>();

const status = computed<DarvinMcpConnectionStatus>(() => props.status ?? 'disconnected');
const visible = computed(() => status.value !== 'disconnected');

const statusClass = computed(() => {
  switch (status.value) {
    case 'connected':  return 'bg-success/10 text-success';
    case 'connecting': return 'bg-warning/10 text-warning';
    case 'error':      return 'bg-danger/10 text-danger';
    case 'disconnected':
    default:           return '';
  }
});

const dotClass = computed(() => {
  switch (status.value) {
    case 'connected':  return 'bg-success';
    case 'connecting': return 'bg-warning animate-pulse';
    case 'error':      return 'bg-danger';
    case 'disconnected':
    default:           return 'bg-text-subtle';
  }
});
</script>

<template>
  <span
    v-if="visible"
    :class="['inline-flex items-center gap-1 rounded px-1.5 py-0.5 font-sans text-[10px] font-medium', statusClass]"
    :title="props.error"
    :data-testid="`mcp-connection-${status}`"
  >
    <span :class="['inline-block h-1.5 w-1.5 shrink-0 rounded-full', dotClass]" />
    {{ t(`mcp.connection.${status}`) }}
  </span>
</template>