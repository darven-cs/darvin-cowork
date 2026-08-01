<template>
  <div
    class="mt-1 flex items-center gap-2 font-mono text-[10px] text-text-subtle opacity-0 transition-opacity duration-200 group-hover:opacity-100"
  >
    <span>{{ timestamp }}</span>
    <span v-if="model" class="rounded bg-surface-raised px-1 py-px">{{ model }}</span>
    <IconButton name="copy" :label="t('chat.copy')" :size="12" @click="handleCopy" />
    <IconButton name="refresh" :label="t('chat.regenerate')" :size="12" @click="handleRegenerate" />
    <IconButton name="git-branch" :label="t('chat.fork')" :size="12" disabled />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import IconButton from '../common/IconButton.vue';
import type { Message } from '../../composables/useMessages';
import { useChatActions } from '../../composables/useChatActions';
import { getLang, t } from '../../services/i18n';

const props = defineProps<{ message: Message }>();

const actions = useChatActions();

const model = computed(() => props.message.model);

const timestamp = computed(() => {
  try {
    return new Intl.DateTimeFormat(getLang(), {
      hour: '2-digit',
      minute: '2-digit',
    }).format(new Date(props.message.createdAt));
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
