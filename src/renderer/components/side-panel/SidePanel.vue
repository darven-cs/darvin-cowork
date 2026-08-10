<template>
  <aside class="relative flex min-h-0 h-full flex-col border-l border-border bg-bg" :style="{ width: `${panelWidth}px` }">
    <ArtifactPanel v-if="sessionId" :session-id="sessionId" />
    <div v-else class="flex flex-1 items-center justify-center px-6 text-center" data-testid="artifact-empty">
      <p class="font-display text-base italic text-text-muted">{{ t('artifact.empty') }}</p>
    </div>
    <div
      class="absolute -left-1 top-0 z-10 h-full w-2 cursor-col-resize"
      :aria-label="t('artifact.drag.resize')"
      data-testid="sidepanel-resize"
      @mousedown="onDragStart"
    />
  </aside>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import ArtifactPanel from './ArtifactPanel.vue';
import { useArtifacts } from '../../composables/useArtifacts';
import { useSession } from '../../composables/useSession';
import { t } from '../../services/i18n';

const artifacts = useArtifacts();
const session = useSession();
const panelWidth = computed(() => artifacts.panelWidth.value);
const sessionId = computed(() => session.activeSessionId.value);

function onDragStart(e: MouseEvent) {
  e.preventDefault();
  const startX = e.clientX;
  const startWidth = artifacts.panelWidth.value;
  artifacts.setDragging(true);
  const move = (ev: MouseEvent) => {
    artifacts.setPanelWidth(startWidth - (ev.clientX - startX));
  };
  const up = () => {
    artifacts.setDragging(false);
    window.removeEventListener('mousemove', move);
    window.removeEventListener('mouseup', up);
  };
  window.addEventListener('mousemove', move);
  window.addEventListener('mouseup', up);
}
</script>
