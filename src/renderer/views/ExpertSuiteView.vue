<template>
  <div class="flex min-w-0 flex-col h-full">
    <ChatHeader
      :side-panel-open="sidePanelOpen"
      @toggle-sidebar="emit('toggle-sidebar')"
      @toggle-side-panel="emit('toggle-side-panel')"
    />

    <div class="border-b border-border px-6 pt-6 pb-4">
      <div class="flex items-center justify-between">
        <h2 class="font-sans text-[20px] font-semibold text-text">{{ t('sidebar.nav.suite') }}</h2>
        <div class="relative w-72">
          <Icon name="search" :size="14" class="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-text-subtle" />
          <input
            v-model="query"
            type="text"
            :placeholder="t('expert.search.placeholder')"
            class="w-full rounded-md border border-border bg-surface-raised py-1.5 pr-3 pl-8 font-sans text-[13px] text-text outline-none placeholder:text-text-muted focus:border-primary"
          />
        </div>
      </div>
      <div class="mt-4">
        <AgentFilterTabs :active="activeTab" @select="onSelectTab" />
      </div>
    </div>

    <div class="flex-1 overflow-y-auto p-6">
      <div v-if="filtered.length === 0" class="py-16 text-center font-sans text-[13px] text-text-muted">
        {{ t('expert.no_match') }}
      </div>
      <div v-else class="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
        <AgentCard
          v-for="agent in filtered"
          :key="agent.id"
          :agent="agent"
          @use="onUse"
          @details="onDetails"
        />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import ChatHeader from '../components/chat/ChatHeader.vue';
import Icon from '../components/common/Icon.vue';
import AgentCard from '../components/expert/AgentCard.vue';
import AgentFilterTabs, { type FilterTabId } from '../components/expert/AgentFilterTabs.vue';
import { useAgents } from '../composables/useAgents';
import { useSession } from '../composables/useSession';
import { useViewMode } from '../composables/useViewMode';
import { useWorkspaces } from '../composables/useWorkspaces';
import { t, getLang } from '../services/i18n';
import { darvinAgentToExpert, type ExpertAgent } from '../services/mock-data';

defineProps<{ sidePanelOpen: boolean }>();
const emit = defineEmits<{
  'toggle-sidebar': [];
  'toggle-side-panel': [];
}>();

const query = ref<string>('');
const activeTab = ref<FilterTabId>('all');

const session = useSession();
const viewMode = useViewMode();
const workspaces = useWorkspaces();
const agentsApi = useAgents();

// 拉当前 workspace 的 agent 列表；workspace 切换时刷新；服务端 push 触发二次刷新。
async function syncAgents(): Promise<void> {
  const ws = workspaces.activeWorkspaceId.value;
  if (!ws) {
    agentsApi.listAgents('').then(() => undefined).catch(() => undefined);
    return;
  }
  await agentsApi.listAgents(ws);
}
void syncAgents();
watch(() => workspaces.activeWorkspaceId.value, () => { void syncAgents(); });
watch(() => agentsApi.agents.value.length, () => { /* 触发 derived re-evaluation */ });

const filtered = computed<ExpertAgent[]>(() => {
  const q = query.value.trim().toLowerCase();
  const en = getLang() === 'en';
  // 过滤 Main Agent（isDefault=true）— 它走设置页切换默认 agent，不进专家套件。
  return agentsApi.agents.value
    .filter((a) => !a.isDefault)
    .map(darvinAgentToExpert)
    .filter((a) => {
      if (activeTab.value === 'free' && a.price !== 'Free') return false;
      if (activeTab.value !== 'all' && activeTab.value !== 'free' && a.category !== activeTab.value) return false;
      if (q) {
        const name = en ? a.nameEn : a.name;
        const desc = en ? a.descriptionEn : a.description;
        if (!name.toLowerCase().includes(q) && !desc.toLowerCase().includes(q)) return false;
      }
      return true;
    });
});

function onSelectTab(id: FilterTabId) {
  activeTab.value = id;
}

function onUse(agent: ExpertAgent) {
  // workspaceId 不传 → main 侧兜底 activeWorkspaceId；systemPrompt/identity 不传，
  // 由 agent 表内容派生（handler 端 SnapShot 到 session）。
  void session.createSession(agent.name, undefined, '', '', agent.id).then(() => {
    viewMode.navigate('chat');
  });
}

function onDetails(agent: ExpertAgent) {
  // eslint-disable-next-line no-console
  console.log('[expert] details:', agent.id, agent.name);
}
</script>