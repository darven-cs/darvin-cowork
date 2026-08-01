<template>
  <div class="flex items-center justify-between px-3 pb-2 pt-1">
    <div class="flex items-center gap-1">
      <div class="relative">
        <button
          type="button"
          :aria-label="t('composer.plus')"
          :title="t('composer.plus')"
          class="flex h-7 w-7 items-center justify-center rounded-md text-text-muted transition-colors hover:bg-surface-2 hover:text-text"
          :class="fp.isActive('plus') ? 'bg-surface-2 text-text' : ''"
          @click="fp.toggle('plus')"
        >
          <Icon name="plus" :size="16" />
        </button>
        <PlusMenu :busy="busy" @pick="onPick" />
      </div>

      <div class="mx-1 h-3.5 w-px bg-border" />

      <button
        type="button"
        :aria-label="t('composer.suite')"
        :title="t('composer.suite')"
        class="flex h-7 w-7 items-center justify-center rounded-md text-text-muted transition-colors hover:bg-surface-2 hover:text-text"
        @click="emit('suite')"
      >
        <Icon name="grid" :size="14" />
      </button>
    </div>

    <div class="flex items-center gap-1">
      <ContextUsageIndicator :session-id="session.activeSessionId.value" @compact="handleCompact" />
      <ModelPicker />
      <MicButton :label="t('composer.mic')" @click="emit('mic')" />
      <SendButton :can-send="canSend" data-testid="composer-send" @click="emit('send')" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { t } from '../../services/i18n';
import Icon from '../common/Icon.vue';
import ContextUsageIndicator from './ContextUsageIndicator.vue';
import SendButton from '../home/SendButton.vue';
import ModelPicker from '../home/ModelPicker.vue';
import MicButton from '../home/MicButton.vue';
import PlusMenu from '../home/PlusMenu.vue';
import { useFloatingPanel } from '../../composables/useFloatingPanel';
import { useSession } from '../../composables/useSession';
import { useMessages } from '../../composables/useMessages';
import { useImportedFiles } from '../../composables/useImportedFiles';

defineProps<{ canSend: boolean }>();
const emit = defineEmits<{ send: []; suite: []; mic: [] }>();

const fp = useFloatingPanel();
const session = useSession();
const messages = useMessages();
const { busy, importFiles } = useImportedFiles();

function onPick(id: 'upload' | 'goal' | 'todo' | 'settings') {
  if (id === 'upload') {
    void importFiles();
    return;
  }
  // eslint-disable-next-line no-console
  console.warn('PlusMenu pick:', id);
}

// 压缩完成事件到达前，圆环持续旋转；超时未收到事件视为失败。
const COMPACT_TIMEOUT_MS = 15000;

async function handleCompact() {
  const sid = session.activeSessionId.value;
  if (!sid) return;
  messages.beginCompact(sid);
  let accepted = false;
  try {
    const res = await window.darvin.compactContext(sid);
    accepted = !!res && res.accepted;
  } catch {
    accepted = false;
  }
  if (!accepted) {
    messages.endCompact(sid);
    return;
  }
  setTimeout(() => {
    if (messages.contextUsageBySessionId.value[sid]?.status === 'compacting') {
      messages.failCompact(sid);
    }
  }, COMPACT_TIMEOUT_MS);
}
</script>
