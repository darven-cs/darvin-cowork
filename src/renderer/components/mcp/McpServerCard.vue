<script setup lang="ts">
/**
 * 单 MCP server 卡片：name / description / transport / enabled
 * 开关 / connection / launch 状态 / 日志抽屉 / 5 按钮（test / details /
 * retry / edit / delete）。工具 / 资源 / 提示详情在「详情」弹窗里展示。
 *
 * builtin server（bundled filesystem）禁用 delete 按钮；launchStatus=failed
 * 才显示 retry 按钮。
 */
import { ref } from 'vue';
import type { DarvinMcpServer } from '../../../shared/darvin-api';
import McpConnectionStatus from './McpConnectionStatus.vue';
import McpLaunchStatus from './McpLaunchStatus.vue';
import Icon from '../common/Icon.vue';
import { t } from '../../services/i18n';
import { useMcpServers } from '../../composables/useMcpServers';

const props = defineProps<{
  server: DarvinMcpServer;
}>();

const emit = defineEmits<{
  toggle: [id: string, enabled: boolean];
  test: [server: DarvinMcpServer];
  details: [server: DarvinMcpServer];
  retry: [server: DarvinMcpServer];
  edit: [server: DarvinMcpServer];
  delete: [server: DarvinMcpServer];
}>();

const { getLogs } = useMcpServers();

/** 运行时日志抽屉（stdio stderr tail）。 */
const logsOpen = ref(false);
const logs = ref<string[]>([]);
const logsLoaded = ref(false);

async function toggleLogs(): Promise<void> {
  logsOpen.value = !logsOpen.value;
  if (logsOpen.value && !logsLoaded.value) {
    logs.value = await getLogs(props.server.id);
    logsLoaded.value = true;
  }
}

function onToggle(e: Event): void {
  const target = e.target as HTMLInputElement;
  emit('toggle', props.server.id, target.checked);
}

const transportLine = (): string => {
  if (props.server.transportType === 'stdio') {
    const cmd = props.server.command ?? '';
    const args = (props.server.args ?? []).join(' ');
    return args ? `${cmd} ${args}` : cmd;
  }
  return props.server.url ?? '';
};

const showRetry = (): boolean => props.server.launchStatus === 'failed';

/** 失败详情可见：launch failed / unsupported 或 connection error 时展示。 */
const hasFailure = (): boolean =>
  props.server.launchStatus === 'failed' ||
  props.server.launchStatus === 'unsupported' ||
  props.server.connectionStatus === 'error';

const failureError = (): string =>
  props.server.launchError || props.server.connectionError || '';

const failureElapsed = (): string => {
  const ms = props.server.launchElapsedMs;
  if (ms === undefined || ms <= 0) return '';
  return ms >= 1000 ? `${(ms / 1000).toFixed(1)}s` : `${ms}ms`;
};
</script>

