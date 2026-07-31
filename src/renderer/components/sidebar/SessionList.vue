<template>
  <nav class="flex-1 overflow-y-auto px-2 pb-2">
    <ul v-if="sessions.length > 0" class="flex flex-col gap-0.5">
      <SessionItem
        v-for="s in sessions"
        :key="s.id"
        :session="s"
        :active="s.id === currentId"
        :running="runningSessionIds.has(s.id)"
        :unread="unreadSessionIds.has(s.id)"
        @select="emit('select', $event)"
        @rename="(id, title) => emit('rename', id, title)"
        @delete="emit('delete', $event)"
      />
    </ul>
    <p v-else class="px-3 py-4 text-xs text-text-muted">{{ t('sidebar.session.empty') }}</p>
  </nav>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { DarvinSession } from '../../../shared/darvin-api';
import SessionItem from './SessionItem.vue';
import { useMessages } from '../../composables/useMessages';
import { t } from '../../services/i18n';

defineProps<{ sessions: DarvinSession[]; currentId: string }>();
const emit = defineEmits<{
  select: [id: string];
  rename: [id: string, title: string];
  delete: [id: string];
}>();

const messages = useMessages();
const runningSessionIds = computed(() => messages.streamingSessionIds.value);
const unreadSessionIds = computed(() => messages.unreadSessionIds.value);
</script>
