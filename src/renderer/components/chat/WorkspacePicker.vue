<template>
  <div
    class="absolute bottom-full left-0 z-30 mb-1 w-80 rounded-xl border border-border bg-surface p-2 shadow-lg"
    data-testid="workspace-picker"
    @click.stop
  >
    <!-- 现有工作区列表 -->
    <div v-if="workspaces.workspaces.value.length > 0" class="max-h-56 overflow-y-auto">
      <button
        v-for="w in workspaces.workspaces.value"
        :key="w.id"
        type="button"
        class="flex w-full items-center gap-2 rounded-lg px-2 py-2 text-left text-xs transition-colors hover:bg-surface-2"
        :class="w.id === workspaces.activeWorkspaceId.value ? 'text-text' : 'text-text-muted'"
        :data-testid="`workspace-item-${w.id}`"
        @click="pick(w.id)"
      >
        <Icon name="folder" :size="14" class="shrink-0" />
        <span class="min-w-0 flex-1 truncate">{{ w.label }}</span>
        <span v-if="w.id === workspaces.activeWorkspaceId.value" class="shrink-0 text-primary">
          <Icon name="check" :size="12" />
        </span>
        <span class="shrink-0 text-text-subtle">{{ formatNumber(w.sessionCount) }}</span>
      </button>
    </div>
    <p v-else class="px-2 py-2 text-xs text-text-subtle">{{ t('workspace.empty.hint') }}</p>

    <!-- 新建工作区 -->
    <div class="mt-1 border-t border-border/60 pt-1">
      <input
        v-model="name"
        class="w-full rounded-md border border-border bg-surface-2 px-2 py-1.5 font-sans text-xs text-text outline-none placeholder:text-text-subtle focus:border-border-strong"
        :placeholder="t('workspace.new.name')"
        @keydown.enter.prevent="submit"
      />
      <div class="mt-1 flex items-center gap-2">
        <button
          type="button"
          class="inline-flex min-w-0 flex-1 items-center gap-1.5 rounded-md border border-border px-2 py-1.5 text-xs text-text-muted transition-colors hover:bg-surface-2 hover:text-text"
          :title="pickedDir ?? t('workspace.new.pick')"
          @click="pickDir"
        >
          <Icon name="folder-open" :size="13" class="shrink-0" />
          <span class="truncate">{{ pickedDir ?? t('workspace.new.pick') }}</span>
        </button>
        <button
          type="button"
          class="shrink-0 rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-primary-hover disabled:opacity-50"
          :disabled="busy"
          @click="submit"
        >
          {{ t('workspace.new.confirm') }}
        </button>
      </div>
    </div>

    <!-- 管理入口 -->
    <div class="mt-1 border-t border-border/60 pt-1">
      <button
        type="button"
        class="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-xs text-text-subtle transition-colors hover:bg-surface-2 hover:text-text"
        data-testid="workspace-manage"
        @click="emit('manage')"
      >
        <Icon name="gear" :size="12" />
        {{ t('workspace.manage') }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import Icon from '../common/Icon.vue';
import { useWorkspaces } from '../../composables/useWorkspaces';
import { t, formatNumber } from '../../services/i18n';

const emit = defineEmits<{ close: []; manage: [] }>();

const workspaces = useWorkspaces();
const name = ref('');
const pickedDir = ref<string | null>(null);
const busy = ref(false);

async function pick(id: string): Promise<void> {
  await workspaces.switchWorkspace(id);
  emit('close');
}

async function pickDir(): Promise<void> {
  const r = await window.darvin.setWorkspaceRoot();
  if (r.canceled || !r.rootPath) return;
  pickedDir.value = r.rootPath;
}

async function submit(): Promise<void> {
  busy.value = true;
  try {
    const w = await workspaces.createWorkspace({
      name: name.value.trim() || undefined,
      rootPath: pickedDir.value ?? undefined,
    });
    await workspaces.switchWorkspace(w.id);
    emit('close');
  } finally {
    busy.value = false;
  }
}
</script>