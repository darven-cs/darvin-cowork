<template>
  <article class="group flex w-full" :class="isUser ? 'justify-end' : 'justify-start'">
    <div
      class="max-w-[85%]"
      :class="isError ? 'border-l-2 border-danger pl-3' : ''"
    >
      <p
        class="mb-1.5 font-mono text-[11px] uppercase tracking-[0.08em]"
        :class="isError ? 'text-danger' : 'text-text-subtle'"
      >
        {{ isUser ? 'You' : 'Darvin' }}
      </p>
      <div
        class="text-md leading-relaxed whitespace-pre-wrap"
        :class="isError ? 'text-danger' : isUser ? 'text-user-msg' : 'text-assistant-msg'"
      >
        <StreamingText
          v-if="!isError"
          :content="message.content"
          :done="message.done"
        />
        <span v-else>{{ message.error }}</span>
      </div>
      <p
        class="mt-1.5 font-mono text-[11px] text-text-subtle opacity-0 transition-opacity group-hover:opacity-100"
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
</script>
