<template>
  <div class="flex min-w-0 flex-col h-full">
    <ChatHeader
      :side-panel-open="sidePanelOpen"
      :title="t(titleKey)"
      @toggle-sidebar="emit('toggle-sidebar')"
      @toggle-side-panel="emit('toggle-side-panel')"
    />
    <div class="flex min-h-0 flex-1 flex-col items-center justify-center gap-4 px-6">
      <span class="grid h-14 w-14 place-items-center rounded-2xl bg-surface-raised text-text-subtle">
        <Icon :name="icon" :size="26" />
      </span>
      <div class="text-center">
        <h2 class="text-lg font-medium text-text">{{ t(titleKey) }}</h2>
        <p class="mx-auto mt-1.5 max-w-sm text-sm leading-relaxed text-text-muted">
          {{ t(descKey) }}
        </p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import ChatHeader from '../components/chat/ChatHeader.vue';
import Icon from '../components/common/Icon.vue';
import { t } from '../services/i18n';

defineProps<{
  sidePanelOpen: boolean;
  titleKey: string;
  descKey: string;
  icon: string;
}>();
const emit = defineEmits<{
  'toggle-sidebar': [];
  'toggle-side-panel': [];
}>();
</script>
