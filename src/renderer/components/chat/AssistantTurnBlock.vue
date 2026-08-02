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
          v-if="!r.continuation && !r.message.done && !r.message.error && (r.message.toolLabel || !r.message.content)"
          class="mb-1.5 inline-flex items-center gap-1.5 self-start rounded-md border border-border bg-surface-2 px-2 py-0.5 font-mono text-[11px] text-text-muted"
        >
          <span class="inline-block h-1.5 w-1.5 rounded-full bg-accent animate-cursor-blink" />
          {{ r.message.toolLabel ?? t('chat.thinking.status') }}
        </div>
        <div
          v-if="!r.continuation && r.message.error"
          class="rounded-lg border-l-2 border-danger bg-transparent px-3.5 py-2 text-md leading-relaxed text-danger"
        >
          {{ r.message.error }}
        </div>
        <template v-else>
          <MarkdownContent :content="r.message.content" :done="r.message.done" />
          <TurnMeta v-if="!r.continuation" :message="r.message" />
        </template>
        <ArtifactCardGroup
          v-if="!r.continuation && r.message.artifacts?.length"
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
import { interleaveToolSegments, useMessages } from '../../composables/useMessages';
import ThinkingBlock from './ThinkingBlock.vue';
import MarkdownContent from './MarkdownContent.vue';
import TurnMeta from './TurnMeta.vue';
import ToolCallGroup from './tools/ToolCallGroup.vue';
import ArtifactCardGroup from './ArtifactCardGroup.vue';
import { t } from '../../services/i18n';

/**
 * 渲染序 = turn 内 assistantItems 的真实顺序。live 场景 Go 用单 messageId 把
 * 整个 run 的文本累积进一条消息，interleaveToolSegments 按工具断点切段，
 * 让「答 → 工具 → 答 → 工具」交错展示；后续文本段（continuation）只渲染
 * 内容，不重复 TurnMeta / thinking / artifacts。
 */
type RenderItem =
  | { kind: 'thinking'; message: Message; key: string }
  | { kind: 'tool'; toolUse: Message; toolResult: Message | null; key: string }
  | { kind: 'content'; message: Message; continuation: boolean; key: string };

const props = defineProps<{ items: AssistantTurnItem[] }>();

const messages = useMessages();

const renderItems = computed<RenderItem[]>(() => {
  const out: RenderItem[] = [];
  let key = 0;
  for (const seg of interleaveToolSegments(props.items)) {
    if (seg.kind === 'tool_group') {
      out.push({ kind: 'tool', toolUse: seg.toolUse, toolResult: seg.toolResult, key: `t-${key++}` });
      continue;
    }
    const msg = seg.message;
    if (!seg.continuation && msg.thinking) {
      out.push({ kind: 'thinking', message: msg, key: `th-${key++}` });
    }
    out.push({ kind: 'content', message: msg, continuation: seg.continuation, key: `c-${key++}` });
  }
  return out;
});

function isStreaming(msg: Message): boolean {
  return messages.streamingSessionIds.value.has(msg.sessionId);
}
</script>
