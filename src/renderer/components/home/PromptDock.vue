<template>
  <div class="px-6 pb-6 pt-2">
    <div
      class="mx-auto w-full max-w-[720px] rounded-xl border border-border bg-surface-2 transition-colors focus-within:border-border-strong"
      :class="{ 'composer-running': props.running }"
    >
      <textarea
        ref="textareaRef"
        v-model="text"
        :placeholder="t('home.prompt.placeholder')"
        :disabled="busy"
        rows="1"
        class="w-full resize-none bg-transparent px-4 pt-3 font-sans text-[14.5px] leading-relaxed text-text outline-none placeholder:text-text-subtle disabled:opacity-50"
        @input="autoGrow"
        @keydown="onKeydown"
      />
      <ComposerToolbar :can-send="canSend" @send="emitSend" @suite="onSuite" @mic="onMic" />
      <ComposerContextRow ref="ctxRowRef" />
    </div>
    <p class="mx-auto mt-1.5 w-full max-w-[720px] text-center font-sans text-[11px] text-text-subtle">
      {{ t('home.disclaimer') }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref } from 'vue';
import { t } from '../../services/i18n';
import { useWorkspaces } from '../../composables/useWorkspaces';
import ComposerToolbar from '../chat/ComposerToolbar.vue';
import ComposerContextRow from '../chat/ComposerContextRow.vue';

const props = defineProps<{ busy: boolean; running?: boolean }>();
const emit = defineEmits<{ send: [content: string] }>();

const text = ref<string>('');
const textareaRef = ref<HTMLTextAreaElement | null>(null);
const ctxRowRef = ref<InstanceType<typeof ComposerContextRow> | null>(null);
const workspaces = useWorkspaces();

const canSend = computed(() => !props.busy && text.value.trim().length > 0);

function emitSend() {
  if (!canSend.value) return;
  // 没有 active workspace：先选/建工作区（dsh 式「聊天框即操作台」）。
  if (!workspaces.activeWorkspaceId.value) {
    ctxRowRef.value?.openPicker();
    return;
  }
  const content = text.value;
  text.value = '';
  resetHeight();
  emit('send', content);
}

function onKeydown(e: KeyboardEvent) {
  // IME 组合期 Enter 是候选选择，不发送。
  if (e.isComposing || e.keyCode === 229) return;
  if (e.key === 'Enter' && e.shiftKey) return; // Shift+Enter 无条件换行
  if (e.key === 'Enter') {
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
