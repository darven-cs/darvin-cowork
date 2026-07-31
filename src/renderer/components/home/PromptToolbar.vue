<template>
  <div class="flex items-center gap-1 px-1">
    <div class="relative">
      <button
        type="button"
        :aria-label="ariaPlus"
        :title="ariaPlus"
        class="flex h-7 w-7 items-center justify-center rounded-md text-text-muted transition-colors hover:bg-surface-2 hover:text-text"
        :class="fp.isActive('plus') ? 'bg-surface-2 text-text' : ''"
        @click="fp.toggle('plus')"
      >
        <Icon name="plus" :size="16" />
      </button>
      <PlusMenu @pick="onPick" />
    </div>

    <div class="mx-1 h-3.5 w-px bg-border" />

    <button
      type="button"
      :aria-label="ariaGrid"
      :title="ariaGrid"
      class="flex h-7 w-7 items-center justify-center rounded-md text-text-muted transition-colors hover:bg-surface-2 hover:text-text"
      @click="$emit('grid')"
    >
      <Icon name="grid" :size="14" />
    </button>

    <div class="mx-1 h-3.5 w-px bg-border" />

    <ModelPicker />

    <div class="mx-1 h-3.5 w-px bg-border" />

    <MicButton :label="ariaMic" @click="$emit('mic')" />
  </div>
</template>

<script setup lang="ts">
import Icon from '../common/Icon.vue';
import ModelPicker from './ModelPicker.vue';
import MicButton from './MicButton.vue';
import PlusMenu from './PlusMenu.vue';
import { useFloatingPanel } from '../../composables/useFloatingPanel';

const fp = useFloatingPanel();

const ariaPlus = '更多';
const ariaGrid = '专家套件';
const ariaMic  = '语音输入';

defineEmits<{
  grid: [];
  mic: [];
  pick: [id: 'upload' | 'goal' | 'todo' | 'settings'];
}>();

function onPick(id: 'upload' | 'goal' | 'todo' | 'settings') {
  // eslint-disable-next-line no-console
  console.warn('PlusMenu pick:', id);
}
</script>