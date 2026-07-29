<template>
  <article class="group flex w-full animate-fade-in" :class="isUser ? 'justify-end' : 'justify-start'">
    <div class="max-w-[85%]">
      <!-- 工具调用 chip（仅 assistant 且 done=false 时显示） -->
      <div
        v-if="!isUser && !message.done && !isError"
        class="mb-1.5 inline-flex items-center gap-1.5 rounded-md border border-border bg-surface-2 px-2 py-0.5 font-mono text-[11px] text-text-muted"
      >
        <span class="inline-block h-1.5 w-1.5 rounded-full bg-accent animate-cursor-blink" />
        {{ toolLabel }}
      </div>

      <!-- 气泡 -->
      <div
        class="px-3.5 py-2 text-md leading-relaxed whitespace-pre-wrap"
        :class="bubbleClass"
      >
        <StreamingText
          v-if="!isError"
          :content="message.content"
          :done="message.done"
        />
        <span v-else>{{ message.error }}</span>
      </div>

      <!-- ID 调试信息（hover 显示） -->
      <p
        class="mt-1 font-mono text-[10px] text-text-subtle opacity-0 transition-opacity group-hover:opacity-100"
        :class="isUser ? 'text-right' : 'text-left'"
      >
        {{ message.id }}
      </p>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { Message } from '../../composables/useMessages';
import StreamingText from './StreamingText.vue';

const props = defineProps<{ message: Message }>();

const isUser = computed(() => props.message.role === 'user');
const isError = computed(() => !!props.message.error);

const toolLabel = computed(() => {
  // PR-3 接 tool name；现在先固定文案
  return props.message.toolLabel ?? 'Darvin · 思考中';
});

// 气泡视觉：
//   user:  右对齐 + 深底白字 + rounded-2xl 但右下角 4px（不对称）
//   assistant: 左对齐 + 浅底 + 完整圆角
//   error: 红色左边线 + 红色文字
const bubbleClass = computed(() => {
  if (isError.value) {
    return 'border-l-2 border-danger pl-3 text-danger bg-transparent';
  }
  if (isUser.value) {
    return 'rounded-2xl rounded-br-md bg-user-msg-bg text-user-msg border border-border';
  }
  return 'rounded-2xl bg-assistant-msg-bg text-assistant-msg';
});
</script>