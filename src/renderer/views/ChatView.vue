<template>
  <div class="flex min-w-0 flex-col h-full">
    <ChatHeader
      :side-panel-open="sidePanelOpen"
      @toggle-sidebar="emit('toggle-sidebar')"
      @toggle-side-panel="emit('toggle-side-panel')"
    />
    <MessageList />
    <Composer ref="composerRef" :busy="busy" :running="running" @send="handleSend" />
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';
import ChatHeader from '../components/chat/ChatHeader.vue';
import MessageList from '../components/chat/MessageList.vue';
import Composer from '../components/chat/Composer.vue';
import { useChatActions } from '../composables/useChatActions';
import { useMessages } from '../composables/useMessages';
import { useSession } from '../composables/useSession';

defineProps<{ sidePanelOpen: boolean }>();
const emit = defineEmits<{ 'toggle-sidebar': []; 'toggle-side-panel': [] }>();

const busy = ref<boolean>(false);
const composerRef = ref<InstanceType<typeof Composer> | null>(null);
const chatActions = useChatActions();
const messages = useMessages();
const session = useSession();
// 当前 active session 处于流式运行时点亮输入框光晕（与侧栏 running 点同源）。
const running = computed(() =>
  messages.streamingSessionIds.value.has(session.activeSessionId.value ?? ''),
);

async function handleSend(content: string) {
  await chatActions.send(content, busy);
  composerRef.value?.focus();
}
</script>