<template>
  <div class="h-full w-full overflow-auto bg-surface-2">
    <!-- eslint-disable-next-line vue/no-v-html --><!-- highlightedHtml 来自 shiki codeToHtml（可信库输出） -->
    <pre class="p-3"><code class="block font-mono text-xs leading-relaxed" v-html="highlightedHtml" /></pre>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';
import { getHighlighter, resolveLang } from '../../../services/highlight';
import type { ShikiHighlighter } from '../../../services/highlight';
import { useTheme } from '../../../composables/useTheme';
import type { Artifact } from '../../../composables/useArtifacts';

const props = defineProps<{ artifact: Artifact }>();

const { theme } = useTheme();
const highlighter = ref<ShikiHighlighter | null>(null);
const highlightFailed = ref(false);

getHighlighter()
  .then((h) => { highlighter.value = h; })
  .catch(() => { highlightFailed.value = true; });

/** 从 artifact name（文件名或语言名）解析 shiki 语言；未知返回 null。 */
function resolveArtifactLang(name?: string): string | null {
  if (!name) return null;
  const candidate = name.includes('.') ? name.split('.').pop()! : name;
  return resolveLang(candidate);
}

const highlightedHtml = computed(() => {
  if (highlightFailed.value || !highlighter.value) return escapeHtml(props.artifact.content);
  const resolved = resolveArtifactLang(props.artifact.name);
  if (!resolved) return escapeHtml(props.artifact.content);
  try {
    const themeName = theme.value === 'dark' ? 'github-dark' : 'github-light';
    const full = highlighter.value.codeToHtml(props.artifact.content, { lang: resolved, theme: themeName });
    return extractCodeInnerHtml(full);
  } catch {
    return escapeHtml(props.artifact.content);
  }
});

function extractCodeInnerHtml(full: string): string {
  const div = document.createElement('div');
  div.innerHTML = full;
  const codeEl = div.querySelector('code');
  return codeEl ? codeEl.innerHTML : full;
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}
</script>
