<template>
  <li class="group relative flex items-center">
    <button
      v-if="!editing"
      type="button"
      class="flex min-w-0 flex-1 items-center gap-2 rounded-md px-3 py-1.5 text-left text-[12.5px] transition-colors"
      :class="active
        ? 'bg-surface-raised text-text'
        : 'text-text-muted hover:bg-surface-hover hover:text-text'"
      @click="emit('select', session.id)"
    >
      <Icon
        v-if="status === 'running'"
        name="circle-dot"
        :size="12"
        class="shrink-0 animate-pulse text-accent"
        :aria-label="t('sidebar.session.status.running')"
      />
      <Icon
        v-else-if="status === 'error'"
        name="alert-circle"
        :size="12"
        class="shrink-0 text-danger"
        :aria-label="t('sidebar.session.status.error')"
      />
      <Icon
        v-else-if="status === 'completed'"
        name="check"
        :size="12"
        class="shrink-0 text-text-subtle"
        :aria-label="t('sidebar.session.status.completed')"
      />
      <Icon
        v-else
        name="message-square"
        :size="12"
        class="shrink-0 text-text-subtle"
      />
      <span class="flex-1 truncate">{{ session.title }}</span>
      <Icon
        v-if="pinned"
        name="pin"
        :size="11"
        class="shrink-0 text-accent"
        :aria-label="t('sidebar.session.status.pinned')"
      />
      <span
        v-if="unread"
        class="h-1.5 w-1.5 shrink-0 rounded-full bg-error"
        :aria-label="t('sidebar.session.unread')"
      />
      <span class="shrink-0 font-mono text-[10px] text-text-subtle">{{ relTime }}</span>
    </button>

    <input
      v-else
      ref="inputRef"
      v-model="draft"
      :placeholder="t('sidebar.session.rename_placeholder')"
      class="w-full min-w-0 rounded-md border border-border bg-surface-2 px-3 py-1.5 text-[12.5px] text-text outline-none"
      data-testid="session-rename-input"
      @keydown.enter="commitRename"
      @keydown.esc="cancelRename"
      @blur="commitRename"
    />

    <div
      v-if="!editing"
      class="absolute right-1 top-1/2 z-10 -translate-y-1/2"
    >
      <Dropdown @update:open="onMenuOpenChange">
        <template #trigger>
          <span
            role="button"
            class="grid h-6 w-6 place-items-center rounded-md text-text-subtle transition-colors hover:bg-surface-hover hover:text-text"
            :class="active ? 'opacity-100' : 'opacity-0 group-hover:opacity-100'"
            :aria-label="t('sidebar.session.more_aria')"
          >
            <Icon name="more" :size="14" />
          </span>
        </template>
        <template #menu="{ close }">
          <div
            v-if="confirming"
            class="min-w-[180px] rounded-lg border border-border bg-surface p-2 shadow-md"
          >
            <p class="px-1 pb-2 text-xs text-text-muted">
              {{ t('sidebar.session.delete_confirm') }}
            </p>
            <div class="flex justify-end gap-1">
              <button
                type="button"
                class="rounded-md px-2 py-1 text-xs text-text-muted transition-colors hover:bg-surface-hover"
                @click="confirming = false"
              >
                {{ t('sidebar.session.cancel') }}
              </button>
              <button
                type="button"
                class="rounded-md bg-danger px-2 py-1 text-xs text-white transition-colors hover:opacity-90"
                @click="confirmDelete(close)"
              >
                {{ t('sidebar.session.delete_confirm_btn') }}
              </button>
            </div>
          </div>
          <div v-else class="min-w-[160px] rounded-lg border border-border bg-surface p-1 shadow-md">
            <button
              type="button"
              class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[12.5px] text-text transition-colors hover:bg-surface-hover"
              @click="togglePin(close)"
            >
              <Icon name="pin" :size="13" class="text-text-subtle" />
              {{ pinned ? t('sidebar.session.unpin') : t('sidebar.session.pin') }}
            </button>
            <button
              type="button"
              class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[12.5px] text-text transition-colors hover:bg-surface-hover"
              @click="startRename(close)"
            >
              <Icon name="edit" :size="13" class="text-text-subtle" />
              {{ t('sidebar.session.rename') }}
            </button>
            <button
              type="button"
              class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[12.5px] text-danger transition-colors hover:bg-surface-hover"
              @click="confirming = true"
            >
              <Icon name="trash" :size="13" />
              {{ t('sidebar.session.delete') }}
            </button>
          </div>
        </template>
      </Dropdown>
    </div>
  </li>
</template>

<script setup lang="ts">
import { computed, nextTick, ref } from 'vue';
import type { DarvinSession } from '../../../shared/darvin-api';
import type { SessionActivityStatus } from '../../composables/useMessages';
import Icon from '../common/Icon.vue';
import Dropdown from '../common/Dropdown.vue';
import { t } from '../../services/i18n';

const props = defineProps<{
  session: DarvinSession;
  active: boolean;
  status: SessionActivityStatus;
  pinned: boolean;
  unread: boolean;
}>();
const emit = defineEmits<{
  select: [id: string];
  rename: [id: string, title: string];
  delete: [id: string];
  pin: [id: string];
}>();

const editing = ref(false);
const confirming = ref(false);
const draft = ref('');
const inputRef = ref<HTMLInputElement | null>(null);

const relTime = computed(() => {
  const diff = Date.now() - props.session.updatedAt;
  const m = Math.floor(diff / 60_000);
  if (m < 1) return '现在';
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h`;
  const d = Math.floor(h / 24);
  if (d === 1) return '昨';
  if (d < 7) return `${d}d`;
  return `${Math.floor(d / 7)}w`;
});

function onMenuOpenChange(open: boolean): void {
  if (open) confirming.value = false;
}

function togglePin(close: () => void): void {
  close();
  emit('pin', props.session.id);
}

function startRename(close: () => void): void {
  close();
  draft.value = props.session.title;
  editing.value = true;
  void nextTick(() => inputRef.value?.select());
}

function commitRename(): void {
  if (!editing.value) return;
  const title = draft.value.trim();
  editing.value = false;
  if (title !== '' && title !== props.session.title) {
    emit('rename', props.session.id, title);
  }
}

function cancelRename(): void {
  editing.value = false;
}

function confirmDelete(close: () => void): void {
  close();
  emit('delete', props.session.id);
}
</script>
