<template>
  <button
    v-for="run in subagents"
    :key="run.id"
    type="button"
    class="inline-flex h-7 max-w-full items-center gap-1.5 rounded-full border border-border bg-surface px-2.5 text-xs text-text-muted transition-colors hover:border-primary hover:text-text"
    :aria-label="t('chat.subagent.chip.open')"
    :data-testid="'subagent-chip-' + run.id"
    @click="open(run)"
  >
    <span class="h-1.5 w-1.5 shrink-0 rounded-full" :class="dotClass(run.status)" />
    <Icon name="subagents" :size="12" class="shrink-0" />
    <span class="max-w-[160px] truncate">{{ name(run) }}</span>
    <span v-if="run.status === 'running'" class="shrink-0 text-text-subtle">
      {{ t('artifact.subagents.section.running') }}
    </span>
  </button>
</template>

<script setup lang="ts">
import type { SubagentRun } from '../../../shared/darvin-api';
import { useArtifacts, ArtifactSpecialTab } from '../../composables/useArtifacts';
import { useSession } from '../../composables/useSession';
import { useSubagents } from '../../composables/useSubagents';
import { t } from '../../services/i18n';
import Icon from '../common/Icon.vue';

defineProps<{ subagents: SubagentRun[]; toolCallId: string }>();

const artifacts = useArtifacts();
const session = useSession();
const subagents$ = useSubagents();

function name(run: SubagentRun): string {
  return subagents$.getSubagentDisplayName(run);
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

function open(run: SubagentRun): void {
  const sid = session.activeSessionId.value;
  if (!sid) return;
  artifacts.activateTab(sid, ArtifactSpecialTab.Subagents);
  subagents$.selectRun(run.id);
}
</script>
