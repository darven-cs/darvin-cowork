<template>
  <div class="flex h-full flex-col" data-testid="artifact-panel">
    <div class="flex h-8 shrink-0 items-stretch overflow-x-auto border-b border-border bg-surface-2">
      <button
        v-for="sp in specialTabs"
        :key="sp.id"
        type="button"
        class="shrink-0 px-2 text-xs"
        :class="tabClass(sp.id)"
        @click="activate(sp.id)"
      >
        {{ sp.label }}
      </button>
      <button
        v-for="tab in previewTabs"
        :key="tab.id"
        type="button"
        class="group flex shrink-0 items-center gap-1 px-2 text-xs"
        :class="tabClass(tab.id)"
        data-testid="artifact-preview-tab"
        @click="activate(tab.id)"
      >
        <span class="max-w-[96px] truncate">{{ tabName(tab) }}</span>
        <span
          role="button"
          class="text-text-subtle transition-colors hover:text-text"
          :aria-label="t('artifact.tab.close')"
          @click.stop="closeTab(tab.id)"
        >
          ×
        </span>
      </button>
    </div>

    <div
      v-if="activePreviewTab"
      class="flex shrink-0 items-center justify-end gap-1 border-b border-border px-2 py-1"
      data-testid="artifact-view-toggle"
    >
      <button
        type="button"
        class="rounded px-1.5 py-0.5 text-xs transition-colors"
        :class="showCode ? 'text-text-muted hover:text-text' : 'bg-surface-hover text-text'"
        @click="setView(ArtifactContentView.Preview)"
      >
        {{ t('artifact.view.preview') }}
      </button>
      <button
        type="button"
        class="rounded px-1.5 py-0.5 text-xs transition-colors"
        :class="showCode ? 'bg-surface-hover text-text' : 'text-text-muted hover:text-text'"
        @click="setView(ArtifactContentView.Code)"
      >
        {{ t('artifact.view.code') }}
      </button>
    </div>

    <div class="min-h-0 flex-1 overflow-hidden">
      <ArtifactRenderer v-if="activeArtifactItem && !showCode" :artifact="activeArtifactItem" />
      <pre v-else-if="activeArtifactItem && showCode" class="h-full w-full overflow-auto whitespace-pre-wrap break-words p-3 font-mono text-xs text-text">{{ activeArtifactItem.content }}</pre>
      <div v-else class="flex h-full flex-col items-center justify-center px-6 text-center">
        <p class="font-display text-base italic text-text-muted">{{ specialLabel }}</p>
        <p class="mt-1 text-sm text-text-subtle">{{ emptyHint }}</p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { ArtifactContentView, ArtifactSpecialTab, isSpecialTabId, useArtifacts } from '../../composables/useArtifacts';
import type { Artifact, ArtifactPreviewTab } from '../../composables/useArtifacts';
import ArtifactRenderer from './ArtifactRenderer.vue';
import { t } from '../../services/i18n';

const props = defineProps<{ sessionId: string }>();

const artifacts = useArtifacts();

const specialTabs: { id: ArtifactSpecialTab; label: string }[] = [
  { id: ArtifactSpecialTab.FileList, label: t('artifact.special.fileList') },
  { id: ArtifactSpecialTab.Browser, label: t('artifact.special.browser') },
  { id: ArtifactSpecialTab.Subagents, label: t('artifact.special.subagents') },
];

const previewTabs = computed<ArtifactPreviewTab[]>(
  () => artifacts.previewTabsBySession.value[props.sessionId] ?? [],
);
const activeTabId = computed<string | null>(
  () => artifacts.activeTabIdBySession.value[props.sessionId] ?? null,
);
const sessionArtifacts = computed<Artifact[]>(
  () => artifacts.artifactsBySession.value[props.sessionId] ?? [],
);
const artifactById = computed(() => new Map(sessionArtifacts.value.map((a) => [a.id, a])));
const activePreviewTab = computed<ArtifactPreviewTab | null>(
  () => previewTabs.value.find((tab) => tab.id === activeTabId.value) ?? null,
);
const activeArtifactItem = computed<Artifact | null>(
  () => (activePreviewTab.value ? artifactById.value.get(activePreviewTab.value.artifactId) ?? null : null),
);
const showCode = computed(
  () => activePreviewTab.value?.contentView === ArtifactContentView.Code,
);

const specialLabel = computed(() => {
  const id = activeTabId.value;
  if (id === ArtifactSpecialTab.FileList) return t('artifact.special.fileList');
  if (id === ArtifactSpecialTab.Browser) return t('artifact.special.browser');
  if (id === ArtifactSpecialTab.Subagents) return t('artifact.special.subagents');
  return t('sidepanel.tabs.artifact');
});

const emptyHint = computed(() => {
  if (isSpecialTabId(activeTabId.value)) return t('artifact.special.placeholder');
  return t('artifact.empty');
});

function tabClass(id: string): string {
  return id === activeTabId.value ? 'text-text' : 'text-text-muted hover:text-text';
}

function tabName(tab: ArtifactPreviewTab): string {
  return artifactById.value.get(tab.artifactId)?.name ?? tab.artifactId;
}

function activate(id: string): void {
  artifacts.activateTab(props.sessionId, id);
}

function closeTab(tabId: string): void {
  artifacts.closePreviewTab(props.sessionId, tabId);
}

function setView(view: ArtifactContentView): void {
  if (!activePreviewTab.value) return;
  artifacts.setContentView(props.sessionId, activePreviewTab.value.id, view);
}
</script>
