<template>
  <div class="group w-full animate-fade-in">
    <div v-for="r in renderItems" :key="r.key" class="flex flex-col">
      <ThinkingBlock
        v-if="r.kind === 'thinking'"
        :content="r.message.thinking"
        :is-currently-streaming="!r.message.done"
      />
      <ToolCallGroup
        v-else-if="r.kind === 'tool'"
        :tool-use="r.toolUse"
        :tool-result="r.toolResult"
        :is-currently-streaming="isStreaming(r.toolUse)"
      />
      <template v-else>
        <div
          v-if="!r.message.done && !r.message.error && (r.message.toolLabel || !r.message.content)"
          class="mb-1.5 inline-flex items-center gap-1.5 self-start rounded-md border border-border bg-surface-2 px-2 py-0.5 font-mono text-[11px] text-text-muted"
        >
          <span class="inline-block h-1.5 w-1.5 rounded-full bg-accent animate-cursor-blink" />
          {{ r.message.toolLabel ?? t('chat.thinking.status') }}
        </div>
        <div
          v-if="r.message.error"
          class="rounded-lg border-l-2 border-danger bg-transparent px-3.5 py-2 text-md leading-relaxed text-danger"
        >
          {{ r.message.error }}
        </div>
        <template v-else>
          <MarkdownContent :content="r.message.content" :done="r.message.done" />
          <TurnMeta :message="r.message" />
        </template>
        <ArtifactCardGroup
          v-if="r.message.artifacts?.length"
          :session-id="r.message.sessionId"
          :artifacts="r.message.artifacts"
        />
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { AssistantTurnItem, Message } from '../../composables/useMessages';
import { useMessages } from '../../composables/useMessages';
import ThinkingBlock from './ThinkingBlock.vue';
import MarkdownContent from './MarkdownContent.vue';
import TurnMeta from './TurnMeta.vue';
import ToolCallGroup from './tools/ToolCallGroup.vue';
import ArtifactCardGroup from './ArtifactCardGroup.vue';
import { t } from '../../services/i18n';

/**
 * 期望渲染序：assistant 的 thinking → 工具组 → assistant 的 content + meta。
 *
 * 事件流里 thinking_delta 先到、tool 事件在中间、text_delta 最后，但
 * startAssistantMessage 把 assistant 消息建在 bucket 最前（thinking 与
 * content 同属一条消息），tool 条目随后 append 到尾部。这里把每条
 * assistant 消息拆成 thinking 段 + content 段，工具组夹在中间，避免
 * 答案文本和 TurnMeta 排在工具下方。
 */
type RenderItem =
  | { kind: 'thinking'; message: Message; key: string }
  | { kind: 'tool'; toolUse: Message; toolResult: Message | null; key: string }
  | { kind: 'content'; message: Message; key: string };

const props = defineProps<{ items: AssistantTurnItem[] }>();

const messages = useMessages();

const renderItems = computed<RenderItem[]>(() => {
  // 三阶段收集：所有 thinking 段 → 所有工具组 → 所有 content 段，保证工具
  // 组渲染在思考与答案之间（事件流时序：thinking → tool → text）。
  const heads: RenderItem[] = [];
  const tools: RenderItem[] = [];
  const tails: RenderItem[] = [];
  let key = 0;
  for (const item of props.items) {
    if (item.type === 'tool_group') {
      tools.push({ kind: 'tool', toolUse: item.toolUse, toolResult: item.toolResult, key: `t-${key++}` });
      continue;
    }
    if (item.message.thinking) {
      heads.push({ kind: 'thinking', message: item.message, key: `th-${key++}` });
    }
    tails.push({ kind: 'content', message: item.message, key: `c-${key++}` });
  }
  return [...heads, ...tools, ...tails];
});

function isStreaming(msg: Message): boolean {
  return messages.streamingSessionIds.value.has(msg.sessionId);
}
</script>
