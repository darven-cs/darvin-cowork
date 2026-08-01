<template>
  <div v-if="error" class="p-4 text-sm text-danger">
    <p class="font-medium">{{ t('artifact.render.mermaidError') }}</p>
    <pre class="mt-2 whitespace-pre-wrap font-mono text-xs">{{ error }}</pre>
  </div>
  <div v-else class="h-full w-full overflow-auto p-3">
    <!-- eslint-disable-next-line vue/no-v-html --><!-- svg 经 mermaid strict + DOMPurify 净化 -->
    <div class="flex min-h-full items-center justify-center" v-html="svg" />
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue';
import mermaid from 'mermaid';
import dompurify from 'dompurify';
import type { Artifact } from '../../../composables/useArtifacts';
import { useTheme } from '../../../composables/useTheme';
import { t } from '../../../services/i18n';

const props = defineProps<{ artifact: Artifact }>();

const { theme } = useTheme();
const svg = ref('');
const error = ref<string | null>(null);
let cancelled = false;

async function renderDiagram(): Promise<void> {
  error.value = null;
  svg.value = '';
  // securityLevel: 'strict' 让 mermaid 自己净化 HTML 注入；渲染结果再过一遍 DOMPurify
  mermaid.initialize({ startOnLoad: false, securityLevel: 'strict', theme: theme.value === 'dark' ? 'dark' : 'default' });
  const id = `mermaid-${props.artifact.id.replace(/[^a-zA-Z0-9]/g, '') || 'x'}`;
  const container = document.createElement('div');
  container.style.position = 'absolute';
  container.style.visibility = 'hidden';
  document.body.appendChild(container);
  try {
    await mermaid.parse(props.artifact.content);
    const { svg: rendered } = await mermaid.render(id, props.artifact.content, container);
    if (!cancelled) {
      svg.value = dompurify.sanitize(rendered, { USE_PROFILES: { svg: true, svgFilters: true } });
    }
  } catch (e) {
    if (!cancelled) error.value = e instanceof Error ? e.message : String(e);
  } finally {
    container.remove();
  }
}

watch(() => props.artifact.content, renderDiagram, { immediate: true });

onBeforeUnmount(() => {
  cancelled = true;
});
</script>
