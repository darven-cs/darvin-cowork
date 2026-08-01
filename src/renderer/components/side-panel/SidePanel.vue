<template>
  <aside class="relative flex h-full flex-col border-l border-border bg-bg" :style="{ width: `${panelWidth}px` }">
    <SidePanelTabs :active="tab" @switch="onSwitch" />
    <SidePanelContent :tab="tab" />
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
import SidePanelTabs from './SidePanelTabs.vue';
import SidePanelContent from './SidePanelContent.vue';
import { useSidePanel } from '../../composables/useSidePanel';
import { useArtifacts } from '../../composables/useArtifacts';
import { t } from '../../services/i18n';

const panel = useSidePanel();
const artifacts = useArtifacts();
const tab = computed(() => panel.tab.value);
const panelWidth = computed(() => artifacts.panelWidth.value);

function onSwitch(t: Parameters<typeof panel.switchTab>[0]) {
  panel.switchTab(t);
}

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
