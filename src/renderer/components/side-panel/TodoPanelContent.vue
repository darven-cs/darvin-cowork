<template>
  <div class="flex h-full flex-col" data-testid="todo-panel-content">
    <div
      v-if="!hasList"
      class="flex flex-1 flex-col items-center justify-center px-6 text-center"
      data-testid="todo-empty"
    >
      <p class="text-sm text-text-muted">{{ t('artifact.todo.empty') }}</p>
      <p class="mt-1 text-xs text-text-subtle">{{ t('artifact.todo.empty.hint') }}</p>
    </div>

    <div
      v-else-if="items.length === 0"
      class="flex flex-1 flex-col items-center justify-center px-6 text-center"
      data-testid="todo-empty-list"
    >
      <p class="text-sm text-text-muted">{{ t('artifact.todo.empty.list') }}</p>
    </div>

    <template v-else>
      <div class="flex h-9 shrink-0 items-center border-b border-border px-4" data-testid="todo-panel-title">
        <h2 class="text-sm font-medium text-text">{{ t('artifact.special.todo') }}</h2>
      </div>
      <div class="min-h-0 flex-1 overflow-y-auto py-1" data-testid="todo-list">
        <ul>
          <li
            v-for="(row, i) in rows"
            :key="`${i}-${row.item.content}`"
            class="flex items-start gap-2.5 px-4 py-2"
            :class="isTree && row.item.level === 1 ? 'pl-9' : ''"
            data-testid="todo-item"
          >
            <span
              class="mt-0.5 flex shrink-0 items-center"
              :aria-label="t(statusKey(row.item.status))"
              data-testid="todo-status"
            >
              <span
                v-if="row.item.status === 'pending'"
                class="h-2 w-2 rounded-full border border-text-subtle"
              />
              <span
                v-else-if="row.item.status === 'in_progress'"
                class="h-2 w-2 animate-todo-pulse rounded-full bg-primary"
              />
              <Icon v-else name="check" :size="14" class="text-success" />
            </span>

            <div class="min-w-0 flex-1">
              <p
                class="text-sm text-text"
                :class="[
                  isTree && row.item.level === 0 ? 'font-semibold' : '',
                  row.item.status === 'completed' ? 'text-text-subtle line-through' : '',
                ]"
              >
                {{ row.item.content }}
              </p>
              <p
                v-if="row.item.status === 'in_progress' && row.item.activeForm"
                class="mt-0.5 text-xs text-text-muted"
              >
                {{ row.item.activeForm }}
              </p>
            </div>

            <span
              v-if="row.signOff"
              class="shrink-0 rounded-full bg-success/10 px-2 py-0.5 text-[11px] font-medium text-success"
              data-testid="todo-signoff"
            >
              {{ t('artifact.todo.signedOff', { n: row.signOff.evidenceCount }) }}
            </span>
          </li>
        </ul>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { useTodos, type TodoItem, type TodoSignOff } from '../../composables/useTodos';
import Icon from '../common/Icon.vue';
import { t } from '../../services/i18n';

interface TodoRow {
  item: TodoItem;
  signOff: TodoSignOff | undefined;
}

defineProps<{ sessionId: string }>();

const todos = useTodos();
const { items, signOffs, hasList } = todos;

/** 存在 level=1 子步骤 → 树形渲染（里程碑加粗、子步骤缩进），否则平铺。 */
const isTree = computed(() => items.value.some((item) => item.level === 1));

const signOffsByContent = computed(() => {
  const map = new Map<string, TodoSignOff>();
  for (const s of signOffs.value) map.set(s.content, s);
  return map;
});

const rows = computed<TodoRow[]>(() =>
  items.value.map((item) => ({ item, signOff: signOffsByContent.value.get(item.content) })),
);

function statusKey(status: TodoItem['status']): string {
  switch (status) {
    case 'in_progress': return 'artifact.todo.status.in_progress';
    case 'completed':   return 'artifact.todo.status.completed';
    default:            return 'artifact.todo.status.pending';
  }
}
</script>
