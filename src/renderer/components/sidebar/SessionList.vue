<template>
  <nav class="flex-1 overflow-y-auto px-2 pb-2">
    <ul v-if="orderedSessions.length > 0" class="flex flex-col gap-0.5">
      <SessionItem
        v-for="s in orderedSessions"
        :key="s.id"
        :session="s"
        :active="s.id === currentId"
        :status="statusOf(s.id)"
        :pinned="pinnedIds.has(s.id)"
        :unread="unreadSessionIds.has(s.id)"
        @select="emit('select', $event)"
        @rename="(id, title) => emit('rename', id, title)"
        @delete="emit('delete', $event)"
        @pin="session.togglePin"
      />
    </ul>
    <p v-else class="px-3 py-4 text-xs text-text-muted">{{ t('sidebar.session.empty') }}</p>
  </nav>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { DarvinSession } from '../../../shared/darvin-api';
import SessionItem from './SessionItem.vue';
import { useMessages, type SessionActivityStatus } from '../../composables/useMessages';
import { useSession } from '../../composables/useSession';
import { t } from '../../services/i18n';

const props = defineProps<{ sessions: DarvinSession[]; currentId: string }>();
const emit = defineEmits<{
  select: [id: string];
  rename: [id: string, title: string];
  delete: [id: string];
}>();

const messages = useMessages();
const session = useSession();
const runningSessionIds = computed(() => messages.streamingSessionIds.value);
const unreadSessionIds = computed(() => messages.unreadSessionIds.value);
const pinnedIds = computed(() => session.pinnedSessionIds.value);
const statusBySession = computed(() => messages.sessionStatusBySessionId.value);

/** pinned 置顶；其余按主进程返回顺序。 */
const orderedSessions = computed(() => {
  const pinned = props.sessions.filter((s) => pinnedIds.value.has(s.id));
  const rest = props.sessions.filter((s) => !pinnedIds.value.has(s.id));
  return [...pinned, ...rest];
});

function statusOf(id: string): SessionActivityStatus {
  // 流式状态实时性最高，覆盖持久化的 completed/error
  if (runningSessionIds.value.has(id)) return 'running';
  return statusBySession.value[id] ?? 'idle';
}
</script>
