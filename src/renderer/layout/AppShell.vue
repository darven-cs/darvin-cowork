<template>
  <div
    class="grid h-screen overflow-hidden bg-bg text-text"
    :style="{ gridTemplateColumns, transition: 'grid-template-columns 180ms cubic-bezier(0.4, 0, 0.2, 1)' }"
  >
    <Sidebar v-if="!sidebarCollapsed" class="col-start-1" @navigate="onSidebarNavigate" />
    <component
      :is="currentView"
      class="col-start-2 min-h-0"
      :side-panel-open="sidePanelOpen"
      @toggle-sidebar="sidebar.toggle"
      @toggle-side-panel="sidePanel.toggle"
      @navigate="navigateTo"
    />
    <SidePanel v-if="sidePanelOpen" class="col-start-3" />
    <ToastHost />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue';
import Sidebar from '../components/sidebar/Sidebar.vue';
import SidePanel from '../components/side-panel/SidePanel.vue';
import HomeView from '../views/HomeView.vue';
import ChatView from '../views/ChatView.vue';
import ExpertSuiteView from '../views/ExpertSuiteView.vue';
import SettingsView from '../views/SettingsView.vue';
import SearchView from '../views/SearchView.vue';
import { useSidebar } from '../composables/useSidebar';
import { useSidePanel } from '../composables/useSidePanel';
import { useTheme } from '../composables/useTheme';
import { useMessages } from '../composables/useMessages';
import { useViewMode, type ViewMode } from '../composables/useViewMode';
import ToastHost from '../components/common/ToastHost.vue';

const sidebar = useSidebar();
const sidePanel = useSidePanel();
useTheme();
// 触发 useMessages 内部 watch（首次 active 拉历史、后续切 session 拉历史）
useMessages();
const viewMode = useViewMode();

const sidebarCollapsed = computed(() => sidebar.collapsed.value);
const sidePanelOpen = computed(() => sidePanel.open.value);

const gridTemplateColumns = computed(() => {
  const left = sidebarCollapsed.value ? '0px' : '220px';
  const right = sidePanelOpen.value ? '300px' : '0px';
  return `${left} 1fr ${right}`;
});

const currentView = computed(() => {
  switch (viewMode.mode.value) {
    case 'chat':     return ChatView;
    case 'suite':    return ExpertSuiteView;
    case 'settings': return SettingsView;
    case 'search':   return SearchView;
    case 'home':
    default:         return HomeView;
  }
});

function navigateTo(target: string) {
  if (target === 'home' || target === 'chat' || target === 'suite' || target === 'settings' || target === 'search') {
    viewMode.navigate(target as ViewMode);
  }
}

function onSidebarNavigate(target: string) {
  navigateTo(target);
}

onMounted(() => {
  const messages = useMessages();
  window.darvin.onEvent((e) => messages.appendEvent(e));
});
</script>