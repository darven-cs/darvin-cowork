<template>
  <div class="text-xs">
    <div v-if="kind === 'todowrite' && todoItems" class="space-y-1.5">
      <div v-for="(item, i) in todoItems" :key="i" class="flex items-start gap-2">
        <span
          class="mt-0.5 flex h-4 w-4 flex-shrink-0 items-center justify-center rounded border"
          :class="checkboxClass(item.status)"
        >
          <Icon v-if="item.status === 'completed'" name="check" :size="10" />
        </span>
        <span
          class="whitespace-pre-wrap break-words leading-5"
          :class="item.status === 'completed' ? 'text-text-muted line-through' : 'text-text'"
        >
          {{ item.primaryText }}
        </span>
      </div>
    </div>
    <pre v-else class="whitespace-pre-wrap break-words font-mono text-xs text-text">{{ summary }}</pre>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { Message } from '../../../composables/useMessages';
import type { TodoStatus } from '../../../services/toolDisplay';
import { formatToolInput, getToolKind, parseTodoWriteItems } from '../../../services/toolDisplay';

const props = defineProps<{ toolUse: Message }>();

const tool = computed(() => props.toolUse.tool ?? 'Tool');
const kind = computed(() => getToolKind(tool.value));
const summary = computed(() => formatToolInput(tool.value, props.toolUse.input));
const todoItems = computed(() => parseTodoWriteItems(props.toolUse.input));

function checkboxClass(status: TodoStatus): string {
  switch (status) {
    case 'completed':
      return 'border-green-500 bg-green-500/10 text-green-500';
    case 'in_progress':
      return 'border-blue-500 text-blue-500';
    default:
      return 'border-border';
  }
}
</script>
