<template>
  <div class="px-6 pb-5 pt-2">
    <div
      class="mx-auto flex max-w-[760px] items-end gap-2 rounded-xl border border-border bg-surface-2 px-4 py-3 transition-colors focus-within:border-border-strong"
    >
      <textarea
        ref="textareaRef"
        v-model="text"
        :placeholder="busy ? 'Darvin 正在思考…' : '给 Darvin 发送消息…'"
        :disabled="busy"
        rows="1"
        class="flex-1 resize-none bg-transparent font-sans text-[14.5px] leading-relaxed text-text outline-none placeholder:text-text-subtle disabled:opacity-50"
        data-testid="composer-textarea"
        @input="autoGrow"
        @keydown="onKeydown"
      />
      <button
        type="button"
        :disabled="!canSend"
        class="flex h-9 w-9 shrink-0 items-center justify-center rounded-full transition-all"
        :class="canSend ? 'bg-accent text-white hover:bg-accent-hover hover:scale-[1.04]' : 'bg-border cursor-not-allowed'"
        :aria-label="t('chat.send')"
        data-testid="composer-send"
        @click="emitSend"
      >
        <Icon name="arrow-up" :size="16" />
      </button>
    </div>
    <p
      v-if="text.length > 50"
      class="mx-auto mt-1.5 max-w-[760px] text-right font-mono text-[11px] text-text-subtle"
    >
      {{ text.length }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref } from 'vue';
import { t } from '../../services/i18n';
import Icon from '../common/Icon.vue';

const props = defineProps<{ busy: boolean }>();
const emit = defineEmits<{ send: [content: string] }>();

const text = ref<string>('');
const textareaRef = ref<HTMLTextAreaElement | null>(null);

const canSend = computed(() => !props.busy && text.value.trim().length > 0);

function emitSend() {
  if (!canSend.value) return;
  const content = text.value;
  text.value = '';
  resetHeight();
  emit('send', content);
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault();
    emitSend();
    return;
  }
  if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
    e.preventDefault();
    emitSend();
  }
}

function autoGrow() {
  const el = textareaRef.value;
  if (!el) return;
  el.style.height = 'auto';
  const max = 200;
  el.style.height = `${Math.min(el.scrollHeight, max)}px`;
}

function resetHeight() {
  const el = textareaRef.value;
  if (el) el.style.height = 'auto';
}

function focus() {
  nextTick(() => textareaRef.value?.focus());
}

defineExpose({ focus });
</script>