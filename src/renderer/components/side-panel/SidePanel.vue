<template>
  <aside class="flex h-full w-[300px] flex-col border-l border-border bg-bg">
    <SidePanelTabs :active="tab" @switch="onSwitch" />
    <SidePanelContent :tab="tab" />
  </aside>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import SidePanelTabs from './SidePanelTabs.vue';
import SidePanelContent from './SidePanelContent.vue';
import { useSidePanel } from '../../composables/useSidePanel';

const panel = useSidePanel();
const tab = computed(() => panel.tab.value);

function onSwitch(t: Parameters<typeof panel.switchTab>[0]) {
  panel.switchTab(t);
}
</script>
