<template>
  <div class="flex items-center gap-3 py-1" role="separator" :aria-label="text">
    <div class="h-px flex-1 border-t border-dashed border-border" />
    <span class="inline-flex items-center gap-1.5 font-mono text-[11px] text-text-subtle">
      <Icon name="refresh" :size="12" />
      {{ text }}
    </span>
    <div class="h-px flex-1 border-t border-dashed border-border" />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { CompactionMarker } from '../../composables/useMessages';
import { t } from '../../services/i18n';

const props = defineProps<{ marker: CompactionMarker }>();

const reasonLabel = computed(() =>
  props.marker.reason === 'manual'
    ? t('chat.compaction.reason.manual')
    : t('chat.compaction.reason.auto'),
);

const timeLabel = computed(() =>
  new Intl.DateTimeFormat(undefined, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(props.marker.createdAt)),
);

const text = computed(() =>
  t('chat.compaction.divider')
    .replace('{reason}', reasonLabel.value)
    .replace('{time}', timeLabel.value),
);
</script>
