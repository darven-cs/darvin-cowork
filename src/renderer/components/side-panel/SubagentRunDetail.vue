<template>
  <div class="flex h-full min-h-0 flex-col" data-testid="subagent-run-detail">
    <!-- 顶部 bar -->
    <div class="flex h-11 shrink-0 items-center gap-2.5 border-b border-border px-3">
      <button
        type="button"
        class="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-text-muted transition-colors hover:bg-surface-hover hover:text-text"
        :aria-label="t('artifact.subagents.detail.back')"
        data-testid="subagent-detail-back"
        @click="$emit('back')"
      >
        <Icon name="arrow-up" :size="15" class="-rotate-90" />
      </button>
      <span class="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-primary-soft text-xs font-semibold text-primary">
        {{ initial(run) }}
      </span>
      <div class="min-w-0 flex-1">
        <div class="truncate text-sm font-medium text-text">{{ name(run) }}</div>
        <div v-if="run.prompt?.trim()" class="truncate text-[10px] text-text-subtle">{{ run.prompt }}</div>
      </div>
      <span class="inline-flex shrink-0 items-center gap-1.5 rounded-full bg-surface px-2 py-0.5 text-[10px] text-text-muted">
        <span class="h-1.5 w-1.5 shrink-0 rounded-full" :class="dotClass(run.status)" />
        {{ statusLabel(run.status) }}
      </span>
    </div>

    <!-- 消息滚动区 -->
    <div class="min-h-0 flex-1 overflow-y-auto p-3" data-testid="subagent-detail-content">
      <div v-if="messages.length === 0" class="flex h-full flex-col items-center justify-center gap-2 px-6 text-center">
        <p class="text-sm text-text-muted">{{ t('artifact.subagents.detail.empty') }}</p>
        <p v-if="run.prompt?.trim()" class="max-w-full rounded-md bg-surface px-3 py-2 text-left text-xs text-text-muted">
          {{ run.prompt }}
        </p>
      </div>
      <div v-else class="flex flex-col gap-2">
        <div
          v-for="m in messages"
          :key="m.id"
          class="rounded-md bg-surface px-3 py-2"
          :class="m.role === 'user' ? 'self-end bg-primary-soft' : ''"
        >
          <div class="mb-1 text-[10px] font-medium uppercase text-text-subtle">{{ roleLabel(m.role) }}</div>
          <div class="whitespace-pre-wrap break-words text-xs text-text">{{ m.content }}</div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, watch } from 'vue';
import type { SubagentRun, SubagentMessage } from '../../../shared/darvin-api';
import { useSubagents } from '../../composables/useSubagents';
import { t } from '../../services/i18n';
import Icon from '../common/Icon.vue';

const props = defineProps<{ run: SubagentRun }>();
defineEmits<{ back: [] }>();

const subagents = useSubagents();

const messages = computed<SubagentMessage[]>(
  () => subagents.messagesByRun.value[props.run.id] ?? [],
);

function name(run: SubagentRun): string {
  return subagents.getSubagentDisplayName(run);
}

function initial(run: SubagentRun): string {
  return subagents.getSubagentDisplayInitial(run);
}

function dotClass(status: SubagentRun['status']): string {
  switch (status) {
    case 'running': return 'animate-pulse bg-accent-blue';
    case 'error':
    case 'timeout': return 'bg-danger';
    case 'aborted': return 'bg-text-subtle';
    default: return 'bg-success';
  }
}

function statusLabel(status: SubagentRun['status']): string {
  switch (status) {
    case 'running': return t('artifact.subagents.section.running');
    case 'done': return t('artifact.subagents.section.done');
    case 'error': return t('artifact.subagents.section.error');
    case 'aborted':
    case 'timeout': return t('artifact.subagents.section.aborted');
    default: return status;
  }
}

function roleLabel(role: SubagentMessage['role']): string {
  switch (role) {
    case 'user': return 'User';
    case 'assistant': return 'Assistant';
    case 'tool_use': return 'Tool';
    case 'tool_result': return 'Tool result';
    case 'system': return 'System';
    default: return role;
  }
}

// running 态 5s 轮询补消息（列表状态轮询由 useSubagents 统一处理）
let detailTimer: ReturnType<typeof setInterval> | undefined;
watch(
  () => props.run.status,
  (status) => {
    if (status === 'running' && !detailTimer) {
      detailTimer = setInterval(() => {
        void subagents.loadMessages(props.run.id);
      }, 5_000);
    } else if (status !== 'running' && detailTimer) {
      clearInterval(detailTimer);
      detailTimer = undefined;
    }
  },
  { immediate: true },
);

onBeforeUnmount(() => {
  if (detailTimer) clearInterval(detailTimer);
});
</script>
