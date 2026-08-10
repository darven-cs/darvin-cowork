<template>
  <div class="flex h-full flex-col" data-testid="subagent-panel-content">
    <div
      v-if="loading && runs.length === 0"
      class="flex flex-1 items-center justify-center px-6 text-center text-sm text-text-muted"
      data-testid="subagent-loading"
    >
      {{ t('artifact.fileList.loading') }}
    </div>

    <div
      v-else-if="selectedId && selected"
      class="flex min-h-0 flex-1 flex-col"
      data-testid="subagent-detail"
    >
      <SubagentRunDetail :run="selected" @back="selectRun(null)" />
    </div>

    <template v-else>
      <div class="flex h-9 shrink-0 items-center border-b border-border px-4" data-testid="subagent-panel-title">
        <h2 class="text-sm font-medium text-text">{{ t('artifact.special.subagents') }}</h2>
      </div>
      <div
        v-if="runs.length === 0"
        class="flex flex-1 flex-col items-center justify-center px-6 text-center"
        data-testid="subagent-empty"
      >
        <p class="text-sm text-text-muted">{{ t('artifact.subagents.empty') }}</p>
      </div>
      <div v-else class="min-h-0 flex-1 overflow-y-auto" data-testid="subagent-list">
        <SubagentRunList :runs="runs" @select="selectRun" />
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue';
import { useSubagents } from '../../composables/useSubagents';
import { t } from '../../services/i18n';
import SubagentRunList from './SubagentRunList.vue';
import SubagentRunDetail from './SubagentRunDetail.vue';

// sessionId 由 ArtifactPanel 传入；useSubagents 从 useSession().activeSessionId
// 取当前 session，props.sessionId 保留给未来跨 session 浏览用（当前单活性一致）。
defineProps<{ sessionId: string }>();

const subagents = useSubagents();
const { runs, loading, selectedId, selectRun } = subagents;

// 面板激活时拉一次当前 session 的 runs（run 可能在面板挂载后产生）。
onMounted(() => {
  void subagents.refreshList();
});

const selected = computed(() => runs.value.find((r) => r.id === selectedId.value) ?? null);
</script>
