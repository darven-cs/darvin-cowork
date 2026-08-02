<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { t } from '../../services/i18n';
import Icon from '../common/Icon.vue';

defineProps<{ rootPath: string | null; label: string | null }>();
const emit = defineEmits<{ close: []; changed: [] }>();

const RECENTS_KEY = 'darvin.recent-workspaces';
const recents = ref<string[]>([]);
const busy = ref(false);

onMounted(() => {
  try {
    const raw = JSON.parse(localStorage.getItem(RECENTS_KEY) ?? '[]') as unknown;
    recents.value = Array.isArray(raw)
      ? raw.filter((x): x is string => typeof x === 'string').slice(0, 8)
      : [];
  } catch {
    recents.value = [];
  }
});

function remember(p: string): void {
  const next = [p, ...recents.value.filter((x) => x !== p)].slice(0, 8);
  recents.value = next;
  try {
    localStorage.setItem(RECENTS_KEY, JSON.stringify(next));
  } catch {
    /* 存储不可用（隐私模式）时忽略 */
  }
}

async function browse(): Promise<void> {
  busy.value = true;
  try {
    const r = await window.darvin.setWorkspaceRoot();
    if (!r.canceled && r.rootPath) {
      remember(r.rootPath);
      emit('changed');
    }
  } finally {
    busy.value = false;
  }
  emit('close');
}

async function pick(path: string): Promise<void> {
  busy.value = true;
  try {
    const r = await window.darvin.setWorkspaceRootTo(path);
    if (!r.canceled && r.rootPath) {
      remember(r.rootPath);
      emit('changed');
    }
  } finally {
    busy.value = false;
  }
  emit('close');
}

function reveal(): void {
  void window.darvin.revealWorkspace();
  emit('close');
}
</script>

<template>
  <div
    class="absolute bottom-full left-0 z-30 mb-1 w-80 rounded-xl border border-border bg-surface p-2 shadow-lg"
    data-testid="folder-picker"
    @click.stop
  >
    <div class="mb-2 px-2 pt-1">
      <p class="text-[11px] text-text-subtle">{{ t('workspace.current') }}</p>
      <p class="truncate font-mono text-xs text-text" :title="rootPath ?? ''">
        {{ rootPath ?? t('workspace.empty') }}
      </p>
    </div>
    <button
      type="button"
      class="flex w-full items-center gap-2 rounded-lg px-2 py-2 text-left text-xs text-text transition-colors hover:bg-surface-2 disabled:cursor-not-allowed disabled:opacity-50"
      data-testid="workspace-browse"
      :disabled="busy"
      @click="browse"
    >
      <Icon name="folder" :size="14" />
      {{ t('workspace.pick.browse') }}
    </button>
    <div v-if="recents.length > 0" class="mt-1">
      <p class="px-2 py-1 text-[11px] text-text-subtle">{{ t('workspace.recent') }}</p>
      <button
        v-for="p in recents"
        :key="p"
        type="button"
        :title="p"
        class="block w-full truncate rounded-lg px-2 py-1.5 text-left font-mono text-xs text-text-muted transition-colors hover:bg-surface-2 hover:text-text"
        data-testid="workspace-recent"
        @click="pick(p)"
      >
        {{ p }}
      </button>
    </div>
    <div class="mt-1 border-t border-border/60 pt-1">
      <button
        type="button"
        class="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-xs text-text-subtle transition-colors hover:bg-surface-2 hover:text-text"
        data-testid="workspace-reveal"
        @click="reveal"
      >
        <Icon name="external-open" :size="12" />
        {{ t('workspace.reveal') }}
      </button>
    </div>
  </div>
</template>
