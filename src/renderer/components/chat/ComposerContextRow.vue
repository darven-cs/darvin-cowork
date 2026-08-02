<template>
  <div class="relative flex items-center justify-between border-t border-border/60 px-3 py-1.5">
    <button
      type="button"
      class="inline-flex max-w-[60%] cursor-pointer items-center gap-1.5 rounded-md px-2 py-0.5 font-sans text-[11px] text-text-subtle transition-colors hover:bg-surface-2 hover:text-text"
      :title="t('workspace.pick.title')"
      :aria-label="t('workspace.pick.title')"
      data-testid="composer-workspace"
      @click="open = !open"
    >
      <Icon name="folder" :size="12" />
      <span class="truncate">{{ workspaceLabel }}</span>
      <Icon name="chevron-down" :size="10" />
    </button>
    <div v-if="open" class="fixed inset-0 z-20" @click="open = false" />
    <FolderPicker
      v-if="open"
      :root-path="rootPath"
      :label="workspaceLabel"
      @close="open = false"
      @changed="refresh"
    />
    <span
      class="inline-flex items-center gap-1.5 rounded-md px-2 py-0.5 font-sans text-[11px] text-text-subtle"
      data-testid="composer-agent"
    >
      <Icon name="bolt" :size="12" />
      {{ t('sidebar.agent.main.name') }}
      <Icon name="chevron-down" :size="10" />
    </span>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref, watch } from 'vue';
import { useSession } from '../../composables/useSession';
import { t } from '../../services/i18n';
import Icon from '../common/Icon.vue';
import FolderPicker from './FolderPicker.vue';

const workspaceLabel = ref<string>(t('composer.workspace'));
const rootPath = ref<string | null>(null);
const open = ref(false);
const session = useSession();

async function refresh(): Promise<void> {
  try {
    const [info, root] = await Promise.all([
      window.darvin.getWorkspaceInfo(),
      window.darvin.getWorkspaceRoot(),
    ]);
    if (info.label) workspaceLabel.value = info.label;
    rootPath.value = root.rootPath;
  } catch {
    /* agent offline：保留默认文案 */
  }
}

// 会话切换后工作目录会变，watch active session 重新拉取
watch(
  () => session.activeSessionId.value,
  () => {
    void refresh();
  },
);

onMounted(() => {
  void refresh();
});
</script>
