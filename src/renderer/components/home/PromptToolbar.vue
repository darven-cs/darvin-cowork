<template>
  <div class="flex items-center gap-1 px-1">
    <!-- Plus（PR-3 接 PlusMenu 浮层） -->
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

    <!-- Grid（PR-4 接 ExpertSuite 视图） -->
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

    <!-- Model picker chip + dropdown -->
    <ModelPicker />

    <div class="mx-1 h-3.5 w-px bg-border" />

    <!-- Mic -->
    <MicButton :aria-label="ariaMic" @click="$emit('mic')" />
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
  // PR-3 stub：仅打印；后续路由 / 弹窗落地
  // eslint-disable-next-line no-console
  console.warn('PlusMenu pick:', id);
}
</script>