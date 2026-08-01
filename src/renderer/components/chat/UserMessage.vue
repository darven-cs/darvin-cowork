<template>
  <div class="flex w-full animate-fade-in justify-end">
    <div class="max-w-[85%]">
      <div v-if="imageAttachments.length" class="mb-1.5 flex flex-wrap justify-end gap-1.5" :aria-label="t('chat.attachments.images')">
        <img
          v-for="att in imageAttachments"
          :key="att.id"
          :src="att.src"
          :alt="att.name"
          class="h-14 w-14 rounded-md border border-border object-cover"
        />
      </div>
      <div
        class="rounded-2xl rounded-br-md border border-border bg-user-msg-bg px-3.5 py-2 text-md leading-relaxed whitespace-pre-wrap text-user-msg"
      >
        {{ message.content }}
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { Message } from '../../composables/useMessages';
import { t } from '../../services/i18n';

const props = defineProps<{ message: Message }>();

const imageAttachments = computed(
  () => props.message.attachments?.filter((a) => a.kind === 'image') ?? [],
);
</script>
