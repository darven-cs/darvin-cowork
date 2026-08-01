<template>
  <div class="my-1.5 overflow-hidden rounded-lg border border-border bg-surface-1">
    <ToolCallHeader
      :name="displayName"
      :kind="kind"
      :is-streaming="isCurrentlyStreaming"
      :has-result="resultMsg !== null"
      :is-error="isError"
      :expanded="expanded"
      @toggle="expanded = !expanded"
    />

    <div v-if="expanded" class="border-t border-border">
      <!-- Bash：仿终端（三色圆点 + 黑底 + $ 前缀） -->
      <div v-if="kind === 'bash'" class="bg-[#14161a] px-3 py-2.5 font-mono text-xs text-zinc-300">
        <div class="mb-2 flex gap-1.5">
          <span class="h-2.5 w-2.5 rounded-full bg-[#ff5f57]" />
          <span class="h-2.5 w-2.5 rounded-full bg-[#febc2e]" />
          <span class="h-2.5 w-2.5 rounded-full bg-[#28c840]" />
        </div>
        <div v-if="inputDisplay" class="whitespace-pre-wrap break-words">
          <span class="text-green-400">$ </span>{{ inputDisplay }}
        </div>
        <ToolCallResult v-if="hasOutput || isError" :tool-use="toolUse" :tool-result="toolResult" terminal />
      </div>

      <!-- 其他 kind：Input / Result 两分区 -->
      <template v-else>
        <div v-if="inputDisplay" class="border-b border-border bg-surface-2 px-3 py-2">
          <div class="mb-1 font-mono text-[10px] uppercase tracking-wider text-text-subtle">{{ t('tool.input') }}</div>
          <ToolCallInput :tool-use="toolUse" />
        </div>
        <div v-if="hasOutput || isError || kind === 'edit'" class="px-3 py-2">
          <div class="mb-1 font-mono text-[10px] uppercase tracking-wider text-text-subtle">{{ t('tool.result') }}</div>
          <ToolCallResult :tool-use="toolUse" :tool-result="toolResult" />
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';
import type { Message } from '../../../composables/useMessages';
import {
  formatToolInput,
  getToolDisplayName,
  getToolKind,
  getToolResultText,
} from '../../../services/toolDisplay';
import ToolCallHeader from './ToolCallHeader.vue';
import ToolCallInput from './ToolCallInput.vue';
import ToolCallResult from './ToolCallResult.vue';
import { t } from '../../../services/i18n';

const props = defineProps<{
  toolUse: Message;
  toolResult: Message | null;
  isCurrentlyStreaming: boolean;
}>();

// 默认折叠；组件以 toolUse.id 为 key 复用，expanded 状态跨流式更新保留
const expanded = ref(false);

const tool = computed(() => props.toolUse.tool ?? props.toolResult?.tool ?? 'Tool');
const kind = computed(() => getToolKind(tool.value));
const displayName = computed(() => getToolDisplayName(tool.value));

// 孤立 tool_result（缺 tool_use）时 resultMsg 落到 toolUse 自身
const resultMsg = computed(() => props.toolResult ?? (props.toolUse.kind === 'tool_result' ? props.toolUse : null));
const isError = computed(() => Boolean(resultMsg.value?.isError));

// tool_use 才有 input；tool_result 条目没有 input，跳过 Input 分区
const inputDisplay = computed(() =>
  props.toolUse.kind === 'tool_use' && props.toolUse.input !== undefined
    ? formatToolInput(tool.value, props.toolUse.input)
    : '',
);
const hasOutput = computed(() => getToolResultText(resultMsg.value?.output) !== '');
</script>
