<template>
  <div>
    <template v-if="kind === 'edit' && editDiffs.length">
      <DiffView :diffs="editDiffs" />
    </template>

    <div v-if="text" class="mt-1.5">
      <!-- 大文本（>4KB）截断预览 -->
      <div v-if="collapsed.isTruncated && !expanded" class="overflow-hidden rounded-md border border-border bg-surface-2">
        <div class="flex flex-wrap items-center justify-between gap-2 border-b border-border px-3 py-1.5 text-xs text-text-muted">
          <span>{{ t('tool.large_preview') }} · {{ collapsed.sizeLabel }}</span>
          <button type="button" class="text-accent hover:text-accent-hover" @click="expanded = true">
            {{ t('tool.expand') }}
          </button>
        </div>
        <pre class="max-h-[240px] overflow-auto whitespace-pre-wrap break-words p-3 font-mono text-xs" :class="terminal ? 'text-zinc-300' : 'text-text'">{{ collapsed.preview }}</pre>
      </div>
      <pre
        v-else
        class="max-h-[360px] overflow-auto whitespace-pre-wrap break-words font-mono text-xs"
        :class="terminal ? (isError ? 'text-red-400' : 'text-zinc-200') : (isError ? 'text-danger' : 'text-text')"
      >{{ text }}</pre>
      <button
        v-if="collapsed.isTruncated && expanded"
        type="button"
        class="mt-1 text-xs text-text-muted transition-colors hover:text-text"
        @click="expanded = false"
      >
        {{ t('tool.collapse') }}
      </button>
    </div>

    <!-- 工具失败且无输出细节：红色 + 兜底文案 -->
    <div v-if="isError && !text.trim()" class="text-xs" :class="terminal ? 'text-red-400' : 'text-danger'">
      {{ t('tool.error.noDetail') }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';
import type { Message } from '../../../composables/useMessages';
import {
  extractDiffFromToolInput,
  getToolKind,
  getToolResultCollapsedDisplay,
  getToolResultText,
} from '../../../services/toolDisplay';
import DiffView from './DiffView.vue';
import { t } from '../../../services/i18n';

const props = withDefaults(defineProps<{
  toolUse: Message;
  toolResult: Message | null;
  /** 终端模式（bash）：黑底上用亮色文字 */
  terminal?: boolean;
}>(), {
  terminal: false,
});

const expanded = ref(false);

const tool = computed(() => props.toolUse.tool ?? props.toolResult?.tool ?? 'Tool');
const kind = computed(() => getToolKind(tool.value));

// 孤立 tool_result（缺 tool_use）时 resultMsg 落到 toolUse 自身
const resultMsg = computed(() => props.toolResult ?? (props.toolUse.kind === 'tool_result' ? props.toolUse : null));
const isError = computed(() => Boolean(resultMsg.value?.isError));
const output = computed(() => resultMsg.value?.output);
const collapsed = computed(() => getToolResultCollapsedDisplay(output.value));

const text = computed(() => {
  const raw = getToolResultText(output.value);
  if (!isError.value) return raw;
  return getToolErrorText(output.value) || raw;
});

// Edit 的 diff 来自 input（old_string → new_string），result 仅 { success: true }
const editDiffs = computed(() => extractDiffFromToolInput(tool.value, props.toolUse.input));

function getToolErrorText(output: unknown): string {
  if (output && typeof output === 'object') {
    const rec = output as Record<string, unknown>;
    const err = rec.error ?? rec.stderr;
    if (typeof err === 'string' && err.trim()) return err;
    if (err && typeof err === 'object') return JSON.stringify(err, null, 2);
  }
  return '';
}
</script>
