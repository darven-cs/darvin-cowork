<template>
  <div class="flex min-w-0 flex-col h-full">
    <ChatHeader
      :side-panel-open="sidePanelOpen"
      @toggle-sidebar="emit('toggle-sidebar')"
      @toggle-side-panel="emit('toggle-side-panel')"
    />
    <MessageList />
    <Composer ref="composerRef" :busy="busy" @send="handleSend" />
    <!-- 输入框下方的工具条：plus / grid / model / mic -->
    <div class="px-6 pb-4">
      <PromptToolbar
        @plus="onPlus"
        @grid="onGrid"
        @model="onModel"
        @mic="onMic"
      />
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

// PR-3 stubs
function onPlus()  { /* TODO: open PlusMenu */ }
function onGrid()  { /* TODO: open ExpertSuite */ }
function onModel() { /* TODO: open ModelPicker dropdown */ }
function onMic()   { /* TODO: start voice input */ }
</script>