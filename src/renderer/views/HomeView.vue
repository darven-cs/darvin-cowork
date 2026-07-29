<template>
  <div class="flex min-w-0 flex-col h-full">
    <ChatHeader
      :side-panel-open="sidePanelOpen"
      @toggle-sidebar="emit('toggle-sidebar')"
      @toggle-side-panel="emit('toggle-side-panel')"
    />

    <!-- hero 区：mascot + greeting 居中 -->
    <div class="flex-1 overflow-y-auto px-6 animate-fade-in">
      <div class="mx-auto flex h-full w-full max-w-[760px] flex-col items-center justify-center gap-6 py-8">
        <Mascot :size="96" />
        <HeroGreeting />
      </div>
    </div>

    <!-- 输入 dock -->
    <PromptDock :busy="busy" ref="dockRef" @send="onSend" />
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import ChatHeader from '../components/chat/ChatHeader.vue';
import Mascot from '../components/home/Mascot.vue';
import HeroGreeting from '../components/home/HeroGreeting.vue';
import PromptDock from '../components/home/PromptDock.vue';
import { useChatActions } from '../composables/useChatActions';
import { useViewMode } from '../composables/useViewMode';

defineProps<{ sidePanelOpen: boolean }>();
const emit = defineEmits<{
  'toggle-sidebar': [];
  'toggle-side-panel': [];
  'navigate': [target: string];
}>();

const busy = ref<boolean>(false);
const dockRef = ref<InstanceType<typeof PromptDock> | null>(null);
const viewMode = useViewMode();
const chatActions = useChatActions();

async function onSend(content: string) {
  // 1) 落地 user/assistant 流
  await chatActions.send(content, busy);
  // 2) 切到 chat 视图
  viewMode.goChat();
}
</script>