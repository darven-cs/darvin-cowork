<template>
  <aside
    class="relative flex h-full flex-col overflow-hidden border-r border-border bg-surface"
    :style="{ width: collapsed ? `${COMPACT_SIDEBAR_WIDTH}px` : 'var(--sidebar-width)' }"
  >
    <SidebarBrand :collapsed="collapsed" class="shrink-0" />
    <SidebarNav
      class="shrink-0"
      :active-id="activeNavId"
      :collapsed="collapsed"
      @navigate="onNavigate"
    />

    <SidebarAgentCard v-if="!collapsed" :collapsed="collapsed" class="shrink-0" />

    <div v-if="!collapsed" class="flex min-h-0 flex-1 flex-col">
      <div class="px-4 pt-3 pb-1 text-[11px] font-medium uppercase tracking-wider text-text-subtle">
        {{ t('sidebar.recent_label') }}
      </div>
      <SessionList
        :sessions="sessions"
        :current-id="currentId"
        @select="onSelect"
        @rename="onRename"
        @delete="onDelete"
      />
    </div>

    <SidebarBottom class="shrink-0" :collapsed="collapsed" @login="onLogin" @settings="onSettings" />

    <div
      v-if="!collapsed"
      class="absolute top-0 right-0 z-10 h-full w-1.5 cursor-col-resize"
      :aria-label="t('sidebar.drag.resize')"
      data-testid="sidebar-resize"
      @mousedown="onDragStart"
    />
  </aside>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import SidebarBrand from './SidebarBrand.vue';
import SidebarNav from './SidebarNav.vue';
import SidebarAgentCard from './SidebarAgentCard.vue';
import SessionList from './SessionList.vue';
import SidebarBottom from './SidebarBottom.vue';
import { useSession } from '../../composables/useSession';
import { useMessages } from '../../composables/useMessages';
import { useSidebar, COMPACT_SIDEBAR_WIDTH } from '../../composables/useSidebar';
import { useViewMode, type ViewMode } from '../../composables/useViewMode';
import { t } from '../../services/i18n';

type NavId = 'new_task' | 'search' | 'scheduled' | 'suite' | 'skill' | 'mcp';

const NAV_BY_VIEW: Record<string, NavId> = {
  home: 'new_task',
  chat: 'new_task',
  search: 'search',
  scheduled: 'scheduled',
  suite: 'suite',
  skills: 'skill',
  mcp: 'mcp',
};

defineProps<{ collapsed: boolean }>();
const emit = defineEmits<{ navigate: [target: string] }>();

const session = useSession();
const messages = useMessages();
const sidebar = useSidebar();
const viewMode = useViewMode();
const sessions = computed(() => session.sessions.value);
const currentId = computed(() =>
  session.draftMode.value ? '' : session.activeSessionId.value ?? '',
);

const activeNavId = computed<NavId | null>(
  () => NAV_BY_VIEW[viewMode.mode.value as ViewMode] ?? null,
);

function onNavigate(id: NavId) {
  if (id === 'new_task') {
    // 不创建会话，只进 compose 态；发首条消息时才真正建会话
    session.startNewTask();
    emit('navigate', 'home');
  } else if (id === 'search') {
    emit('navigate', 'search');
  } else if (id === 'scheduled') {
    emit('navigate', 'scheduled');
  } else if (id === 'suite') {
    emit('navigate', 'suite');
  } else if (id === 'skill') {
    emit('navigate', 'skills');
  } else if (id === 'mcp') {
    emit('navigate', 'mcp');
  }
}

function onSelect(id: string) {
  session.draftMode.value = false;
  void session.switchSession(id);
  emit('navigate', 'chat');
}

function onRename(id: string, title: string) {
  void session.renameSession(id, title);
}

async function onDelete(id: string) {
  const r = await session.deleteSession(id);
  messages.removeSession(id);
  if (r.nextActiveSessionId === null) {
    emit('navigate', 'home');
  }
}

function onLogin() {
  console.warn(t('sidebar.placeholder.warn'));
}

function onSettings() {
  emit('navigate', 'settings');
}

function onDragStart(e: MouseEvent) {
  e.preventDefault();
  const startX = e.clientX;
  const startWidth = sidebar.width.value;
  sidebar.setDragging(true);
  const move = (ev: MouseEvent) => {
    sidebar.setWidth(startWidth + (ev.clientX - startX));
  };
  const up = () => {
    sidebar.setDragging(false);
    window.removeEventListener('mousemove', move);
    window.removeEventListener('mouseup', up);
  };
  window.addEventListener('mousemove', move);
  window.addEventListener('mouseup', up);
}
</script>
