<template>
  <aside class="flex h-full w-[220px] flex-col overflow-hidden border-r border-border bg-surface">
    <SidebarBrand class="shrink-0" />
    <SidebarNav class="shrink-0" :active-id="activeNavId" @navigate="onNavigate" />

    <SidebarAgentCard class="shrink-0" />

    <div class="flex min-h-0 flex-1 flex-col">
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

    <SidebarBottom class="shrink-0" @login="onLogin" @settings="onSettings" />
  </aside>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';
import SidebarBrand from './SidebarBrand.vue';
import SidebarNav from './SidebarNav.vue';
import SidebarAgentCard from './SidebarAgentCard.vue';
import SessionList from './SessionList.vue';
import SidebarBottom from './SidebarBottom.vue';
import { useSession } from '../../composables/useSession';
import { useMessages } from '../../composables/useMessages';
import { t } from '../../services/i18n';

type NavId = 'new_task' | 'search' | 'scheduled' | 'suite' | 'skill' | 'mcp';

const emit = defineEmits<{ navigate: [target: string] }>();

const session = useSession();
const messages = useMessages();
const sessions = computed(() => session.sessions.value);
const currentId = computed(() =>
  session.draftMode.value ? '' : session.activeSessionId.value ?? '',
);

const activeNavId = ref<NavId>('new_task');

function onNavigate(id: NavId) {
  activeNavId.value = id;
  if (id === 'new_task') {
    // 不创建会话，只进 compose 态；发首条消息时才真正建会话
    session.startNewTask();
    emit('navigate', 'home');
  } else if (id === 'search') {
    emit('navigate', 'search');
  } else if (id === 'suite') {
    emit('navigate', 'suite');
  } else {
    console.warn(t('sidebar.placeholder.warn'));
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
</script>
