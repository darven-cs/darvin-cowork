<template>
  <div ref="scrollRef" class="flex-1 overflow-y-auto px-6 py-8">
    <div
      v-if="messageList.length === 0"
      class="flex h-full flex-col items-center justify-center gap-2 text-text-muted"
    >
      <p class="font-display text-2xl italic">{{ t('chat.empty.title') }}</p>
      <p class="text-xs text-text-subtle">{{ t('chat.empty') }}</p>
    </div>
    <div v-else class="mx-auto flex max-w-[720px] flex-col gap-6">
      <MessageItem
        v-for="msg in messageList"
        :key="msg.id"
        :message="msg"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import MessageItem from './MessageItem.vue';
import { useMessages } from '../../composables/useMessages';
import { t } from '../../services/i18n';

const messages = useMessages();
const messageList = computed(() => messages.list.value);
const scrollRef = ref<HTMLDivElement | null>(null);

watch(
  () => messageList.value.length,
  async () => {
    await nextTick();
    const el = scrollRef.value;
    if (el) el.scrollTo({ top: el.scrollHeight, behavior: 'smooth' });
  },
);

watch(
  () => messageList.value.map((m) => `${m.id}:${m.content.length}`).join('|'),
  async () => {
    await nextTick();
    const el = scrollRef.value;
    if (el) el.scrollTo({ top: el.scrollHeight, behavior: 'auto' });
  },
);
</script>