<template>
  <div
    class="flex flex-col gap-3 rounded-lg border border-border bg-surface p-4"
    :data-testid="`mcp-card-${server.id}`"
  >
    <div class="flex items-start justify-between gap-2">
      <div class="flex min-w-0 flex-1 flex-wrap items-center gap-2">
        <Icon name="link" :size="16" class="shrink-0 text-text-muted" />
        <h3 class="truncate font-sans text-sm font-medium text-text">{{ server.name }}</h3>
        <span
          v-if="server.isBuiltIn"
          class="shrink-0 rounded bg-primary-muted px-1.5 py-0.5 font-sans text-[10px] font-medium text-primary"
        >
          {{ t('mcp.badge.builtin') }}
        </span>
        <McpConnectionStatus :status="server.connectionStatus" :error="server.connectionError" />
        <McpLaunchStatus
          v-if="server.launchStatus && server.launchStatus !== 'ready'"
          :status="server.launchStatus"
          :error="server.launchError"
        />
      </div>
      <label class="relative inline-flex shrink-0 cursor-pointer items-center">
        <input
          type="checkbox"
          class="peer sr-only"
          :checked="server.enabled"
          :data-testid="`mcp-toggle-${server.id}`"
          @change="onToggle"
        />
        <span
          class="h-5 w-9 rounded-full bg-border transition-colors peer-checked:bg-primary peer-focus:outline-none peer-focus:ring-2 peer-focus:ring-primary/30 after:absolute after:left-0.5 after:top-0.5 after:h-4 after:w-4 after:rounded-full after:bg-white after:transition-transform peer-checked:after:translate-x-4"
        />
      </label>
    </div>

    <p class="line-clamp-2 font-sans text-xs text-text-muted">
      {{ server.description || '—' }}
    </p>

    <div class="flex flex-wrap items-center gap-2 font-mono text-[11px] text-text-subtle">
      <span class="rounded bg-surface-2 px-1.5 py-0.5">{{ t(`mcp.transport.${server.transportType}`) }}</span>
      <span class="truncate" :title="transportLine()">{{ transportLine() }}</span>
    </div>

    <!-- 运行时日志抽屉 -->
    <div class="border-t border-border pt-2">
      <button
        type="button"
        class="font-sans text-[10px] text-text-muted transition-colors hover:text-text"
        :data-testid="`mcp-logs-toggle-${server.id}`"
        @click="toggleLogs"
      >
        {{ t('mcp.logs.title') }}
      </button>
      <pre
        v-if="logsOpen"
        class="mt-1 max-h-40 overflow-y-auto whitespace-pre-wrap break-words rounded bg-surface-2 px-2 py-1.5 font-mono text-[10px] leading-snug text-text-muted"
        data-testid="mcp-logs-content"
      >{{ logs.length ? logs.join('\n') : t('mcp.logs.empty') }}</pre>
    </div>

    <div v-if="hasFailure()" class="space-y-1 border-t border-border pt-2" data-testid="mcp-failure-detail">
      <div class="font-sans text-[10px] font-medium text-danger">{{ t('mcp.failure.title') }}</div>
      <p v-if="failureError()" class="break-words font-mono text-[10px] leading-snug text-danger/90">
        {{ failureError() }}
      </p>
      <div v-if="server.launchStage || failureElapsed()" class="flex flex-wrap gap-2 font-sans text-[10px] text-text-muted">
        <span v-if="server.launchStage" data-testid="mcp-failure-stage">
          {{ t('mcp.failure.stage') }}: {{ server.launchStage }}
        </span>
        <span v-if="failureElapsed()" data-testid="mcp-failure-elapsed">
          {{ t('mcp.failure.elapsed') }}: {{ failureElapsed() }}
        </span>
      </div>
      <pre
        v-if="server.launchStderr"
        class="max-h-20 overflow-y-auto whitespace-pre-wrap break-words rounded bg-surface-2 px-2 py-1 font-mono text-[10px] leading-snug text-text-muted"
        data-testid="mcp-failure-stderr"
      >{{ server.launchStderr }}</pre>
    </div>

    <div class="flex flex-wrap items-center justify-end gap-3 pt-1">
      <button
        type="button"
        class="font-sans text-xs text-text-muted transition-colors hover:text-text disabled:opacity-40"
        :disabled="!server.enabled"
        :data-testid="`mcp-test-${server.id}`"
        @click="emit('test', server)"
      >
        {{ t('mcp.action.test') }}
      </button>
      <button
        type="button"
        class="font-sans text-xs text-text-muted transition-colors hover:text-text"
        :data-testid="`mcp-details-${server.id}`"
        @click="emit('details', server)"
      >
        {{ t('mcp.action.details') }}
      </button>
      <button
        v-if="showRetry()"
        type="button"
        class="font-sans text-xs text-warning transition-colors hover:text-warning disabled:opacity-40"
        :data-testid="`mcp-retry-${server.id}`"
        @click="emit('retry', server)"
      >
        {{ t('mcp.action.retry') }}
      </button>
      <button
        type="button"
        class="font-sans text-xs text-text-muted transition-colors hover:text-text"
        :data-testid="`mcp-edit-${server.id}`"
        @click="emit('edit', server)"
      >
        {{ t('mcp.action.edit') }}
      </button>
      <button
        type="button"
        class="font-sans text-xs text-danger transition-colors hover:text-danger disabled:opacity-40"
        :disabled="server.isBuiltIn"
        :data-testid="`mcp-delete-${server.id}`"
        @click="emit('delete', server)"
      >
        {{ t('mcp.action.delete') }}
      </button>
    </div>
  </div>
</template>