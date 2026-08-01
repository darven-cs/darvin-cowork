<template>
  <div class="flex h-full flex-col" data-testid="file-list-view">
    <div class="flex shrink-0 items-center gap-1.5 border-b border-border px-2 py-1.5">
      <Icon name="search" :size="14" class="shrink-0 text-text-subtle" />
      <input
        v-model="query"
        type="text"
        :placeholder="t('artifact.fileList.search')"
        class="h-6 min-w-0 flex-1 bg-transparent text-xs text-text outline-none placeholder:text-text-subtle"
        data-testid="file-list-search"
      />
    </div>

    <div
      v-if="loading"
      class="flex flex-1 items-center justify-center px-6 text-center text-sm text-text-muted"
      data-testid="file-list-loading"
    >
      {{ t('artifact.fileList.loading') }}
    </div>
    <div
      v-else-if="groups.length === 0"
      class="flex flex-1 flex-col items-center justify-center px-6 text-center"
      data-testid="file-list-empty"
    >
      <p class="text-sm text-text-muted">
        {{ files.length === 0 ? t('artifact.fileList.empty') : t('artifact.fileList.noResult') }}
      </p>
    </div>
    <div v-else class="min-h-0 flex-1 overflow-y-auto py-1">
      <div v-for="group in groups" :key="group.kind" class="mb-1">
        <div class="px-3 py-1 text-[10px] font-medium uppercase tracking-wide text-text-subtle">
          {{ group.kind }}
        </div>
        <button
          v-for="file in group.files"
          :key="file.relativePath"
          type="button"
          class="group flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs transition-colors"
          :class="isClickable(file) ? 'hover:bg-surface-hover' : 'cursor-default'"
          :data-testid="'file-list-row'"
          @click="openFile(file)"
        >
          <span class="flex h-5 w-5 shrink-0 items-center justify-center text-text-muted">
            <Icon :name="iconForKind(file.kind)" :size="14" />
          </span>
          <span class="min-w-0 flex-1">
            <span class="block truncate text-text">{{ file.name }}</span>
            <span class="block truncate text-[10px] text-text-subtle">{{ shortenPath(file.relativePath) }}</span>
          </span>
          <span class="shrink-0 rounded border border-border px-1 py-px text-[10px] uppercase text-text-subtle">
            {{ file.kind }}
          </span>
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import type { DarvinArtifactKind, DarvinWorkspaceFileInfo } from '../../../shared/darvin-api';
import { useArtifacts } from '../../composables/useArtifacts';
import { t } from '../../services/i18n';
import { showToast } from '../../services/toast';

const props = defineProps<{ sessionId: string }>();

const artifacts = useArtifacts();

const files = ref<DarvinWorkspaceFileInfo[]>([]);
const loading = ref(true);
const query = ref('');

/** 走本地预览服务的 kind（合成 filePath artifact）。 */
const FILE_PATH_KINDS = new Set<DarvinArtifactKind>(['html', 'svg', 'image', 'video']);
/** 读文本内容后 inline 打开的 kind。 */
const TEXT_KINDS = new Set<DarvinArtifactKind>(['markdown', 'text', 'code']);

const filtered = computed(() => {
  const q = query.value.trim().toLowerCase();
  if (!q) return files.value;
  return files.value.filter(
    (f) => f.name.toLowerCase().includes(q) || f.relativePath.toLowerCase().includes(q),
  );
});

const KIND_ORDER: DarvinArtifactKind[] = [
  'html', 'svg', 'image', 'video', 'mermaid', 'markdown', 'text', 'code', 'document', 'local-service',
];

const groups = computed<Array<{ kind: string; files: DarvinWorkspaceFileInfo[] }>>(() => {
  const byKind = new Map<string, DarvinWorkspaceFileInfo[]>();
  for (const f of filtered.value) {
    const arr = byKind.get(f.kind) ?? [];
    arr.push(f);
    byKind.set(f.kind, arr);
  }
  const order = KIND_ORDER.filter((k) => byKind.has(k));
  for (const k of byKind.keys()) {
    if (!order.includes(k)) order.push(k);
  }
  return order.map((k) => ({ kind: k, files: byKind.get(k) ?? [] }));
});

function iconForKind(kind: string): string {
  if (kind === 'code') return 'terminal';
  return 'file-text';
}

function shortenPath(rel: string): string {
  const parts = rel.split('/');
  if (parts.length <= 2) return rel;
  return `…/${parts.slice(-2).join('/')}`;
}

function isClickable(f: DarvinWorkspaceFileInfo): boolean {
  return FILE_PATH_KINDS.has(f.kind) || TEXT_KINDS.has(f.kind);
}

async function openFile(f: DarvinWorkspaceFileInfo): Promise<void> {
  if (FILE_PATH_KINDS.has(f.kind)) {
    artifacts.addArtifact(props.sessionId, {
      id: `file:${f.relativePath}`,
      kind: f.kind,
      name: f.name,
      content: '',
      filePath: f.relativePath,
      createdAt: Date.now(),
    });
    return;
  }
  if (!TEXT_KINDS.has(f.kind)) {
    showToast(t('artifact.render.unsupported'), 'error');
    return;
  }
  const r = await window.darvin.readWorkspaceFile(f.relativePath);
  if (r.success && r.content !== undefined) {
    artifacts.addArtifact(props.sessionId, {
      id: `file:${f.relativePath}`,
      kind: f.kind,
      name: f.name,
      content: r.content,
      createdAt: Date.now(),
    });
  } else if (r.error === 'too_large') {
    showToast(t('artifact.fileList.tooLarge'), 'error');
  } else {
    showToast(t('artifact.fileList.readError'), 'error');
  }
}

onMounted(async () => {
  try {
    const r = await window.darvin.listWorkspaceFiles();
    files.value = r.files;
  } catch {
    files.value = [];
  } finally {
    loading.value = false;
  }
});
</script>
