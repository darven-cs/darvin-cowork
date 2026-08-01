<template>
  <div class="px-6 pb-5 pt-2">
    <ImportedFilesBar class="mb-1.5" />
    <div
      class="mx-auto max-w-[760px] rounded-xl border border-border bg-surface-2 transition-colors focus-within:border-border-strong"
    >
      <textarea
        ref="textareaRef"
        v-model="text"
        :placeholder="busy ? t('chat.placeholder.busy') : t('home.prompt.placeholder')"
        :disabled="busy"
        rows="1"
        class="w-full resize-none bg-transparent px-4 pt-3 font-sans text-[14.5px] leading-relaxed text-text outline-none placeholder:text-text-subtle disabled:opacity-50"
        data-testid="composer-textarea"
        @input="autoGrow"
        @keydown="onKeydown"
      />
      <ComposerToolbar :can-send="canSend" @send="emitSend" @suite="onSuite" @mic="onMic" />
      <ComposerContextRow />
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
import ComposerToolbar from './ComposerToolbar.vue';
import ComposerContextRow from './ComposerContextRow.vue';
import ImportedFilesBar from './ImportedFilesBar.vue';

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

function onSuite() {}
function onMic() {}
</script>
