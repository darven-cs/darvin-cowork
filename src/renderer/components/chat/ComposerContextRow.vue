<template>
  <div class="relative flex items-center justify-between border-t border-border/60 px-3 py-1.5">
    <button
      type="button"
      class="inline-flex max-w-[60%] cursor-pointer items-center gap-1.5 rounded-md px-2 py-0.5 font-sans text-[11px] text-text-subtle transition-colors hover:bg-surface-2 hover:text-text"
      :title="t('workspace.switch')"
      :aria-label="t('workspace.switch')"
      data-testid="composer-workspace"
      @click="open = !open"
    >
      <Icon name="folder" :size="12" />
      <span class="truncate">{{ workspaceLabel }}</span>
      <Icon name="chevron-down" :size="10" />
    </button>
    <div v-if="open" class="fixed inset-0 z-20" @click="open = false" />
    <WorkspacePicker v-if="open" @close="open = false" @manage="goWorkspaces" />
    <span
      class="inline-flex items-center gap-1.5 rounded-md px-2 py-0.5 font-sans text-[11px] text-text-subtle"
      data-testid="composer-agent"
    >
      <Icon name="bolt" :size="12" />
      {{ t('sidebar.agent.main.name') }}
      <Icon name="chevron-down" :size="10" />
    </span>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';
import { useWorkspaces } from '../../composables/useWorkspaces';
import { useViewMode } from '../../composables/useViewMode';
import { t } from '../../services/i18n';
import Icon from '../common/Icon.vue';
import WorkspacePicker from './WorkspacePicker.vue';

const workspaces = useWorkspaces();
const viewMode = useViewMode();
const open = ref(false);

const workspaceLabel = computed(
  () => workspaces.activeWorkspace.value?.label ?? t('workspace.pick'),
);

function goWorkspaces(): void {
  open.value = false;
  viewMode.goWorkspaces();
}

function openPicker(): void {
  open.value = true;
}

defineExpose({ openPicker });
</script>