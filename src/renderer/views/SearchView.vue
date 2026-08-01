<template>
  <div class="flex min-w-0 h-full flex-col">
    <ChatHeader
      :side-panel-open="sidePanelOpen"
      :title="t('sidebar.nav.search')"
      @toggle-sidebar="emit('toggle-sidebar')"
      @toggle-side-panel="emit('toggle-side-panel')"
    />
    <div class="flex min-h-0 flex-1 flex-col px-6 py-6">
      <div class="mx-auto w-full max-w-[760px]">
        <div
          class="flex items-center gap-2.5 rounded-xl border border-border bg-surface-2 px-3.5 py-2.5 transition-colors focus-within:border-border-strong"
        >
          <Icon name="search" :size="16" class="shrink-0 text-text-subtle" />
          <input
            v-model="query"
            type="text"
            :placeholder="t('search.placeholder')"
            class="w-full bg-transparent text-md text-text outline-none placeholder:text-text-subtle"
            data-testid="search-input"
          />
          <button
            v-if="query"
            type="button"
            class="shrink-0 text-text-subtle transition-colors hover:text-text"
            :aria-label="t('search.clear')"
            @click="query = ''"
          >
            <Icon name="x" :size="14" />
          </button>
        </div>
      </div>

      <div class="mx-auto mt-6 min-h-0 w-full max-w-[760px] flex-1 overflow-y-auto pb-6">
        <p v-if="!query.trim()" class="py-16 text-center text-sm text-text-muted">
          {{ t('search.hint') }}
        </p>
        <template v-else-if="hasResult">
          <section v-if="result.sessions.length" class="mb-7">
            <h2
              class="mb-2 text-[11px] font-medium uppercase tracking-wider text-text-subtle"
            >
              {{ t('search.section.sessions') }}
            </h2>
            <ul class="flex flex-col gap-0.5">
              <li v-for="s in result.sessions" :key="s.id">
                <button
                  type="button"
                  class="flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-sm text-text transition-colors hover:bg-surface-hover"
                  @click="openSession(s.id)"
                >
                  <Icon name="message-square" :size="13" class="shrink-0 text-text-subtle" />
                  <span class="truncate">{{ s.title }}</span>
                </button>
              </li>
            </ul>
          </section>
          <section v-if="result.messages.length" class="mb-7">
            <h2
              class="mb-2 text-[11px] font-medium uppercase tracking-wider text-text-subtle"
            >
              {{ t('search.section.messages') }}
            </h2>
            <ul class="flex flex-col gap-0.5">
              <li v-for="hit in result.messages" :key="hit.message.id">
                <button
                  type="button"
                  class="block w-full rounded-md px-3 py-2 text-left transition-colors hover:bg-surface-hover"
                  @click="openSession(hit.sessionId)"
                >
                  <span class="block truncate text-[13px] font-medium text-text">
                    {{ hit.sessionTitle }}
                  </span>
                  <span class="block truncate text-xs text-text-muted">
                    {{ roleLabel(darvinMessageRole(hit.message)) }} · {{ snippet(darvinMessageContent(hit.message)) }}
                  </span>
                </button>
              </li>
            </ul>
          </section>
        </template>
        <p v-else class="py-16 text-center text-sm text-text-muted">
          {{ t('search.empty') }}
        </p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import type { DarvinSearchSessionsResponse } from '../../shared/darvin-api';
import { darvinMessageRole, darvinMessageContent } from '../../shared/darvin-api';
import ChatHeader from '../components/chat/ChatHeader.vue';
import Icon from '../components/common/Icon.vue';
import { useSession } from '../composables/useSession';
import { useViewMode } from '../composables/useViewMode';
import { t } from '../services/i18n';

defineProps<{ sidePanelOpen: boolean }>();
const emit = defineEmits<{
  'toggle-sidebar': [];
  'toggle-side-panel': [];
  navigate: [target: string];
}>();

const session = useSession();
const viewMode = useViewMode();

const query = ref('');
const result = ref<DarvinSearchSessionsResponse>({ sessions: [], messages: [] });
let timer: number | undefined;

const hasResult = computed(
  () => result.value.sessions.length > 0 || result.value.messages.length > 0,
);

watch(query, (q) => {
  window.clearTimeout(timer);
  if (!q.trim()) {
    result.value = { sessions: [], messages: [] };
    return;
  }
  timer = window.setTimeout(async () => {
    try {
      result.value = await window.darvin.searchSessions(q.trim());
    } catch {
      result.value = { sessions: [], messages: [] };
    }
  }, 300);
});

onBeforeUnmount(() => window.clearTimeout(timer));

function openSession(id: string): void {
  session.draftMode.value = false;
  void session.switchSession(id);
  viewMode.goChat();
}

function roleLabel(role: 'user' | 'assistant'): string {
  return role === 'user' ? t('search.role_user') : 'Darvin';
}

function snippet(content: string): string {
  const c = content.replace(/\s+/g, ' ').trim();
  return c.length > 80 ? `${c.slice(0, 80)}…` : c;
}
</script>
