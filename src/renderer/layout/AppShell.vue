<template>
  <div
    class="grid h-screen overflow-hidden bg-bg text-text"
    :style="{ gridTemplateColumns, transition: dragging ? 'none' : 'grid-template-columns 180ms cubic-bezier(0.4, 0, 0.2, 1)' }"
  >
    <Sidebar class="col-start-1" :collapsed="sidebarCollapsed" @navigate="onSidebarNavigate" />
    <component
      v-if="!isPlaceholderView"
      :is="currentView"
      class="col-start-2 min-h-0"
      :side-panel-open="sidePanelOpen"
      @toggle-sidebar="sidebar.toggle"
      @toggle-side-panel="sidePanel.toggle"
      @navigate="navigateTo"
    />
    <PlaceholderView
      v-else
      class="col-start-2 min-h-0"
      :side-panel-open="sidePanelOpen"
      :title-key="placeholder.titleKey"
      :desc-key="placeholder.descKey"
      :icon="placeholder.icon"
      @toggle-sidebar="sidebar.toggle"
      @toggle-side-panel="sidePanel.toggle"
    />
    <SidePanel v-if="sidePanelOpen" class="col-start-3" />
    <ToastHost />
    <PermissionModal />
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
import PlaceholderView from '../views/PlaceholderView.vue';
import { useSidebar, COMPACT_SIDEBAR_WIDTH } from '../composables/useSidebar';
import { useSidePanel } from '../composables/useSidePanel';
import { useTheme } from '../composables/useTheme';
import { useMessages } from '../composables/useMessages';
import { useViewMode, type ViewMode } from '../composables/useViewMode';
import { useArtifacts } from '../composables/useArtifacts';
import { useShortcuts } from '../composables/useShortcuts';
import ToastHost from '../components/common/ToastHost.vue';
import PermissionModal from '../components/chat/PermissionModal.vue';

const sidebar = useSidebar();
const sidePanel = useSidePanel();
const artifacts = useArtifacts();
useTheme();
// 触发 useMessages 内部 watch（首次 active 拉历史、后续切 session 拉历史）
useMessages();
const viewMode = useViewMode();
useShortcuts();

const sidebarCollapsed = computed(() => sidebar.collapsed.value);
const sidePanelOpen = computed(() => sidePanel.open.value);
const dragging = computed(() => artifacts.dragging.value || sidebar.dragging.value);

const gridTemplateColumns = computed(() => {
  const left = sidebarCollapsed.value ? `${COMPACT_SIDEBAR_WIDTH}px` : 'var(--sidebar-width)';
  const right = sidePanelOpen.value ? `${artifacts.panelWidth.value}px` : '0px';
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

const PLACEHOLDERS: Record<string, { titleKey: string; descKey: string; icon: string }> = {
  scheduled: { titleKey: 'sidebar.nav.scheduled', descKey: 'sidebar.placeholder.scheduled.desc', icon: 'clock' },
  skills:    { titleKey: 'sidebar.nav.skill',     descKey: 'sidebar.placeholder.skills.desc',    icon: 'star' },
  mcp:       { titleKey: 'sidebar.nav.mcp',       descKey: 'sidebar.placeholder.mcp.desc',       icon: 'link' },
};

const isPlaceholderView = computed(() => viewMode.mode.value in PLACEHOLDERS);
const placeholder = computed(() => PLACEHOLDERS[viewMode.mode.value]);

function navigateTo(target: string) {
  if (target === 'home' || target === 'chat' || target === 'suite' || target === 'settings' || target === 'search') {
    viewMode.navigate(target as ViewMode);
  } else if (target in PLACEHOLDERS) {
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
