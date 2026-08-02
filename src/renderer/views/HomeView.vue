<template>
  <div class="flex min-w-0 flex-col h-full">
    <ChatHeader
      :side-panel-open="sidePanelOpen"
      @toggle-sidebar="emit('toggle-sidebar')"
      @toggle-side-panel="emit('toggle-side-panel')"
    />

    <div class="min-h-0 flex-1 overflow-y-auto px-6 animate-fade-in">
      <div class="mx-auto flex h-full w-full max-w-[760px] flex-col items-center justify-center gap-6 py-10">
        <Mascot :size="96" />
        <HeroGreeting />
        <div class="w-full pt-2">
          <QuickActions @select="onTileSelect" />
        </div>
      </div>
    </div>

    <PromptDock :busy="busy" ref="dockRef" @send="onSend" />
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import ChatHeader from '../components/chat/ChatHeader.vue';
import Mascot from '../components/home/Mascot.vue';
import HeroGreeting from '../components/home/HeroGreeting.vue';
import QuickActions from '../components/home/QuickActions.vue';
import PromptDock from '../components/home/PromptDock.vue';
import { useChatActions } from '../composables/useChatActions';
import { useViewMode } from '../composables/useViewMode';
import { t } from '../services/i18n';

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
  await chatActions.send(content, busy);
  viewMode.goChat();
}

function onTileSelect(id: 'qa-slide' | 'qa-data' | 'qa-doc' | 'qa-web') {
  const TEMPLATE_KEYS: Record<typeof id, string> = {
    'qa-slide': 'home.example.slide',
    'qa-data':  'home.example.data',
    'qa-doc':   'home.example.doc',
    'qa-web':   'home.example.web',
  };
  onSend(t(TEMPLATE_KEYS[id]));
}
</script>