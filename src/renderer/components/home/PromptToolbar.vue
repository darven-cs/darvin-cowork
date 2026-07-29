<template>
  <div class="flex items-center gap-1 px-1">
    <!-- Plus（PR-3 接 PlusMenu） -->
    <button
      type="button"
      aria-label="更多"
      class="flex h-7 w-7 items-center justify-center rounded-md text-text-muted transition-colors hover:bg-surface-2 hover:text-text"
      @click="$emit('plus')"
    >
      <Icon name="plus" :size="16" />
    </button>

    <div class="mx-1 h-3.5 w-px bg-border" />

    <!-- Grid（PR-3 接 ExpertSuite / Suite filter） -->
    <button
      type="button"
      aria-label="专家套件"
      class="flex h-7 w-7 items-center justify-center rounded-md text-text-muted transition-colors hover:bg-surface-2 hover:text-text"
      @click="$emit('grid')"
    >
      <Icon name="grid" :size="14" />
    </button>

    <div class="mx-1 h-3.5 w-px bg-border" />

    <!-- Model picker chip -->
    <ModelPicker @open="$emit('model')" />

    <div class="mx-1 h-3.5 w-px bg-border" />

    <!-- Mic -->
    <MicButton aria-label="语音输入" @click="$emit('mic')" />
  </div>
</template>

<script setup lang="ts">
import Icon from '../common/Icon.vue';
import ModelPicker from './ModelPicker.vue';
import MicButton from './MicButton.vue';

defineEmits<{
  plus: [];
  grid: [];
  model: [];
  mic: [];
}>();
</script>