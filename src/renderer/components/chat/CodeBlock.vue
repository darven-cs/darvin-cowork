<template>
  <div class="my-3 overflow-hidden rounded-lg border border-border bg-surface-2">
    <div class="flex items-center justify-between border-b border-border bg-surface-raised/60 px-3 py-1.5">
      <span class="font-mono text-xs text-text-muted">{{ langLabel }}</span>
      <button
        type="button"
        class="inline-flex items-center gap-1 rounded px-1 py-0.5 text-xs text-text-muted transition-colors hover:bg-surface-hover hover:text-text"
        :aria-label="t('chat.markdown.copy_code')"
        @click="handleCopy"
      >
        <Icon :name="copied ? 'check' : 'copy'" :size="14" />
        <span v-if="copied">{{ t('chat.markdown.copied') }}</span>
      </button>
    </div>
    <!-- eslint-disable-next-line vue/no-v-html --><!-- highlightedHtml 来自 shiki codeToHtml（可信库输出） -->
    <pre class="max-h-[480px] overflow-auto p-3"><code class="block font-mono text-code leading-relaxed" v-html="highlightedHtml" /></pre>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue';
import { getHighlighter, resolveLang } from '../../services/highlight';
import type { ShikiHighlighter } from '../../services/highlight';
import { useChatActions } from '../../composables/useChatActions';
import { useTheme } from '../../composables/useTheme';
import { t } from '../../services/i18n';

const props = defineProps<{ lang: string; code: string; done: boolean }>();

const { theme } = useTheme();
const actions = useChatActions();
const copied = ref(false);
const highlighter = ref<ShikiHighlighter | null>(null);
const highlightFailed = ref(false);

let copyTimer: ReturnType<typeof setTimeout> | null = null;
onBeforeUnmount(() => {
  if (copyTimer) clearTimeout(copyTimer);
});

getHighlighter()
  .then((h) => { highlighter.value = h; })
  .catch(() => { highlightFailed.value = true; });

const langLabel = computed(() => resolveLang(props.lang) ?? (props.lang || 'text'));

const highlightedHtml = computed(() => {
  if (!props.done || highlightFailed.value || !highlighter.value) {
    return escapeHtml(props.code);
  }
  const resolved = resolveLang(props.lang);
  if (!resolved) return escapeHtml(props.code);
  try {
    const themeName = theme.value === 'dark' ? 'github-dark' : 'github-light';
    const full = highlighter.value.codeToHtml(props.code, { lang: resolved, theme: themeName });
    return extractCodeInnerHtml(full);
  } catch {
    return escapeHtml(props.code);
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

async function handleCopy() {
  await actions.copy(props.code);
  copied.value = true;
  if (copyTimer) clearTimeout(copyTimer);
  copyTimer = setTimeout(() => { copied.value = false; }, 1500);
}
</script>
