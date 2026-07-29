<template>
  <button
    type="button"
    :disabled="!canSend"
    :aria-label="t('chat.send')"
    class="flex h-9 w-9 shrink-0 items-center justify-center rounded-full transition-all"
    :class="canSend
      ? 'bg-accent text-white hover:bg-accent-hover hover:scale-[1.04]'
      : 'bg-border cursor-not-allowed'"
    @click="$emit('click')"
  >
    <Icon name="arrow-up" :size="16" />
  </button>
</template>

<script setup lang="ts">
import { t } from '../../services/i18n';
import Icon from '../common/Icon.vue';

defineProps<{ canSend: boolean }>();
defineEmits<{ click: [] }>();
</script>