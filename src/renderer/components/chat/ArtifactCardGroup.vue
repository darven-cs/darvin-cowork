<template>
  <div
    class="mt-2 divide-y overflow-hidden rounded-lg border border-border bg-surface"
    data-testid="artifact-card-group"
  >
    <button
      v-for="a in visibleArtifacts"
      :key="a.id"
      type="button"
      class="group flex w-full items-center gap-2.5 px-2.5 py-1.5 text-left transition-colors hover:bg-surface-2"
      :data-testid="'artifact-card-' + a.id"
      @click="open(a)"
    >
      <span class="flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-surface-2 text-text-muted">
        <Icon :name="iconForKind(a.kind)" :size="16" />
      </span>
      <span class="min-w-0 flex-1">
        <span class="block truncate text-sm text-text">{{ a.name ?? a.id }}</span>
        <span class="block truncate text-xs text-text-subtle">{{ subtitle(a) }}</span>
      </span>
      <span
        class="shrink-0 rounded border border-border px-1.5 py-0.5 text-[10px] uppercase text-text-subtle transition-colors group-hover:border-primary group-hover:text-primary"
      >
        {{ t('artifact.chat.open') }}
      </span>
    </button>

    <button
      v-if="artifacts.length > 3"
      type="button"
      class="flex w-full items-center justify-center gap-1 px-2.5 py-1.5 text-xs text-text-muted transition-colors hover:bg-surface-2 hover:text-text"
      data-testid="artifact-card-toggle"
      @click="expanded = !expanded"
    >
      {{ expanded ? t('artifact.chat.collapse') : t('artifact.chat.showAll', { count: artifacts.length }) }}
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';
import type { Artifact } from '../../composables/useArtifacts';
import { useArtifacts } from '../../composables/useArtifacts';
import { t } from '../../services/i18n';

const props = defineProps<{ sessionId: string; artifacts: Artifact[] }>();

const artifactsStore = useArtifacts();
const expanded = ref(false);

const visibleArtifacts = computed(() =>
  expanded.value ? props.artifacts : props.artifacts.slice(0, 3),
);

function subtitle(a: Artifact): string {
  if (a.filePath) return a.filePath;
  return a.kind;
}

function iconForKind(kind?: string): string {
  switch (kind) {
    case 'code':           return 'terminal';
    case 'local-service':  return 'link';
    default:               return 'file-text';
  }
}

function open(a: Artifact): void {
  if (a.kind === 'local-service' && a.url) {
    artifactsStore.openBrowser(props.sessionId, a.url);
    return;
  }
  artifactsStore.openPreviewTab(props.sessionId, a.id);
}
</script>
