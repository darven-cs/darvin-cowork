<script setup lang="ts">
import { t } from '../../services/i18n';
import type { DarvinSchedule } from '../../../shared/darvin-api';

defineProps<{ schedules: DarvinSchedule[] }>();

const emit = defineEmits<{
  'run-now': [scheduleId: string, name: string];
  toggle: [scheduleId: string, enabled: boolean];
  delete: [scheduleId: string, name: string];
  edit: [schedule: DarvinSchedule];
  'open-detail': [scheduleId: string];
}>();

function fmtTime(ts: number | undefined): string {
  if (ts === undefined) return '—';
  return new Date(ts).toLocaleString();
}
</script>

<template>
  <div class="flex flex-col gap-2">
    <header class="flex items-center justify-between">
      <h2 class="text-lg font-medium">{{ t('schedule.nav.title') }}</h2>
    </header>
    <ul v-if="schedules.length > 0" class="flex flex-col gap-2">
      <li
        v-for="s in schedules"
        :key="s.id"
        class="flex items-center justify-between rounded-lg border border-border bg-surface-raised px-4 py-2"
      >
        <button class="flex-1 text-left" type="button" @click="emit('open-detail', s.id)">
          <div class="font-medium">
            {{ s.name }}
            <span class="ml-2 rounded bg-surface-2 px-1.5 py-0.5 text-xs">{{ t(`schedule.kind.${s.kind}`) }}</span>
            <span
              v-if="s.consecutiveErrors >= 5"
              class="ml-2 rounded bg-danger px-1.5 py-0.5 text-xs text-text-inverse"
            >
              {{ t('schedule.badge.failure') }}
            </span>
          </div>
          <div class="text-xs text-text-muted">
            {{ t('schedule.card.next', { time: fmtTime(s.nextFireAt) }) }} ·
            {{ t('schedule.card.last', { time: fmtTime(s.lastFiredAt) }) }}
          </div>
        </button>
        <div class="flex gap-2">
          <button
            type="button"
            class="rounded bg-primary px-2 py-1 text-xs text-text-inverse"
            :disabled="!s.enabled"
            @click="emit('run-now', s.id, s.name)"
          >
            {{ t('schedule.card.actions.runNow') }}
          </button>
          <button
            type="button"
            class="rounded border border-border px-2 py-1 text-xs"
            @click="emit('toggle', s.id, !s.enabled)"
          >
            {{ s.enabled ? '⏸' : '▶' }}
          </button>
          <button
            type="button"
            class="rounded border border-border px-2 py-1 text-xs"
            @click="emit('edit', s)"
          >
            {{ t('schedule.card.actions.edit') }}
          </button>
          <button
            type="button"
            class="rounded border border-danger px-2 py-1 text-xs text-danger"
            @click="emit('delete', s.id, s.name)"
          >
            {{ t('schedule.card.actions.delete') }}
          </button>
        </div>
      </li>
    </ul>
    <p v-else class="text-sm text-text-muted">{{ t('schedule.list.empty') }}</p>
  </div>
</template>