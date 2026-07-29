<template>
  <div class="px-6 pb-6 pt-2">
    <div
      class="mx-auto flex w-full max-w-[720px] flex-col gap-3 rounded-xl border border-border bg-surface-2 px-4 py-3 transition-colors focus-within:border-border-strong"
    >
      <!-- textarea + send -->
      <div class="flex items-end gap-2">
        <textarea
          ref="textareaRef"
          v-model="text"
          :placeholder="t('home.prompt.placeholder')"
          :disabled="busy"
          rows="1"
          class="flex-1 resize-none bg-transparent font-sans text-[14.5px] leading-relaxed text-text outline-none placeholder:text-text-subtle disabled:opacity-50"
          @input="autoGrow"
          @keydown="onKeydown"
        />
        <SendButton :can-send="canSend" @click="emitSend" />
      </div>

      <!-- 工具条（输入框下方） -->
      <PromptToolbar
        @grid="onGrid"
        @mic="onMic"
      />
    </div>
    <p class="mx-auto mt-1.5 w-full max-w-[720px] text-center font-sans text-[11px] text-text-subtle">
      {{ t('home.disclaimer') }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref } from 'vue';
import { t } from '../../services/i18n';
import PromptToolbar from './PromptToolbar.vue';
import SendButton from './SendButton.vue';

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

// PR-3 / PR-4 stubs
function onGrid() { /* TODO: open ExpertSuite */ }
function onMic() { /* TODO: start voice input */ }
</script>