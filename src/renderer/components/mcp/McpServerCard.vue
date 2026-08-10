<script setup lang="ts">
/**
 * 单 MCP server 卡片：name / description / transport / enabled
 * 开关 / connection / launch 状态 / tools 列表 / 4 按钮（test / retry /
 * edit / delete）。
 *
 * builtin server（bundled filesystem）禁用 delete 按钮；launchStatus=failed
 * 才显示 retry 按钮。
 */
import { computed, onMounted, ref } from 'vue';
import type {
  DarvinMcpResource,
  DarvinMcpPrompt,
  DarvinMcpServer,
  DarvinMcpServerExposedTool,
} from '../../../shared/darvin-api';
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
  retry: [server: DarvinMcpServer];
  edit: [server: DarvinMcpServer];
  delete: [server: DarvinMcpServer];
}>();

const { listResources, listPrompts, getLogs, fetchTools } = useMcpServers();

/**
 * main 端 SQLite 视图不携带 exposedTools（工具面在 Go 注册表），连接态下
 * 兜底拉一次无 toast 的工具列表，让 schema / 安全徽章能展示。
 */
const fallbackTools = ref<DarvinMcpServerExposedTool[] | null>(null);
const displayTools = computed<DarvinMcpServerExposedTool[] | undefined>(() =>
  props.server.exposedTools ?? fallbackTools.value ?? undefined,
);
onMounted(() => {
  const tools = props.server.exposedTools;
  if (!tools || tools.length === 0) {
    if (props.server.connectionStatus === 'connected' || props.server.connectionStatus === 'error') {
      void fetchTools(props.server.id).then((list) => {
        if (list.length > 0) fallbackTools.value = list;
      });
    }
  }
});

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

/** 点工具名展开 schema。同一时间只展开一个。 */
const expandedTool = ref<string | null>(null);
function toggleTool(name: string): void {
  expandedTool.value = expandedTool.value === name ? null : name;
}
function schemaText(tool: DarvinMcpServerExposedTool): string {
  return JSON.stringify(tool.inputSchema, null, 2);
}

/** resources / prompts 懒加载区块。 */
const resourcesOpen = ref(false);
const promptsOpen = ref(false);
const resources = ref<DarvinMcpResource[]>([]);
const prompts = ref<DarvinMcpPrompt[]>([]);
const resourcesLoaded = ref(false);
const promptsLoaded = ref(false);

async function toggleResources(): Promise<void> {
  resourcesOpen.value = !resourcesOpen.value;
  if (resourcesOpen.value && !resourcesLoaded.value) {
    resources.value = await listResources(props.server.id);
    resourcesLoaded.value = true;
  }
}

async function togglePrompts(): Promise<void> {
  promptsOpen.value = !promptsOpen.value;
  if (promptsOpen.value && !promptsLoaded.value) {
    prompts.value = await listPrompts(props.server.id);
    promptsLoaded.value = true;
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

    <div v-if="displayTools && displayTools.length > 0" class="space-y-1 border-t border-border pt-2">
      <div class="font-sans text-[10px] text-text-subtle">
        {{ t('mcp.tools.count', { count: displayTools.length }) }}
      </div>
      <div class="flex flex-wrap gap-1">
        <button
          v-for="tool in displayTools"
          :key="tool.name"
          type="button"
          class="inline-flex items-center gap-1 rounded bg-surface-raised px-1.5 py-0.5 font-mono text-[10px] text-text-muted transition-colors hover:text-text"
          :data-testid="`mcp-tool-${server.id}-${tool.name}`"
          @click="toggleTool(tool.name)"
        >
          {{ tool.name }}
          <span
            v-if="tool.destructiveHint"
            class="rounded bg-danger/10 px-1 font-sans text-[9px] font-medium text-danger"
          >D</span>
          <span
            v-else-if="tool.readOnlyHint"
            class="rounded bg-success/10 px-1 font-sans text-[9px] font-medium text-success"
          >R</span>
        </button>
      </div>
      <pre
        v-if="expandedTool"
        class="max-h-48 overflow-y-auto whitespace-pre-wrap break-words rounded bg-surface-2 px-2 py-1.5 font-mono text-[10px] leading-snug text-text-muted"
        :data-testid="`mcp-tool-schema-${expandedTool}`"
      >{{ schemaText(displayTools.find((x) => x.name === expandedTool)!) }}</pre>
    </div>

    <!-- resources / prompts 懒加载区块 -->
    <div class="space-y-1 border-t border-border pt-2">
      <div class="flex flex-wrap gap-3">
        <button
          type="button"
          class="font-sans text-[10px] text-text-muted transition-colors hover:text-text"
          :data-testid="`mcp-resources-toggle-${server.id}`"
          @click="toggleResources"
        >
          {{ t('mcp.cap.resources', { count: resourcesOpen ? resources.length : '?' }) }}
        </button>
        <button
          type="button"
          class="font-sans text-[10px] text-text-muted transition-colors hover:text-text"
          :data-testid="`mcp-prompts-toggle-${server.id}`"
          @click="togglePrompts"
        >
          {{ t('mcp.cap.prompts', { count: promptsOpen ? prompts.length : '?' }) }}
        </button>
      </div>
      <div v-if="resourcesOpen" class="space-y-0.5" data-testid="mcp-resources-list">
        <div v-if="resources.length === 0" class="font-sans text-[10px] text-text-subtle">
          {{ t('mcp.cap.empty') }}
        </div>
        <div
          v-for="r in resources"
          :key="r.uri"
          class="truncate font-mono text-[10px] text-text-muted"
          :title="r.uri"
        >
          {{ r.name || r.uri }}
        </div>
      </div>
      <div v-if="promptsOpen" class="space-y-0.5" data-testid="mcp-prompts-list">
        <div v-if="prompts.length === 0" class="font-sans text-[10px] text-text-subtle">
          {{ t('mcp.cap.empty') }}
        </div>
        <div v-for="p in prompts" :key="p.name" class="font-mono text-[10px] text-text-muted">
          {{ p.name }}
        </div>
      </div>
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