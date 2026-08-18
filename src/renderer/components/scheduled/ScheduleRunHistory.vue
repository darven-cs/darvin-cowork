<script setup lang="ts">
import { computed, onMounted, watch } from 'vue';
import { t } from '../../services/i18n';
import type { DarvinSchedule, DarvinScheduleRun } from '../../../shared/darvin-api';
import { useSchedules } from '../../composables/useSchedules';

const props = defineProps<{
  workspaceId: string | null;
  schedules: DarvinSchedule[];
  runsBySchedule: Record<string, DarvinScheduleRun[]>;
}>();

const { loadAllRuns } = useSchedules();

const allRuns = computed(() => {
  const flat: Array<{ schedule: DarvinSchedule; run: DarvinScheduleRun }> = [];
  for (const s of props.schedules) {
    const runs = props.runsBySchedule[s.id] ?? [];
    for (const r of runs) {
      flat.push({ schedule: s, run: r });
    }
  }
  flat.sort((a, b) => b.run.triggeredAt - a.run.triggeredAt);
  return flat;
});

onMounted(() => {
  if (props.workspaceId) void loadAllRuns(props.workspaceId);
});

watch(() => props.workspaceId, (id) => {
  if (id) void loadAllRuns(id);
});

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
    <h2 class="mb-3 text-lg font-medium">{{ t('schedule.history.title') }}</h2>
    <table v-if="allRuns.length > 0" class="w-full text-sm">
      <thead class="border-b border-border text-text-muted">
        <tr>
          <th class="py-1 text-left">Schedule</th>
          <th class="py-1 text-left">{{ t('schedule.history.col.triggeredAt') }}</th>
          <th class="py-1 text-left">{{ t('schedule.history.col.trigger') }}</th>
          <th class="py-1 text-left">{{ t('schedule.history.col.status') }}</th>
          <th class="py-1 text-left">{{ t('schedule.history.col.duration') }}</th>
          <th class="py-1 text-left">{{ t('schedule.history.col.error') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="{ schedule, run } in allRuns" :key="run.id" class="border-b border-border">
          <td class="py-1">{{ schedule.name }}</td>
          <td class="py-1">{{ fmtTime(run.triggeredAt) }}</td>
          <td class="py-1">{{ t(`schedule.trigger.${run.trigger}`) }}</td>
          <td class="py-1">{{ t(`schedule.status.${run.status}`) }}</td>
          <td class="py-1">{{ fmtDuration(durationMs(run.startedAt, run.endedAt)) }}</td>
          <td class="py-1 truncate max-w-xs">{{ run.error ?? '—' }}</td>
        </tr>
      </tbody>
    </table>
    <p v-else class="text-sm text-text-muted">{{ t('schedule.history.empty') }}</p>
  </div>
</template>