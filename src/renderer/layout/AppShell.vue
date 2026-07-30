<template>
  <div
    class="grid h-screen overflow-hidden bg-bg text-text"
    :style="{ gridTemplateColumns, transition: 'grid-template-columns 180ms cubic-bezier(0.4, 0, 0.2, 1)' }"
  >
    <Sidebar v-if="!sidebarCollapsed" class="col-start-1" @navigate="onSidebarNavigate" />
    <component
      :is="currentView"
      class="col-start-2"
      :side-panel-open="sidePanelOpen"
      @toggle-sidebar="sidebar.toggle"
      @toggle-side-panel="sidePanel.toggle"
      @navigate="navigateTo"
    />
    <SidePanel v-if="sidePanelOpen" class="col-start-3" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, watch } from 'vue';
import type { DarvinMessage } from '../../shared/darvin-api';
import Sidebar from '../components/sidebar/Sidebar.vue';
import SidePanel from '../components/side-panel/SidePanel.vue';
import HomeView from '../views/HomeView.vue';
import ChatView from '../views/ChatView.vue';
import ExpertSuiteView from '../views/ExpertSuiteView.vue';
import SettingsView from '../views/SettingsView.vue';
import { useSidebar } from '../composables/useSidebar';
import { useSidePanel } from '../composables/useSidePanel';
import { useTheme } from '../composables/useTheme';
import { useMessages } from '../composables/useMessages';
import { useSession } from '../composables/useSession';
import { useViewMode, type ViewMode } from '../composables/useViewMode';

const sidebar = useSidebar();
const sidePanel = useSidePanel();
useTheme(); // 立即应用持久化主题
const messages = useMessages();
const session = useSession();
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
    case 'home':
    default:         return HomeView;
  }
});

function navigateTo(target: string) {
  if (target === 'home' || target === 'chat' || target === 'suite' || target === 'settings') {
    viewMode.navigate(target as ViewMode);
  }
}

function onSidebarNavigate(target: string) {
  // Sidebar 已经在内部处理了 'home'（创建新会话）和 'suite' / 'settings'
  navigateTo(target);
}

// 历史消息从 agent 拉，不再读 mock-data。S5 的 getMessages 还返空，
// 所以切会话等于清空列表；S6 接上 agent.get_messages 后自然有内容。
async function reloadMessagesForCurrentSession() {
  messages.reset();
  const sessionId = session.currentSessionId.value;
  // agent 离线时静默降级：运行状态由 RuntimeStatusBadge 呈现
  const r = await window.darvin
    .getMessages(sessionId)
    .catch(() => ({ messages: [] as DarvinMessage[] }));
  // await 期间用户可能已经切走，别把旧会话的消息灌进新会话
  if (session.currentSessionId.value !== sessionId) return;
  for (const m of r.messages) {
    messages.list.value.push({
      id: m.id,
      sessionId: m.sessionId,
      role: m.role,
      content: m.content,
      done: m.done,
      createdAt: m.createdAt,
      error: m.error,
      toolLabel: m.toolLabel,
    });
  }
}

watch(() => session.currentSessionId.value, () => {
  void reloadMessagesForCurrentSession();
}, { immediate: true });

onMounted(async () => {
  window.darvin.onEvent((e) => messages.appendEvent(e));
  // S5 的 listSessions 返空，等于清掉 useSession 里的 mock 种子；
  // S6 接上 agent.list_sessions 之后才会有真会话。
  const r = await window.darvin.listSessions().catch(() => ({ sessions: [] }));
  session.sessions.value = r.sessions;
});
</script>