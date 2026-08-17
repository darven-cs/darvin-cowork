<template>
  <section class="flex flex-col gap-4" data-testid="settings-agents">
    <div class="flex items-center justify-between">
      <div class="flex flex-col gap-0.5">
        <h3 class="font-sans text-[15px] font-semibold text-text">{{ t('settings.agents.title') }}</h3>
        <p class="font-sans text-[12.5px] text-text-muted">{{ t('settings.agents.desc') }}</p>
      </div>
      <button
        type="button"
        class="rounded-md bg-primary px-3 py-1.5 font-sans text-[12.5px] font-medium text-white hover:bg-primary-hover"
        @click="openCreate"
      >
        {{ t('settings.agents.create') }}
      </button>
    </div>

    <div class="flex flex-col gap-2">
      <div
        v-for="a in sortedAgents"
        :key="a.id"
        class="flex items-center justify-between gap-3 rounded-md border border-border bg-surface px-3 py-2.5"
      >
        <div class="flex min-w-0 flex-col gap-0.5">
          <div class="flex items-center gap-2">
            <span class="truncate font-sans text-[13px] font-medium text-text">{{ displayName(a) }}</span>
            <span
              v-if="a.source === 'preset'"
              class="rounded-sm bg-surface-2 px-1.5 py-0.5 font-sans text-[10.5px] text-text-muted"
            >{{ t('settings.agents.preset_badge') }}</span>
            <span
              v-else
              class="rounded-sm bg-surface-2 px-1.5 py-0.5 font-sans text-[10.5px] text-text-muted"
            >{{ t('settings.agents.user_badge') }}</span>
            <span
              v-if="a.isDefault"
              class="rounded-sm bg-primary-soft px-1.5 py-0.5 font-sans text-[10.5px] text-primary"
            >★</span>
          </div>
          <span class="truncate font-sans text-[11.5px] text-text-muted">{{ displayDesc(a) }}</span>
        </div>
        <div class="flex shrink-0 items-center gap-2">
          <button
            type="button"
            class="rounded-md border border-border bg-surface-raised px-2.5 py-1 font-sans text-[12px] text-text hover:bg-surface-hover"
            @click="openEdit(a.id)"
          >
            {{ t('settings.agents.edit') }}
          </button>
          <button
            v-if="a.source === 'preset'"
            type="button"
            class="rounded-md border border-border bg-surface-raised px-2.5 py-1 font-sans text-[12px] text-text hover:bg-surface-hover"
            @click="copyFromPreset(a.id)"
          >
            {{ t('settings.agents.copy_from_preset') }}
          </button>
          <button
            v-if="a.source !== 'preset' && !a.isDefault"
            type="button"
            class="rounded-md border border-danger/40 bg-surface-raised px-2.5 py-1 font-sans text-[12px] text-danger hover:bg-surface-hover"
            @click="onDelete(a.id)"
          >
            {{ t('settings.agents.delete') }}
          </button>
        </div>
      </div>
    </div>

    <AgentCreateModal
      v-if="createOpen"
      :workspace-id="workspaceId"
      @close="createOpen = false"
      @created="onCreated"
    />
    <AgentSettingsPanel
      v-if="editingId"
      :agent-id="editingId"
      :workspace-id="workspaceId"
      @close="editingId = ''"
    />
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import AgentCreateModal from '../agent/AgentCreateModal.vue';
import AgentSettingsPanel from '../agent/AgentSettingsPanel.vue';
import { useAgents } from '../../composables/useAgents';
import { useWorkspaces } from '../../composables/useWorkspaces';
import { useToasts } from '../../services/toast';
import { t, getLang } from '../../services/i18n';
import type { DarvinAgent } from '../../../shared/darvin-api';

const workspaces = useWorkspaces();
const agentsApi = useAgents();
const { showToast } = useToasts();

const workspaceId = computed(() => workspaces.activeWorkspaceId.value ?? '');

const createOpen = ref(false);
const editingId = ref<string>('');

watch(
  () => workspaces.activeWorkspaceId.value,
  async (id) => { if (id) await agentsApi.listAgents(id); },
  { immediate: true },
);

const sortedAgents = computed<DarvinAgent[]>(() =>
  [...agentsApi.agents.value].sort((a, b) => {
    if (a.isDefault !== b.isDefault) return a.isDefault ? -1 : 1;
    if (a.source !== b.source) return a.source === 'preset' ? -1 : 1;
    return a.sortOrder - b.sortOrder;
  }),
);

function displayName(a: DarvinAgent): string {
  return getLang() === 'en' ? (a.nameEn || a.name) : a.name;
}
function displayDesc(a: DarvinAgent): string {
  return getLang() === 'en' ? (a.descriptionEn || a.description) : a.description;
}

function openCreate(): void {
  if (!workspaceId.value) return;
  createOpen.value = true;
}
function openEdit(id: string): void {
  editingId.value = id;
}
function copyFromPreset(id: string): void {
  if (!workspaceId.value) return;
  void agentsApi
    .createAgent({ workspaceId: workspaceId.value, name: '', fromPresetId: id })
    .then((created) => {
      editingId.value = created.id;
      showToast(t('settings.agents.created'));
    })
    .catch((e: unknown) => {
      showToast(t('settings.agents.create_failed') + ': ' + (e as Error).message, 'error');
    });
}
function onDelete(id: string): void {
  if (!window.confirm(t('settings.agents.confirm_delete'))) return;
  void agentsApi
    .deleteAgent(id)
    .then(() => showToast(t('settings.agents.deleted')))
    .catch((e: unknown) => {
      showToast(t('settings.agents.delete_failed') + ': ' + (e as Error).message, 'error');
    });
}
function onCreated(): void {
  createOpen.value = false;
  showToast(t('settings.agents.created'));
}
</script>