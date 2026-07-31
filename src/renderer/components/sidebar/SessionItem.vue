<template>
  <li>
    <button
      type="button"
      class="flex w-full items-center gap-2 rounded-md px-3 py-1.5 text-left text-[12.5px] transition-colors"
      :class="active
        ? 'bg-surface-raised text-text'
        : 'text-text-muted hover:bg-surface-hover hover:text-text'"
      @click="emit('select', session.id)"
    >
      <span
        v-if="running"
        class="inline-block h-1.5 w-1.5 shrink-0 animate-pulse rounded-full bg-accent"
        aria-label="运行中"
      />
      <Icon
        v-else
        name="message-square"
        :size="12"
        class="shrink-0 text-text-subtle"
      />
      <span class="flex-1 truncate">{{ session.title }}</span>
      <span
        v-if="unread"
        class="h-1.5 w-1.5 shrink-0 rounded-full bg-error"
        aria-label="未读"
      />
      <span class="shrink-0 font-mono text-[10px] text-text-subtle">{{ relTime }}</span>
    </button>
  </li>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { DarvinSession } from '../../../shared/darvin-api';
import Icon from '../common/Icon.vue';

const props = defineProps<{
  session: DarvinSession;
  active: boolean;
  running: boolean;
  unread: boolean;
}>();
const emit = defineEmits<{ select: [id: string] }>();

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
</script>