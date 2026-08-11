<template>
  <component :is="subRenderer" v-if="subRenderer" :artifact="artifact" />
  <div v-else class="flex h-full w-full flex-col" data-testid="document-fallback">
    <pre
      v-if="content"
      class="min-h-0 flex-1 overflow-auto whitespace-pre-wrap break-words p-3 font-mono text-xs text-text"
    >{{ content }}</pre>
    <div v-else class="flex min-h-0 flex-1 flex-col items-center justify-center gap-3 p-4 text-center">
      <span class="flex h-12 w-12 items-center justify-center rounded-xl bg-surface-2 text-text-muted">
        <Icon name="file-text" :size="22" />
      </span>
      <span class="max-w-full truncate text-xs text-text-muted">{{ name }}</span>
      <button
        v-if="filePath"
        type="button"
        class="rounded border border-border px-2.5 py-1 text-xs text-text transition-colors hover:bg-surface-2"
        data-testid="document-open-app"
        @click="openWithApp"
      >
        {{ t('artifact.doc.openWithApp') }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { Component } from 'vue';
import Icon from '../../common/Icon.vue';
import type { Artifact } from '../../../composables/useArtifacts';
import DocxRenderer from './document/DocxRenderer.vue';
import PdfRenderer from './document/PdfRenderer.vue';
import SheetRenderer from './document/SheetRenderer.vue';
import PptxRenderer from './document/PptxRenderer.vue';
import { t } from '../../../services/i18n';

const props = defineProps<{ artifact: Artifact }>();

const name = computed(() => props.artifact.name ?? props.artifact.filePath ?? '');
const filePath = computed(() => props.artifact.filePath ?? '');
const content = computed(() => props.artifact.content.trim());

function ext(): string {
  const n = name.value;
  const idx = n.lastIndexOf('.');
  return idx >= 0 ? n.slice(idx + 1).toLowerCase() : '';
}

const subRenderer = computed<Component | null>(() => {
  switch (ext()) {
    case 'docx':
      return DocxRenderer;
    case 'pdf':
      return PdfRenderer;
    case 'xlsx':
    case 'xls':
      return SheetRenderer;
    case 'pptx':
      return PptxRenderer;
    default:
      return null;
  }
});

function openWithApp(): void {
  if (filePath.value) void window.darvin.openWorkspaceFile(filePath.value);
}
</script>
