<template>
  <div class="flex h-full flex-col" data-testid="sheet-renderer">
    <div
      ref="scrollRef"
      class="min-h-0 flex-1 overflow-auto bg-surface"
      @scroll="onScroll"
    >
      <table class="border-collapse text-xs text-text">
        <thead class="sticky top-0 z-10 bg-surface-2 text-text-muted">
          <tr>
            <th
              v-for="(c, ci) in colCount"
              :key="ci"
              class="border border-border px-2 py-1 text-left font-medium"
              :style="{ minWidth: colWidth(ci) }"
            >
              {{ colLabel(ci) }}
            </th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="row in visibleRows"
            :key="row.index"
            class="absolute left-0 right-0"
            :style="{ top: `${row.index * ROW_H}px`, height: `${ROW_H}px` }"
          >
            <td
              v-for="(cell, ci) in row.cells"
              :key="ci"
              class="whitespace-nowrap border border-border px-2 py-1"
            >
              {{ cell }}
            </td>
          </tr>
          <tr :style="{ height: `${totalRows * ROW_H}px` }" />
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import * as XLSX from 'xlsx';
import type { Artifact } from '../../../../composables/useArtifacts';
import { artifactToBuffer } from '../../../../services/fileContent';
import { t } from '../../../../services/i18n';
import { showToast } from '../../../../services/toast';

const props = defineProps<{ artifact: Artifact }>();

const ROW_H = 28;
const OVERSCAN = 10;
const MAX_ROWS = 10000;

const scrollRef = ref<HTMLElement | null>(null);
const scrollTop = ref(0);
const viewportH = ref(600);
const rows = ref<string[][]>([]);
const colWidths = ref<number[]>([]);

const totalRows = computed(() => rows.value.length);
const colCount = computed(() => Math.max(1, ...rows.value.map((r) => r.length)));

const visibleRows = computed(() => {
  const start = Math.max(0, Math.floor(scrollTop.value / ROW_H) - OVERSCAN);
  const end = Math.min(totalRows.value, Math.ceil((scrollTop.value + viewportH.value) / ROW_H) + OVERSCAN);
  const out: { index: number; cells: string[] }[] = [];
  for (let i = start; i < end; i += 1) {
    out.push({ index: i, cells: rows.value[i] ?? [] });
  }
  return out;
});

function onScroll(e: Event): void {
  scrollTop.value = (e.target as HTMLElement).scrollTop;
}

function colLabel(i: number): string {
  let label = '';
  let n = i + 1;
  while (n > 0) {
    n -= 1;
    label = String.fromCharCode(65 + (n % 26)) + label;
    n = Math.floor(n / 26);
  }
  return label;
}

function colWidth(i: number): string {
  const wch = colWidths.value[i];
  return wch ? `${Math.max(wch, 6) * 7}px` : '80px';
}

onMounted(async () => {
  if (scrollRef.value) viewportH.value = scrollRef.value.clientHeight;
  try {
    const buf = await artifactToBuffer(props.artifact.filePath, props.artifact.content);
    const wb = XLSX.read(buf, { type: 'array', cellStyles: true });
    const ws = wb.Sheets[wb.SheetNames[0]];
    if (!ws) return;
    const aoa = XLSX.utils.sheet_to_json<string[]>(ws, { header: 1, raw: false, defval: '' });
    rows.value = aoa.slice(0, MAX_ROWS);
    colWidths.value = (ws['!cols'] ?? []).map((c) => c.wch ?? 0);
  } catch {
    showToast(t('artifact.doc.loadFailed'), 'error');
  }
});
</script>
