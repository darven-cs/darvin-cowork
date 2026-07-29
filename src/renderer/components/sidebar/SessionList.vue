<template>
  <nav class="flex-1 overflow-y-auto px-2 pb-2">
    <ul v-if="sessions.length > 0" class="flex flex-col gap-0.5">
      <SessionItem
        v-for="s in sessions"
        :key="s.id"
        :session="s"
        :active="s.id === currentId"
        @select="emit('select', $event)"
      />
    </ul>
    <p v-else class="px-3 py-4 text-xs text-text-muted">暂无会话</p>
  </nav>
</template>

<script setup lang="ts">
import type { DarvinSession } from '../../../shared/darvin-api';
import SessionItem from './SessionItem.vue';

defineProps<{ sessions: DarvinSession[]; currentId: string }>();
const emit = defineEmits<{ select: [id: string] }>();
</script>
