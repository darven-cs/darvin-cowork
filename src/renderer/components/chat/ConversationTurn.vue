<template>
  <div class="flex flex-col gap-3">
    <UserMessage v-if="turn.userMessage" :message="turn.userMessage" />
    <AssistantTurnBlock v-if="turn.assistantItems.length" :items="turn.assistantItems" />
  </div>
</template>

<script setup lang="ts">
import type { ConversationTurn } from '../../composables/useMessages';
import UserMessage from './UserMessage.vue';
import AssistantTurnBlock from './AssistantTurnBlock.vue';

defineProps<{ turn: ConversationTurn }>();
</script>
