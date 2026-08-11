<template>
  <div class="markdown-content min-w-0" @click.capture="onLinkClick">
    <div
      v-if="truncated && !expanded"
      class="overflow-hidden rounded-lg border border-border bg-surface-raised/60"
    >
      <div class="flex flex-wrap items-center justify-between gap-2 border-b border-border px-3 py-2 text-xs text-text-muted">
        <span>{{ t('chat.markdown.large_preview') }} · {{ sizeLabel }}</span>
        <button type="button" class="text-accent hover:text-accent-hover" @click="expanded = true">
          {{ t('chat.markdown.expand') }}
        </button>
      </div>
      <pre class="max-h-[420px] overflow-auto whitespace-pre-wrap break-words p-3 font-mono text-xs text-text">{{ preview }}</pre>
    </div>
    <template v-else>
      <template v-for="(seg, i) in segments" :key="i">
        <!-- eslint-disable-next-line vue/no-v-html --><!-- seg.html 经 DOMPurify 净化 -->
        <span v-if="seg.type === 'html'" class="contents" v-html="sanitize(seg.html)" />
        <CodeBlock v-else :lang="seg.lang" :code="seg.code" :done="done" />
      </template>
      <div v-if="truncated && expanded" class="mt-1 flex justify-end">
        <button type="button" class="text-xs text-text-muted hover:text-text" @click="expanded = false">
          {{ t('chat.markdown.collapse') }}
        </button>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';
import dompurify from 'dompurify';
import 'katex/dist/katex.min.css';
import CodeBlock from './CodeBlock.vue';
import { useArtifacts } from '../../composables/useArtifacts';
import { useSession } from '../../composables/useSession';
import {
  formatContentSize,
  getLargeMarkdownPreview,
  renderMarkdownSegments,
  shouldUseLargeMarkdownPreview,
} from '../../services/markdown';
import { t } from '../../services/i18n';
import { showToast } from '../../services/toast';

const props = defineProps<{ content: string; done: boolean }>();

const artifacts = useArtifacts();
const session = useSession();

const expanded = ref(false);

const truncated = computed(() => shouldUseLargeMarkdownPreview(props.content));
const preview = computed(() => getLargeMarkdownPreview(props.content));
const sizeLabel = computed(() => formatContentSize(props.content.length));
const segments = computed(() => renderMarkdownSegments(props.content));

/** 链接点击拦截：http(s) → 面板 Browser tab；其他协议 → 系统浏览器。锚点跳转放行。 */
function onLinkClick(e: MouseEvent): void {
  const target = e.target as Element | null;
  const anchor = target?.closest?.('a');
  if (!anchor) return;
  const href = anchor.getAttribute('href');
  if (!href || href.trim() === '' || href.startsWith('#')) return;
  e.preventDefault();
  const sid = session.activeSessionId.value;
  if (/^https?:\/\//i.test(href)) {
    if (sid) {
      artifacts.openBrowser(sid, href);
    } else {
      void window.darvin.openExternal(href);
    }
    return;
  }
  void window.darvin.openExternal(href).then((r) => {
    if (!r.success) showToast(t('artifact.link.openFailed'), 'error');
  });
}

function sanitize(html: string): string {
  return dompurify.sanitize(html, { USE_PROFILES: { html: true, mathMl: true } });
}
</script>
