<script setup lang="ts">
import { computed } from 'vue';
import { t } from '../../services/i18n';
import type { DarvinSchedule, DarvinScheduleRun } from '../../../shared/darvin-api';

const props = defineProps<{
  scheduleId: string;
  schedule: DarvinSchedule | null;
  runs: DarvinScheduleRun[];
}>();

const emit = defineEmits<{
  abort: [scheduleId: string, runId: string];
  close: [];
}>();

const running = computed(() => props.runs.find((r) => r.status === 'running') ?? null);

function fmtTime(ts: number | undefined): string {
  if (ts === undefined) return '—';
  return new Date(ts).toLocaleString();
}

function durationMs(start: number | undefined | null, end: number | undefined | null): number {
  if (start === undefined || start === null || end === undefined || end === null) return 0;
  return Math.max(0, end - start);
}

function fmtDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
  return `${(ms / 60_000).toFixed(1)}m`;
}
</script>

<template>
  <div class="rounded-lg border border-border bg-surface-raised p-4">
    <header class="mb-3 flex items-center justify-between">
      <h3 class="text-base font-medium">
        {{ schedule?.name ?? scheduleId }}
        <span v-if="schedule && schedule.consecutiveErrors >= 5" class="ml-2 rounded bg-danger px-2 py-0.5 text-xs text-text-inverse">
          {{ t('schedule.badge.failure') }}
        </span>
      </h3>
      <button class="text-text-muted hover:text-text" type="button" @click="emit('close')">×</button>
    </header>
    <dl class="mb-4 grid grid-cols-2 gap-2 text-sm text-text-muted">
      <div><dt class="inline font-medium">{{ t('schedule.card.last') }}: </dt><dd class="inline">{{ fmtTime(schedule?.lastFiredAt) }}</dd></div>
      <div><dt class="inline font-medium">{{ t('schedule.card.next') }}: </dt><dd class="inline">{{ fmtTime(schedule?.nextFireAt) }}</dd></div>
    </dl>
    <h4 class="mb-2 text-sm font-medium">{{ t('schedule.history.title') }}</h4>
    <table v-if="runs.length > 0" class="w-full text-sm">
      <thead class="border-b border-border text-text-muted">
        <tr>
          <th class="py-1 text-left">{{ t('schedule.history.col.triggeredAt') }}</th>
          <th class="py-1 text-left">{{ t('schedule.history.col.trigger') }}</th>
          <th class="py-1 text-left">{{ t('schedule.history.col.status') }}</th>
          <th class="py-1 text-left">{{ t('schedule.history.col.duration') }}</th>
          <th class="py-1 text-left">{{ t('schedule.history.col.error') }}</th>
          <th class="py-1 text-right"></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="run in runs" :key="run.id" class="border-b border-border">
          <td class="py-1">{{ fmtTime(run.triggeredAt) }}</td>
          <td class="py-1">{{ t(`schedule.trigger.${run.trigger}`) }}</td>
          <td class="py-1">{{ t(`schedule.status.${run.status}`) }}</td>
          <td class="py-1">{{ fmtDuration(durationMs(run.startedAt, run.endedAt)) }}</td>
          <td class="py-1 truncate max-w-xs">{{ run.error ?? '—' }}</td>
          <td class="py-1 text-right">
            <button
              v-if="run.status === 'running' && running?.id === run.id"
              type="button"
              class="rounded bg-danger px-2 py-0.5 text-xs text-text-inverse"
              @click="emit('abort', scheduleId, run.id)"
            >
              {{ t('schedule.card.actions.abort') }}
            </button>
          </td>
        </tr>
      </tbody>
    </table>
    <p v-else class="text-sm text-text-muted">{{ t('schedule.history.empty') }}</p>
  </div>
</template>