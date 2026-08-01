<template>
  <div v-if="rows.length" class="overflow-hidden rounded-md border border-border bg-surface-2">
    <div
      v-for="(row, di) in rows"
      :key="di"
      class="max-h-80 overflow-auto"
      :class="{ 'border-b border-border': di < rows.length - 1 }"
    >
      <div
        v-if="row.filePath"
        class="border-b border-border bg-surface-raised/60 px-3 py-1 font-mono text-[11px] text-text-muted"
      >
        {{ row.filePath }}
      </div>
      <table class="w-full border-collapse font-mono text-xs leading-relaxed">
        <tbody>
          <tr v-for="(line, li) in row.lines" :key="li" :class="lineClass(line.type)">
            <td class="w-8 select-none px-2 text-right text-text-subtle/60">
              {{ line.oldLineNo ?? '' }}
            </td>
            <td class="w-8 select-none px-2 text-right text-text-subtle/60">
              {{ line.newLineNo ?? '' }}
            </td>
            <td class="w-4 select-none px-1 text-center">{{ prefix(line.type) }}</td>
            <td class="whitespace-pre-wrap break-all px-2">{{ line.text || '\u00A0' }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { DiffData, DiffLineType } from '../../../services/toolDisplay';
import { computeDiffLines } from '../../../services/toolDisplay';

const props = defineProps<{ diffs: DiffData[] }>();

const rows = computed(() =>
  props.diffs.map((d) => ({ filePath: d.filePath ?? '', lines: computeDiffLines(d.oldStr, d.newStr) })),
);

function lineClass(type: DiffLineType): string {
  switch (type) {
    case 'added':
      return 'bg-green-500/10 text-green-700 dark:text-green-400';
    case 'removed':
      return 'bg-red-500/10 text-red-700 dark:text-red-400';
    default:
      return 'text-text';
  }
}

function prefix(type: DiffLineType): string {
  return type === 'added' ? '+' : type === 'removed' ? '-' : ' ';
}
</script>
