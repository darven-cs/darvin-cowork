<script setup lang="ts">
/**
 * MCP 服务器管理视图。
 *
 * 顶部 [+ 新增] → 打开 FormModal（editing=null 走 create）。
 * 卡片 [编辑] → 打开 FormModal（editing=server 走 update）。
 * 卡片 [删除] → confirm() → remove(id)。
 * 卡片 [测试] → testConnection(id)（toast 内部弹）。
 * 卡片 [重试] → retryResolution(id)（仅在 launchStatus=failed 时显示）。
 * toggle → setEnabled(id, enabled)（乐观更新）。
 */
import { ref } from 'vue';
import type {
  DarvinMcpServer,
  DarvinMcpServerCreate,
  DarvinMcpServerPatch,
} from '../../shared/darvin-api';
import ChatHeader from '../components/chat/ChatHeader.vue';
import McpServerCard from '../components/mcp/McpServerCard.vue';
import McpServerDetailModal from '../components/mcp/McpServerDetailModal.vue';
import McpServerFormModal from '../components/mcp/McpServerFormModal.vue';
import { useMcpServers } from '../composables/useMcpServers';
import { t } from '../services/i18n';

defineProps<{ sidePanelOpen: boolean }>();
const emit = defineEmits<{
  'toggle-sidebar': [];
  'toggle-side-panel': [];
}>();

const { servers, loading, setEnabled, create, update, remove, testConnection, retryResolution } =
  useMcpServers();

const modalOpen = ref(false);
const editingServer = ref<DarvinMcpServer | null>(null);
const detailsModalOpen = ref(false);
const detailsServer = ref<DarvinMcpServer | null>(null);

function openCreate(): void {
  editingServer.value = null;
  modalOpen.value = true;
}

function openEdit(server: DarvinMcpServer): void {
  editingServer.value = server;
  modalOpen.value = true;
}

function closeModal(): void {
  modalOpen.value = false;
  editingServer.value = null;
}

function openDetails(server: DarvinMcpServer): void {
  detailsServer.value = server;
  detailsModalOpen.value = true;
}

function closeDetails(): void {
  detailsModalOpen.value = false;
}

async function onSave(payload: DarvinMcpServerCreate | { id: string; patch: DarvinMcpServerPatch }): Promise<void> {
  if ('id' in payload) {
    const updated = await update(payload.id, payload.patch);
    if (updated) closeModal();
  } else {
    const created = await create(payload);
    if (created) closeModal();
  }
}

async function onToggle(id: string, enabled: boolean): Promise<void> {
  try {
    await setEnabled(id, enabled);
  } catch {
    // 失败已 toast + 回滚
  }
}

async function onDelete(server: DarvinMcpServer): Promise<void> {
  if (server.isBuiltIn) return;
  const ok = window.confirm(t('mcp.delete.confirm_message', { name: server.name }));
  if (!ok) return;
  await remove(server.id);
}

async function onTest(server: DarvinMcpServer): Promise<void> {
  await testConnection(server.id);
}

async function onRetry(server: DarvinMcpServer): Promise<void> {
  await retryResolution(server.id);
}
</script>

<template>
  <div class="flex h-full min-w-0 flex-col">
    <ChatHeader
      :side-panel-open="sidePanelOpen"
      @toggle-sidebar="emit('toggle-sidebar')"
      @toggle-side-panel="emit('toggle-side-panel')"
    />

    <div class="border-b border-border px-6 pt-6 pb-4">
      <div class="flex items-center justify-between gap-2">
        <h2 class="font-sans text-[20px] font-semibold text-text">{{ t('mcp.list.title') }}</h2>
        <button
          type="button"
          class="rounded-md bg-primary px-3 py-1.5 font-sans text-xs font-medium text-white transition-opacity hover:opacity-90"
          data-testid="mcp-add"
          @click="openCreate"
        >
          {{ t('mcp.list.add') }}
        </button>
      </div>
    </div>

    <div class="flex-1 overflow-y-auto p-6">
      <div v-if="loading && servers.length === 0" class="py-8 text-center font-sans text-xs text-text-muted">
        {{ t('common.loading') }}
      </div>
      <div v-else-if="servers.length === 0" class="py-8 text-center font-sans text-xs text-text-muted">
        {{ t('mcp.list.empty') }}
      </div>
      <div v-else class="grid grid-cols-1 gap-3 md:grid-cols-2">
        <McpServerCard
          v-for="server in servers"
          :key="server.id"
          :server="server"
          @toggle="onToggle"
          @test="onTest"
          @details="openDetails"
          @retry="onRetry"
          @edit="openEdit"
          @delete="onDelete"
        />
      </div>
    </div>

    <McpServerFormModal
      :open="modalOpen"
      :editing="editingServer"
      @save="onSave"
      @cancel="closeModal"
    />

    <McpServerDetailModal
      :open="detailsModalOpen"
      :server="detailsServer"
      @close="closeDetails"
    />
  </div>
</template>