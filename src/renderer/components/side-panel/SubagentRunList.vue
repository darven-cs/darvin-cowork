<template>
  <div data-testid="subagent-run-list">
    <section v-for="group in groups" :key="group.status">
      <div class="sticky top-0 z-10 flex h-9 items-center border-b border-border bg-bg px-4">
        <h3 class="text-xs font-medium text-text-subtle">{{ group.label }} ({{ group.runs.length }})</h3>
      </div>
      <button
        v-for="run in group.runs"
        :key="run.id"
        type="button"
        class="group flex w-full items-center gap-3 px-4 py-2.5 text-left transition-colors hover:bg-surface-hover"
        :data-testid="'subagent-run-row-' + run.id"
        @click="$emit('select', run.id)"
      >
        <span
          class="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-primary-soft text-xs font-semibold text-primary"
        >
          {{ initial(run) }}
        </span>
        <span class="min-w-0 flex-1">
          <span class="flex min-w-0 items-center gap-1.5">
            <span class="truncate text-sm font-medium text-text">{{ name(run) }}</span>
            <span class="h-1.5 w-1.5 shrink-0 rounded-full" :class="dotClass(run.status)" />
          </span>
          <span class="mt-0.5 block truncate text-[10px] text-text-subtle">{{ sub(run) }}</span>
        </span>
        <span class="shrink-0 text-[10px] text-text-subtle">{{ right(run) }}</span>
      </button>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { SubagentRun } from '../../../shared/darvin-api';
import { useSubagents } from '../../composables/useSubagents';
import { t } from '../../services/i18n';

defineEmits<{ select: [id: string] }>();
const props = defineProps<{ runs: SubagentRun[] }>();

const subagents = useSubagents();

interface RunGroup {
  status: SubagentRun['status'];
  label: string;
  runs: SubagentRun[];
}

const STATUS_ORDER: SubagentRun['status'][] = ['running', 'done', 'error', 'aborted', 'timeout'];

const groups = computed<RunGroup[]>(() => {
  const byStatus = new Map<SubagentRun['status'], SubagentRun[]>();
  for (const run of props.runs) {
    const list = byStatus.get(run.status) ?? [];
    list.push(run);
    byStatus.set(run.status, list);
  }
  const out: RunGroup[] = [];
  for (const status of STATUS_ORDER) {
    const list = byStatus.get(status) ?? [];
    if (list.length === 0) continue;
    list.sort((a, b) => b.startedAt - a.startedAt);
    out.push({ status, label: label(status), runs: list });
  }
  return out;
});

function label(status: SubagentRun['status']): string {
  switch (status) {
    case 'running': return t('artifact.subagents.section.running');
    case 'done': return t('artifact.subagents.section.done');
    case 'error': return t('artifact.subagents.section.error');
    case 'aborted': return t('artifact.subagents.section.aborted');
    case 'timeout': return t('artifact.subagents.section.aborted');
    default: return status;
  }
}

function name(run: SubagentRun): string {
  return subagents.getSubagentDisplayName(run);
}

function initial(run: SubagentRun): string {
  return subagents.getSubagentDisplayInitial(run);
}

function dotClass(status: SubagentRun['status']): string {
  switch (status) {
    case 'running': return 'animate-pulse bg-accent-blue';
    case 'error':
    case 'timeout': return 'bg-danger';
    case 'aborted': return 'bg-text-subtle';
    default: return 'bg-success';
  }
}

function sub(run: SubagentRun): string {
  if (run.status === 'error' && run.errorMsg) return run.errorMsg;
  if (run.prompt?.trim()) return run.prompt;
  return run.id;
}

function right(run: SubagentRun): string {
  if (run.status === 'running') return t('artifact.subagents.section.running');
  if (run.durationMs > 0) return t('artifact.subagents.row.elapsed', { ms: run.durationMs });
  return '';
}
</script>
