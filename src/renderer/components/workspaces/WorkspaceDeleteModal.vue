<template>
  <div
    class="fixed inset-0 z-30 flex items-center justify-center bg-text/30 p-4"
    data-testid="workspace-delete-modal"
    @click.self="emit('close')"
  >
    <div class="w-full max-w-md rounded-xl border border-border bg-bg p-5 shadow-lg">
      <h2 class="font-display text-base font-semibold text-text">
        {{ workspace.name || workspace.label }}
      </h2>
      <p class="mt-2 font-mono text-xs text-text-subtle" :title="workspace.rootPath">
        {{ workspace.rootPath }}
      </p>

      <p class="mt-4 font-sans text-sm text-text">
        {{ t('workspace.delete.empty') }}
      </p>
      <p v-if="workspace.sessionCount > 0" class="mt-3 rounded-md border border-danger/40 bg-danger/5 px-3 py-2 font-sans text-xs text-danger">
        {{ t('workspace.delete.cascade', { n: workspace.sessionCount }) }}
      </p>

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
          class="rounded-md px-4 py-2 font-sans text-xs font-medium text-white transition-colors disabled:opacity-50"
          :class="workspace.sessionCount > 0 ? 'bg-danger hover:bg-danger/90' : 'bg-primary hover:bg-primary-hover'"
          :disabled="busy"
          @click="confirm"
        >
          {{
            workspace.sessionCount > 0
              ? t('workspace.delete.confirmForce', { n: workspace.sessionCount })
              : t('workspace.delete.confirm')
          }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import type { DarvinWorkspace } from '../../../shared/darvin-api';
import { useWorkspaces } from '../../composables/useWorkspaces';
import { t } from '../../services/i18n';

const props = defineProps<{ workspace: DarvinWorkspace }>();
const emit = defineEmits<{ close: [] }>();

const workspacesState = useWorkspaces();
const busy = ref(false);

async function confirm(): Promise<void> {
  busy.value = true;
  try {
    await workspacesState.deleteWorkspace(props.workspace.id, {
      force: props.workspace.sessionCount > 0,
    });
    emit('close');
  } catch (e) {
    busy.value = false;
    void e;
  }
}
</script>