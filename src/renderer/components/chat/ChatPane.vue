<template>
  <div class="flex min-w-0 flex-col h-full">
    <ChatHeader
      :side-panel-open="sidePanelOpen"
      @toggle-sidebar="emit('toggle-sidebar')"
      @toggle-side-panel="emit('toggle-side-panel')"
    />
    <MessageList />
    <Composer ref="composerRef" :busy="busy" @send="handleSend" />
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import ChatHeader from './ChatHeader.vue';
import MessageList from './MessageList.vue';
import Composer from './Composer.vue';
import { useSession } from '../../composables/useSession';
import { useMessages } from '../../composables/useMessages';

defineProps<{ sidePanelOpen: boolean }>();
const emit = defineEmits<{ 'toggle-sidebar': []; 'toggle-side-panel': [] }>();

const session = useSession();
const messages = useMessages();
const busy = ref<boolean>(false);
const composerRef = ref<InstanceType<typeof Composer> | null>(null);

async function handleSend(content: string) {
  if (!content.trim()) return;
  const sessId = session.activeSessionId.value;
  if (sessId === null) return;
  busy.value = true;
  messages.appendUserMessage(sessId, content);
  try {
    const r = await window.darvin.prompt({ content });
    messages.startAssistantMessage(r.sessionId, r.messageId);
  } catch (err) {
    const mid = `m-err-${Date.now().toString(36)}`;
    messages.startAssistantMessage(sessId, mid);
    messages.appendEvent({ type: 'error', messageId: mid, message: (err as Error).message });
  } finally {
    busy.value = false;
    composerRef.value?.focus();
  }
}
</script>