<template>
  <div ref="rootRef" class="relative inline-block">
    <div @click="toggle">
      <slot name="trigger" :open="open" />
    </div>
    <Transition
      enter-active-class="transition-opacity duration-100 ease-out"
      leave-active-class="transition-opacity duration-100 ease-in"
      enter-from-class="opacity-0"
      leave-to-class="opacity-0"
    >
      <div
        v-if="open"
        class="absolute right-0 mt-1 min-w-[160px] z-50"
        @click.stop
      >
        <slot name="menu" :close="close" />
      </div>
    </Transition>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue';

const props = defineProps<{ defaultOpen?: boolean }>();
const emit = defineEmits<{ 'update:open': [boolean] }>();

const open = ref<boolean>(props.defaultOpen === true);
const rootRef = ref<HTMLDivElement | null>(null);

function setOpen(v: boolean) {
  open.value = v;
  emit('update:open', v);
}

function toggle() {
  setOpen(!open.value);
}

function close() {
  setOpen(false);
}

function onDocumentClick(e: MouseEvent) {
  if (!open.value) return;
  const root = rootRef.value;
  if (root && !root.contains(e.target as Node)) {
    setOpen(false);
  }
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape' && open.value) {
    setOpen(false);
  }
}

onMounted(() => {
  document.addEventListener('mousedown', onDocumentClick);
  document.addEventListener('keydown', onKeydown);
});

onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onDocumentClick);
  document.removeEventListener('keydown', onKeydown);
});
</script>
