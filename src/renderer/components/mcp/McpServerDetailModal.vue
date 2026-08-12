<script setup lang="ts">
/**
 * MCP server 详情弹窗：展示该服务器的工具（含 R/D 安全徽章 + schema
 * 展开）、资源、提示三类能力。工具优先用 server.exposedTools，缺失时在
 * 连接态兜底拉一次；资源 / 提示打开时懒加载。Esc / 遮罩 / × 关闭。
 */
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import type {
  DarvinMcpPrompt,
  DarvinMcpResource,
  DarvinMcpServer,
  DarvinMcpServerExposedTool,
} from '../../../shared/darvin-api';
import Icon from '../common/Icon.vue';
import { t } from '../../services/i18n';
import { useMcpServers } from '../../composables/useMcpServers';

const props = defineProps<{
  open: boolean;
  server: DarvinMcpServer | null;
}>();

const emit = defineEmits<{
  close: [];
}>();

const { listResources, listPrompts, fetchTools } = useMcpServers();

const fallbackTools = ref<DarvinMcpServerExposedTool[] | null>(null);
const tools = computed<DarvinMcpServerExposedTool[]>(() => {
  if (!props.server) return [];
  return props.server.exposedTools ?? fallbackTools.value ?? [];
});

const resources = ref<DarvinMcpResource[]>([]);
const prompts = ref<DarvinMcpPrompt[]>([]);
const loaded = ref(false);
const currentId = ref<string | null>(null);

const expandedTool = ref<string | null>(null);
function toggleTool(name: string): void {
  expandedTool.value = expandedTool.value === name ? null : name;
}
function schemaText(tool: DarvinMcpServerExposedTool): string {
  return JSON.stringify(tool.inputSchema, null, 2);
}

const transportLine = computed<string>(() => {
  const srv = props.server;
  if (!srv) return '';
  if (srv.transportType === 'stdio') {
    const cmd = srv.command ?? '';
    const args = (srv.args ?? []).join(' ');
    return args ? `${cmd} ${args}` : cmd;
  }
  return srv.url ?? '';
});

watch(
  () => [props.open, props.server?.id] as const,
  ([isOpen, id]) => {
    const srv = props.server;
    if (!isOpen || !srv) return;
    if (id !== currentId.value) {
      currentId.value = id ?? null;
      loaded.value = false;
      resources.value = [];
      prompts.value = [];
      fallbackTools.value = null;
      expandedTool.value = null;
    }
    if (loaded.value) return;
    loaded.value = true;
    // 工具缺失时兜底拉一次。不依赖 renderer 的 connectionStatus（可能
    // 过期），testMcpConnection RPC 自行判断连接态并返回 ok:false。
    if (!srv.exposedTools || srv.exposedTools.length === 0) {
      void fetchTools(srv.id).then((list) => {
        if (list.length > 0) fallbackTools.value = list;
      });
    }
    void listResources(srv.id).then((r) => {
      resources.value = r;
    });
    void listPrompts(srv.id).then((p) => {
      prompts.value = p;
    });
  },
);

function onKeydown(e: KeyboardEvent): void {
  if (e.key === 'Escape') emit('close');
}
watch(
  () => props.open,
  (isOpen) => {
    if (isOpen) window.addEventListener('keydown', onKeydown);
    else window.removeEventListener('keydown', onKeydown);
  },
);
onBeforeUnmount(() => window.removeEventListener('keydown', onKeydown));
</script>

<template>
  <Teleport to="body">
    <div
      v-if="open && server"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      data-testid="mcp-detail-modal"
      @click.self="emit('close')"
    >
      <div class="flex max-h-[80vh] w-full max-w-lg flex-col rounded-xl border border-border bg-surface shadow-lg">
        <div class="flex shrink-0 items-center justify-between gap-2 border-b border-border p-4">
          <div class="min-w-0">
            <h3 class="font-sans text-sm font-semibold text-text">{{ t('mcp.detail.title') }}</h3>
            <p class="truncate font-mono text-[10px] text-text-subtle">
              {{ server.name }} · {{ t(`mcp.transport.${server.transportType}`) }} · {{ transportLine }}
            </p>
          </div>
          <button
            type="button"
            class="shrink-0 rounded-md p-1 text-text-muted transition-colors hover:bg-surface-2 hover:text-text"
            data-testid="mcp-detail-close"
            @click="emit('close')"
          >
            <Icon name="x" :size="14" />
          </button>
        </div>

        <div class="min-h-0 flex-1 space-y-4 overflow-y-auto p-4">
          <section class="space-y-1">
            <h4 class="font-sans text-xs font-medium text-text-muted">{{ t('mcp.detail.tools') }} ({{ tools.length }})</h4>
            <div v-if="tools.length === 0" class="font-sans text-[10px] text-text-subtle">
              {{ t('mcp.cap.empty') }}
            </div>
            <div v-else class="flex flex-wrap gap-1">
              <button
                v-for="tool in tools"
                :key="tool.name"
                type="button"
                class="inline-flex items-center gap-1 rounded bg-surface-raised px-1.5 py-0.5 font-mono text-[10px] text-text-muted transition-colors hover:text-text"
                :data-testid="`mcp-detail-tool-${tool.name}`"
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
              :data-testid="`mcp-detail-tool-schema-${expandedTool}`"
            >{{ schemaText(tools.find((x) => x.name === expandedTool)!) }}</pre>
          </section>

          <section class="space-y-1">
            <h4 class="font-sans text-xs font-medium text-text-muted">{{ t('mcp.detail.resources') }} ({{ resources.length }})</h4>
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
          </section>

          <section class="space-y-1">
            <h4 class="font-sans text-xs font-medium text-text-muted">{{ t('mcp.detail.prompts') }} ({{ prompts.length }})</h4>
            <div v-if="prompts.length === 0" class="font-sans text-[10px] text-text-subtle">
              {{ t('mcp.cap.empty') }}
            </div>
            <div v-for="p in prompts" :key="p.name" class="font-mono text-[10px] text-text-muted">
              {{ p.name }}
            </div>
          </section>
        </div>
      </div>
    </div>
  </Teleport>
</template>
