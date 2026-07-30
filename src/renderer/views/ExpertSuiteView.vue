<template>
  <div class="flex min-w-0 flex-col h-full">
    <ChatHeader
      :side-panel-open="sidePanelOpen"
      @toggle-sidebar="emit('toggle-sidebar')"
      @toggle-side-panel="emit('toggle-side-panel')"
    />

    <!-- 顶栏：标题 + 搜索框 + filter tabs -->
    <div class="border-b border-border px-6 pt-6 pb-4">
      <div class="flex items-center justify-between">
        <h2 class="font-sans text-[20px] font-semibold text-text">专家套件</h2>
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

    <!-- 卡片网格 -->
    <div class="flex-1 overflow-y-auto p-6">
      <div v-if="filtered.length === 0" class="py-16 text-center font-sans text-[13px] text-text-muted">
        没有匹配的专家
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
import { computed, ref } from 'vue';
import ChatHeader from '../components/chat/ChatHeader.vue';
import Icon from '../components/common/Icon.vue';
import AgentCard from '../components/expert/AgentCard.vue';
import AgentFilterTabs, { type FilterTabId } from '../components/expert/AgentFilterTabs.vue';
import { t } from '../services/i18n';
import { expertSuiteAgents, type ExpertAgent } from '../services/mock-data';

defineProps<{ sidePanelOpen: boolean }>();
const emit = defineEmits<{
  'toggle-sidebar': [];
  'toggle-side-panel': [];
}>();

const query = ref<string>('');
const activeTab = ref<FilterTabId>('all');

const filtered = computed<ExpertAgent[]>(() => {
  const q = query.value.trim().toLowerCase();
  return expertSuiteAgents.filter((a) => {
    // tab 过滤：free 是按价格，其余按分类
    if (activeTab.value === 'free' && a.price !== 'Free') return false;
    if (activeTab.value !== 'all' && activeTab.value !== 'free' && a.category !== activeTab.value) return false;
    if (q && !a.name.toLowerCase().includes(q) && !a.description.toLowerCase().includes(q)) return false;
    return true;
  });
});

function onSelectTab(id: FilterTabId) {
  activeTab.value = id;
}

function onUse(agent: ExpertAgent) {
  // PR-4 stub：仅 console.log，不接跳 ExpertSuite 实际工作流
  // eslint-disable-next-line no-console
  console.log('[expert] use:', agent.id, agent.name);
}

function onDetails(agent: ExpertAgent) {
  // eslint-disable-next-line no-console
  console.log('[expert] details:', agent.id, agent.name);
}
</script>