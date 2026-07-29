<template>
  <aside class="flex h-full w-[220px] flex-col border-r border-border bg-surface">
    <!-- 段 1：Brand + Nav -->
    <SidebarBrand />
    <SidebarNav :active-id="activeNavId" @navigate="onNavigate" />

    <!-- 段 2：我的 Agent -->
    <SidebarAgentCard />

    <!-- 段 3：近期任务（flex-1 scroll） -->
    <div class="flex min-h-0 flex-1 flex-col">
      <div class="px-4 pt-3 pb-1 text-[11px] font-medium uppercase tracking-wider text-text-subtle">
        {{ t('sidebar.recent_label') }}
      </div>
      <SessionList
        :sessions="sessions"
        :current-id="currentId"
        @select="onSelect"
      />
    </div>

    <!-- 段 4：底部按钮 -->
    <SidebarBottom @login="onLogin" @settings="onSettings" />
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
import { t } from '../../services/i18n';

type NavId = 'new_task' | 'search' | 'scheduled' | 'suite' | 'skill' | 'mcp';

const emit = defineEmits<{ navigate: [target: string]; 'new-chat': [] }>();

const session = useSession();
const sessions = computed(() => session.sessions.value);
const currentId = computed(() => session.currentSessionId.value);

const activeNavId = ref<NavId>('new_task');

function onNavigate(id: NavId) {
  activeNavId.value = id;
  if (id === 'new_task') {
    session.createSession();
    emit('new-chat');
    emit('navigate', 'home');
  } else if (id === 'suite') {
    emit('navigate', 'suite');
  } else {
    console.warn(t('sidebar.placeholder.warn'));
  }
}

function onSelect(id: string) {
  session.switchSession(id);
}

function onLogin() {
  console.warn(t('sidebar.placeholder.warn'));
}

function onSettings() {
  emit('navigate', 'settings');
}
</script>
