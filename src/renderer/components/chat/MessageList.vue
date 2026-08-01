<template>
  <div ref="scrollRef" class="min-h-0 flex-1 overflow-y-auto px-6 py-8">
    <div
      v-if="turns.length === 0"
      class="flex h-full flex-col items-center justify-center gap-2 text-text-muted"
    >
      <p class="font-display text-2xl italic">{{ t('chat.empty.title') }}</p>
      <p class="text-xs text-text-subtle">{{ t('chat.empty') }}</p>
    </div>
    <div v-else class="mx-auto flex max-w-[760px] flex-col gap-6">
      <template v-for="turn in turns" :key="turn.id">
        <ContextCompactionDivider
          v-for="marker in turn.precedingCompactions"
          :key="marker.checkpointId"
          :marker="marker"
        />
        <ConversationTurn :turn="turn" />
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import ConversationTurn from './ConversationTurn.vue';
import ContextCompactionDivider from './ContextCompactionDivider.vue';
import { buildConversationTurns, useMessages } from '../../composables/useMessages';
import { t } from '../../services/i18n';

const messages = useMessages();
const turns = computed(() =>
  buildConversationTurns(messages.currentMessages.value, messages.currentCompactions.value),
);
const scrollRef = ref<HTMLDivElement | null>(null);

watch(
  () => turns.value.length,
  async () => {
    await nextTick();
    const el = scrollRef.value;
    if (el) el.scrollTo({ top: el.scrollHeight, behavior: 'smooth' });
  },
);

watch(
  () => turns.value.map((t) => t.assistantItems.map((it) => {
    if (it.type === 'tool_group') return `t:${it.toolUse.id}:${it.toolResult ? '1' : '0'}`;
    return `${it.message.id}:${it.message.content.length}:${it.message.thinking?.length ?? 0}`;
  }).join('|')).join('~'),
  async () => {
    await nextTick();
    const el = scrollRef.value;
    if (el) el.scrollTo({ top: el.scrollHeight, behavior: 'auto' });
  },
);
</script>
