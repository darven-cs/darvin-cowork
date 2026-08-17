<template>
  <div class="flex h-full min-w-0 flex-col">
    <ChatHeader
      :side-panel-open="sidePanelOpen"
      :title="t('workspace.title')"
      @toggle-sidebar="emit('toggle-sidebar')"
      @toggle-side-panel="emit('toggle-side-panel')"
    />

    <div class="min-h-0 flex-1 overflow-y-auto px-6 animate-fade-in">
      <div class="mx-auto flex h-full w-full max-w-[760px] flex-col justify-center gap-6 py-10">
        <div class="flex flex-col items-center gap-3 text-center">
          <Mascot :size="72" />
          <h1 class="font-display text-xl font-semibold text-text">{{ t('workspace.title') }}</h1>
          <p class="max-w-[420px] text-sm leading-relaxed text-text-muted">
            {{ t('workspace.subtitle') }}
          </p>
        </div>

        <!-- 空态：还没有任何工作区 -->
        <div
          v-if="workspaces.length === 0 && !creating"
          class="flex flex-col items-center gap-4"
        >
          <p class="text-sm text-text-subtle">{{ t('workspace.empty.hint') }}</p>
          <button
            type="button"
            class="inline-flex items-center gap-2 rounded-lg bg-primary px-5 py-2.5 font-sans text-sm font-medium text-white transition-colors hover:bg-primary-hover"
            @click="startCreate"
          >
            <Icon name="folder-plus" :size="16" />
            {{ t('workspace.new.submit') }}
          </button>
        </div>

        <!-- 工作区列表 -->
        <ul v-else class="flex flex-col gap-2">
          <li
            v-for="w in workspaces"
            :key="w.id"
            class="group flex items-center gap-3 rounded-xl border border-border bg-surface px-4 py-3 transition-colors hover:border-border-strong"
          >
            <Icon name="folder" :size="20" class="shrink-0 text-text-muted" />
            <button
              type="button"
              class="min-w-0 flex-1 text-left"
              :title="w.rootPath"
              @click="enter(w.id)"
            >
              <span class="block truncate font-sans text-[15px] font-medium text-text">
                {{ w.label }}
              </span>
              <span class="block truncate font-sans text-xs text-text-subtle">
                {{ formatNumber(w.sessionCount) }} {{ t('workspace.sessionCount') }}
              </span>
            </button>
            <span class="shrink-0 font-sans text-[11px] text-text-subtle">
              {{ formatRelativeTime(w.updatedAt) }}
            </span>
            <div class="flex items-center gap-1 opacity-0 transition-opacity group-hover:opacity-100">
              <button
                type="button"
                class="rounded-md px-2 py-1.5 text-text-subtle transition-colors hover:bg-surface-2 hover:text-text"
                :aria-label="t('workspace.rename.action')"
                :title="t('workspace.rename.action')"
                @click="editing = w"
              >
                <Icon name="edit" :size="14" />
              </button>
              <button
                type="button"
                class="rounded-md px-2 py-1.5 text-text-subtle transition-colors hover:bg-surface-2 hover:text-text"
                :aria-label="t('workspace.edit.move')"
                :title="t('workspace.edit.move')"
                @click="editing = w"
              >
                <Icon name="folder-open" :size="14" />
              </button>
              <button
                type="button"
                class="rounded-md px-2 py-1.5 text-text-subtle transition-colors hover:bg-surface-2 hover:text-danger"
                :aria-label="t('workspace.delete')"
                :title="t('workspace.delete')"
                @click="deleting = w"
              >
                <Icon name="trash" :size="14" />
              </button>
            </div>
            <button
              type="button"
              class="shrink-0 rounded-md bg-primary px-3 py-1.5 font-sans text-xs font-medium text-white transition-colors hover:bg-primary-hover"
              @click="enter(w.id)"
            >
              {{ t('workspace.enter') }}
            </button>
          </li>
        </ul>

        <!-- 新建表单 -->
        <form
          v-if="creating"
          class="flex flex-col gap-3 rounded-xl border border-border bg-surface p-4"
          @submit.prevent="submit"
        >
          <input
            v-model="name"
            class="w-full rounded-md border border-border bg-surface-2 px-3 py-2 font-sans text-sm text-text outline-none placeholder:text-text-subtle focus:border-border-strong"
            :placeholder="t('workspace.new.name')"
          />
          <div class="flex items-center justify-between gap-2">
            <button
              type="button"
              class="inline-flex items-center gap-2 rounded-md border border-border px-3 py-2 font-sans text-xs text-text-muted transition-colors hover:bg-surface-2 hover:text-text"
              @click="pickDir"
            >
              <Icon name="folder-open" :size="14" />
              {{ pickedDir ?? t('workspace.new.pick') }}
            </button>
            <div class="flex items-center gap-2">
              <button
                type="button"
                class="rounded-md px-3 py-2 font-sans text-xs text-text-muted transition-colors hover:bg-surface-2"
                @click="creating = false"
              >
                {{ t('workspace.new.cancel') }}
              </button>
              <button
                type="submit"
                class="rounded-md bg-primary px-4 py-2 font-sans text-xs font-medium text-white transition-colors hover:bg-primary-hover disabled:opacity-50"
                :disabled="busy"
              >
                {{ t('workspace.new.confirm') }}
              </button>
            </div>
          </div>
        </form>

        <div v-if="workspaces.length > 0 && !creating" class="flex justify-center">
          <button
            type="button"
            class="inline-flex items-center gap-2 rounded-lg border border-border px-4 py-2 font-sans text-sm text-text-muted transition-colors hover:bg-surface-2 hover:text-text"
            @click="startCreate"
          >
            <Icon name="plus" :size="14" />
            {{ t('workspace.new.cta') }}
          </button>
        </div>
      </div>
    </div>

    <WorkspaceEditModal v-if="editing" :workspace="editing" @close="editing = null" />
    <WorkspaceDeleteModal v-if="deleting" :workspace="deleting" @close="deleting = null" />
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import type { DarvinWorkspace } from '../../shared/darvin-api';
import ChatHeader from '../components/chat/ChatHeader.vue';
import Mascot from '../components/home/Mascot.vue';
import Icon from '../components/common/Icon.vue';
import WorkspaceEditModal from '../components/workspaces/WorkspaceEditModal.vue';
import WorkspaceDeleteModal from '../components/workspaces/WorkspaceDeleteModal.vue';
import { useWorkspaces } from '../composables/useWorkspaces';
import { useViewMode } from '../composables/useViewMode';
import { t, formatNumber, formatRelativeTime } from '../services/i18n';

defineProps<{ sidePanelOpen: boolean }>();
const emit = defineEmits<{
  'toggle-sidebar': [];
  'toggle-side-panel': [];
  'navigate': [target: string];
}>();

const workspaces = useWorkspaces();
const viewMode = useViewMode();
const creating = ref(false);
const name = ref('');
const pickedDir = ref<string | null>(null);
const busy = ref(false);
const editing = ref<DarvinWorkspace | null>(null);
const deleting = ref<DarvinWorkspace | null>(null);

function startCreate(): void {
  creating.value = true;
  name.value = '';
  pickedDir.value = null;
}

async function enter(id: string): Promise<void> {
  await workspaces.switchWorkspace(id);
  viewMode.goChat();
}

async function submit(): Promise<void> {
  busy.value = true;
  try {
    const w = await workspaces.createWorkspace({
      name: name.value.trim() || undefined,
      rootPath: pickedDir.value ?? undefined,
    });
    await workspaces.switchWorkspace(w.id);
    viewMode.goChat();
  } catch (e) {
    busy.value = false;
  }
}

async function pickDir(): Promise<void> {
  const r = await window.darvin.setWorkspaceRoot();
  if (r.canceled || !r.rootPath) return;
  pickedDir.value = r.rootPath;
}
</script>