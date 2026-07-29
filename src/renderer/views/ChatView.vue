<template>
  <div class="flex min-w-0 flex-col h-full">
    <ChatHeader
      :side-panel-open="sidePanelOpen"
      @toggle-sidebar="emit('toggle-sidebar')"
      @toggle-side-panel="emit('toggle-side-panel')"
    />
    <MessageList />
    <Composer ref="composerRef" :busy="busy" @send="handleSend" />
    <!-- 输入框下方的工具条：plus / grid / model / mic，与 Composer 同宽度居中、左缩进一致 -->
    <div class="px-6 pb-4">
      <div class="mx-auto flex max-w-[760px] items-center pl-4">
        <PromptToolbar
          @grid="onGrid"
          @mic="onMic"
        />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import ChatHeader from '../components/chat/ChatHeader.vue';
import MessageList from '../components/chat/MessageList.vue';
import Composer from '../components/chat/Composer.vue';
import PromptToolbar from '../components/home/PromptToolbar.vue';
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

// PR-3 / PR-4 stubs
function onGrid()  { /* TODO: open ExpertSuite */ }
function onMic()   { /* TODO: start voice input */ }
</script>