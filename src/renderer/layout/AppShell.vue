<template>
  <div
    class="grid h-screen overflow-hidden bg-bg text-text"
    :style="{ gridTemplateColumns, transition: 'grid-template-columns 180ms cubic-bezier(0.4, 0, 0.2, 1)' }"
  >
    <Sidebar v-if="!sidebarCollapsed" class="col-start-1" @navigate="onNavigate" />
    <ChatPane
      class="col-start-2"
      :side-panel-open="sidePanelOpen"
      @toggle-sidebar="sidebar.toggle"
      @toggle-side-panel="sidePanel.toggle"
    />
    <SidePanel v-if="sidePanelOpen" class="col-start-3" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, watch } from 'vue';
import Sidebar from '../components/sidebar/Sidebar.vue';
import ChatPane from '../components/chat/ChatPane.vue';
import SidePanel from '../components/side-panel/SidePanel.vue';
import { useSidebar } from '../composables/useSidebar';
import { useSidePanel } from '../composables/useSidePanel';
import { useTheme } from '../composables/useTheme';
import { useMessages } from '../composables/useMessages';
import { useSession } from '../composables/useSession';
import { mockMessages } from '../services/mock-data';

const sidebar = useSidebar();
const sidePanel = useSidePanel();
useTheme(); // 立即应用持久化主题
const messages = useMessages();
const session = useSession();

const sidebarCollapsed = computed(() => sidebar.collapsed.value);
const sidePanelOpen = computed(() => sidePanel.open.value);

const gridTemplateColumns = computed(() => {
  const left = sidebarCollapsed.value ? '0px' : '220px';
  const right = sidePanelOpen.value ? '300px' : '0px';
  return `${left} 1fr ${right}`;
});

function onNavigate(_target: string): void {
  // 视图路由在 useViewMode composable 落地后启用
}

function reloadMessagesForCurrentSession() {
  const list = mockMessages[session.currentSessionId.value] ?? [];
  messages.reset();
  for (const m of list) {
    messages.list.value.push({
      id: m.id,
      sessionId: m.sessionId,
      role: m.role,
      content: m.content,
      done: m.done,
      createdAt: m.createdAt,
      error: m.error,
    });
  }
}

watch(() => session.currentSessionId.value, () => {
  reloadMessagesForCurrentSession();
}, { immediate: true });

onMounted(() => {
  window.darvin.onEvent((e) => messages.appendEvent(e));
});
</script>
