<template>
  <div class="group w-full animate-fade-in">
    <div v-for="item in items" :key="item.id" class="flex flex-col">
      <div
        v-if="!item.done && !item.error && (item.toolLabel || !item.content)"
        class="mb-1.5 inline-flex items-center gap-1.5 self-start rounded-md border border-border bg-surface-2 px-2 py-0.5 font-mono text-[11px] text-text-muted"
      >
        <span class="inline-block h-1.5 w-1.5 rounded-full bg-accent animate-cursor-blink" />
        {{ item.toolLabel ?? t('chat.thinking.status') }}
      </div>
      <div
        v-if="item.error"
        class="rounded-lg border-l-2 border-danger bg-transparent px-3.5 py-2 text-md leading-relaxed text-danger"
      >
        {{ item.error }}
      </div>
      <template v-else>
        <ThinkingBlock
          v-if="item.thinking"
          :content="item.thinking"
          :is-currently-streaming="!item.done"
        />
        <MarkdownContent :content="item.content" :done="item.done" />
        <TurnMeta :message="item" />
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import type { Message } from '../../composables/useMessages';
import ThinkingBlock from './ThinkingBlock.vue';
import MarkdownContent from './MarkdownContent.vue';
import TurnMeta from './TurnMeta.vue';
import { t } from '../../services/i18n';

defineProps<{ items: Message[] }>();
</script>
