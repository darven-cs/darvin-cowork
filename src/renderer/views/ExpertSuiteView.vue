<template>
  <div class="flex min-w-0 flex-col h-full">
    <ChatHeader
      :side-panel-open="sidePanelOpen"
      @toggle-sidebar="emit('toggle-sidebar')"
      @toggle-side-panel="emit('toggle-side-panel')"
    />
    <div class="flex-1 flex items-center justify-center px-6">
      <div class="text-center">
        <Icon name="layout" :size="36" class="mx-auto mb-3 text-text-muted" />
        <h2 class="font-display text-xl font-medium text-text">专家套件</h2>
        <p class="mt-1 font-sans text-sm text-text-muted">PR-4 实现</p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import ChatHeader from '../components/chat/ChatHeader.vue';
import Icon from '../components/common/Icon.vue';

defineProps<{ sidePanelOpen: boolean }>();
const emit = defineEmits<{ 'toggle-sidebar': []; 'toggle-side-panel': [] }>();
</script>