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
import ChatHeader from '../components/chat/ChatHeader.vue';
import MessageList from '../components/chat/MessageList.vue';
import Composer from '../components/chat/Composer.vue';
import { useChatActions } from '../composables/useChatActions';

defineProps<{ sidePanelOpen: boolean }>();
const emit = defineEmits<{ 'toggle-sidebar': []; 'toggle-side-panel': [] }>();

const busy = ref<boolean>(false);
const composerRef = ref<InstanceType<typeof Composer> | null>(null);
const chatActions = useChatActions();

async function handleSend(content: string) {
  await chatActions.send(content, busy);
  composerRef.value?.focus();
}
</script>