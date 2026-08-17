<template>
  <div
    class="fixed inset-0 z-30 flex items-center justify-center bg-text/30 p-4"
    data-testid="workspace-edit-modal"
    @click.self="emit('close')"
  >
    <div class="w-full max-w-md rounded-xl border border-border bg-bg p-5 shadow-lg">
      <h2 class="font-display text-base font-semibold text-text">{{ t('workspace.edit.title') }}</h2>

      <div class="mt-4 flex flex-col gap-3">
        <label class="flex flex-col gap-1">
          <span class="font-sans text-xs text-text-muted">{{ t('workspace.edit.name') }}</span>
          <input
            ref="nameInput"
            v-model.trim="name"
            class="w-full rounded-md border border-border bg-surface-2 px-3 py-2 font-sans text-sm text-text outline-none placeholder:text-text-subtle focus:border-border-strong"
            :placeholder="t('workspace.edit.name')"
            @keydown.enter.prevent="save"
            @keydown.escape="emit('close')"
          />
        </label>

        <label class="flex flex-col gap-1">
          <span class="font-sans text-xs text-text-muted">{{ t('workspace.edit.rootPath') }}</span>
          <div class="flex items-center gap-2">
            <button
              type="button"
              class="inline-flex min-w-0 flex-1 items-center gap-2 rounded-md border border-border bg-surface-2 px-3 py-2 text-left font-sans text-xs text-text-muted transition-colors hover:border-border-strong hover:text-text"
              :title="rootPath"
              @click="pickDir"
            >
              <Icon name="folder-open" :size="13" class="shrink-0" />
              <span class="truncate">{{ rootPath }}</span>
            </button>
            <button
              v-if="rootPath !== originalRootPath"
              type="button"
              class="font-sans text-xs text-text-muted hover:text-text"
              @click="rootPath = originalRootPath"
            >
              ↺
            </button>
          </div>
        </label>

        <p v-if="error" class="font-sans text-xs text-danger" role="alert">{{ error }}</p>
      </div>

      <div class="mt-5 flex items-center justify-end gap-2">
        <button
          type="button"
          class="rounded-md px-3 py-2 font-sans text-xs text-text-muted transition-colors hover:bg-surface-2"
          @click="emit('close')"
        >
          {{ t('workspace.edit.cancel') }}
        </button>
        <button
          type="button"
          class="rounded-md bg-primary px-4 py-2 font-sans text-xs font-medium text-white transition-colors hover:bg-primary-hover disabled:opacity-50"
          :disabled="busy || !dirty"
          @click="save"
        >
          {{ t('workspace.edit.save') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue';
import type { DarvinWorkspace } from '../../../shared/darvin-api';
import { useWorkspaces } from '../../composables/useWorkspaces';
import Icon from '../common/Icon.vue';
import { t } from '../../services/i18n';

const props = defineProps<{ workspace: DarvinWorkspace }>();
const emit = defineEmits<{ close: [] }>();

const workspacesState = useWorkspaces();
const name = ref(props.workspace.name);
const rootPath = ref(props.workspace.rootPath);
const originalRootPath = ref(props.workspace.rootPath);
const error = ref<string | null>(null);
const busy = ref(false);
const nameInput = ref<HTMLInputElement | null>(null);

const dirty = computed(() => name.value !== props.workspace.name || rootPath.value !== originalRootPath.value);

async function pickDir(): Promise<void> {
  const r = await window.darvin.setWorkspaceRoot();
  if (r.canceled || !r.rootPath) return;
  // 选择目录可能命中其它 workspace 的根 → 提示并拒绝应用（保持唯一性约束）。
  const owner = workspacesState.workspaces.value.find(
    (w) => w.id !== props.workspace.id && w.rootPath === r.rootPath,
  );
  if (owner) {
    error.value = t('workspace.errors.conflict');
    return;
  }
  rootPath.value = r.rootPath;
  error.value = null;
}

async function save(): Promise<void> {
  error.value = null;
  const trimmed = name.value.trim();
  if (!trimmed) {
    error.value = t('workspace.edit.empty');
    return;
  }
  const dup = workspacesState.workspaces.value.find(
    (w) => w.id !== props.workspace.id && w.name.trim() === trimmed,
  );
  if (dup) {
    error.value = t('workspace.edit.duplicate');
    return;
  }
  busy.value = true;
  try {
    const nameChanged = trimmed !== props.workspace.name;
    const rootChanged = rootPath.value !== originalRootPath.value;
    if (nameChanged) {
      await workspacesState.renameWorkspace(props.workspace.id, trimmed);
    }
    if (rootChanged) {
      await workspacesState.updateWorkspaceRoot(props.workspace.id, rootPath.value);
    }
    emit('close');
  } catch (e) {
    error.value = (e as Error).message || 'Save';
  } finally {
    busy.value = false;
  }
}

onMounted(() => {
  void nextTick(() => nameInput.value?.focus());
});
</script>