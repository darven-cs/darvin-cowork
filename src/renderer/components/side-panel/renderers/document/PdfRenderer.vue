<template>
  <div class="flex h-full flex-col" data-testid="pdf-renderer">
    <div v-if="loading" class="flex flex-1 items-center justify-center text-sm text-text-muted">
      {{ t('artifact.doc.loading') }}
    </div>
    <div v-else ref="scrollRef" class="min-h-0 flex-1 overflow-auto bg-surface">
      <div class="flex flex-col items-center p-3">
        <canvas
          v-for="n in pageCount"
          :key="n"
          :ref="(el) => setCanvas(el, n)"
          class="mb-3 block bg-white shadow-md"
        />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue';
import { getDocument, GlobalWorkerOptions, type PDFDocumentProxy } from 'pdfjs-dist';
import pdfWorkerUrl from 'pdfjs-dist/build/pdf.worker.mjs?url';
import type { Artifact } from '../../../../composables/useArtifacts';
import { artifactToBuffer } from '../../../../services/fileContent';
import { t } from '../../../../services/i18n';
import { showToast } from '../../../../services/toast';

GlobalWorkerOptions.workerSrc = pdfWorkerUrl;

const props = defineProps<{ artifact: Artifact }>();

const MAX_PAGES = 100;

const scrollRef = ref<HTMLElement | null>(null);
const pageCount = ref(0);
const loading = ref(true);
const renderWidth = ref(0);
const canvases: (HTMLCanvasElement | null)[] = [];
let pdfDoc: PDFDocumentProxy | null = null;
let resizeObserver: ResizeObserver | null = null;

function setCanvas(el: unknown, n: number): void {
  canvases[n - 1] = el as HTMLCanvasElement | null;
}

function measure(): void {
  const el = scrollRef.value;
  if (el) renderWidth.value = Math.max(120, el.clientWidth - 24);
}

async function renderAll(): Promise<void> {
  if (!pdfDoc || !renderWidth.value) return;
  for (let i = 1; i <= pageCount.value; i += 1) {
    const canvas = canvases[i - 1];
    if (!canvas) continue;
    const page = await pdfDoc.getPage(i);
    const base = page.getViewport({ scale: 1 });
    const scale = renderWidth.value / base.width;
    const viewport = page.getViewport({ scale });
    canvas.width = viewport.width;
    canvas.height = viewport.height;
    const ctx = canvas.getContext('2d');
    if (ctx) {
      await page.render({ canvasContext: ctx, viewport }).promise;
    }
    page.cleanup();
  }
}

async function load(): Promise<void> {
  try {
    const buf = await artifactToBuffer(props.artifact.filePath, props.artifact.content);
    pdfDoc = await getDocument({ data: buf }).promise;
    pageCount.value = Math.min(pdfDoc.numPages, MAX_PAGES);
  } catch {
    showToast(t('artifact.doc.loadFailed'), 'error');
  }
}

onMounted(async () => {
  await load();
  loading.value = false;
  // scrollRef 在 loading=false 后才渲染，必须等 nextTick 再量宽 + 渲染。
  await nextTick();
  measure();
  await renderAll();
  if (scrollRef.value) {
    resizeObserver = new ResizeObserver(() => {
      measure();
      void renderAll();
    });
    resizeObserver.observe(scrollRef.value);
  }
});

onBeforeUnmount(() => {
  resizeObserver?.disconnect();
  void pdfDoc?.destroy();
});
</script>
