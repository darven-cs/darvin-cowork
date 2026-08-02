<template>
  <div
    class="mt-1 flex flex-col items-start gap-0.5 font-mono text-[10px] text-text-subtle opacity-0 transition-opacity duration-200 group-hover:opacity-100"
  >
    <div class="flex items-center gap-2">
      <span>{{ timestamp }}</span>
      <span v-if="model" class="rounded bg-surface-raised px-1 py-px">{{ model }}</span>
      <IconButton name="copy" :label="t('chat.copy')" :size="12" @click="handleCopy" />
      <IconButton name="refresh" :label="t('chat.regenerate')" :size="12" @click="handleRegenerate" />
      <IconButton name="git-branch" :label="t('chat.fork')" :size="12" disabled />
    </div>
    <span v-if="usageLine" class="text-text-muted">{{ usageLine }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import IconButton from '../common/IconButton.vue';
import type { Message } from '../../composables/useMessages';
import { useChatActions } from '../../composables/useChatActions';
import { formatDate, t } from '../../services/i18n';
import { formatTokenCount } from '../../services/tokenFormat';

const props = defineProps<{ message: Message }>();

const actions = useChatActions();

const model = computed(() => props.message.model);

// spec 03 — 单条 assistant 消息的 token 三元组（in / out / cache），
// done 事件带 usage 时展示；cache 缺省则只显示 in/out。
const usageLine = computed(() => {
  const u = props.message.usage;
  if (!u) return '';
  const parts = [`${formatTokenCount(u.inputTokens)} in`, `${formatTokenCount(u.outputTokens)} out`];
  if (typeof u.cacheReadTokens === 'number') {
    parts.push(`${formatTokenCount(u.cacheReadTokens)} cache`);
  }
  return parts.join(' · ');
});

const timestamp = computed(() => {
  try {
    return formatDate(props.message.createdAt, { hour: '2-digit', minute: '2-digit' });
  } catch {
    return '';
  }
});

async function handleCopy() {
  await actions.copy(props.message.content);
}

function handleRegenerate() {
  void actions.regenerate(props.message.id);
}
</script>
