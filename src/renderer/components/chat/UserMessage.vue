<template>
  <div class="flex w-full animate-fade-in justify-end">
    <div class="max-w-[85%]">
      <div v-if="imageRefs.length" class="mb-1.5 flex flex-wrap justify-end gap-1.5" :aria-label="t('chat.attachments.images')">
        <img
          v-for="img in imageRefs"
          :key="img.path"
          :src="img.dataUrl"
          :alt="img.name"
          class="h-14 w-14 rounded-md border border-border object-cover"
        />
      </div>
      <div v-if="fileRefs.length" class="mb-1.5 flex flex-wrap justify-end gap-1.5">
        <span
          v-for="f in fileRefs"
          :key="f.path"
          class="inline-flex max-w-[14rem] items-center gap-1.5 rounded-md border border-border bg-surface-2 px-2 py-1 text-xs text-text"
          :title="f.name"
        >
          <Icon name="file-text" :size="12" />
          <span class="truncate">{{ f.name }}</span>
          <span class="shrink-0 text-text-subtle">{{ formatBytes(f.size) }}</span>
        </span>
      </div>
      <!-- 兼容老 wire 的 imageAttachments（base64 / dataURL） -->
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
import { formatBytes } from '../../composables/useImportedFiles';
import { t } from '../../services/i18n';
import Icon from '../common/Icon.vue';

const props = defineProps<{ message: Message }>();

const imageRefs = computed(() => props.message.imageRefs ?? []);
const fileRefs = computed(() => props.message.attachmentRefs ?? []);

const imageAttachments = computed(
  () => props.message.attachments?.filter((a) => a.kind === 'image') ?? [],
);
</script>
