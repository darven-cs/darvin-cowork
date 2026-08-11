<template>
  <div class="flex h-full w-full flex-col items-center justify-center gap-3 p-4">
    <span class="flex h-12 w-12 items-center justify-center rounded-xl bg-surface-2 text-text-muted">
      <Icon name="link" :size="22" />
    </span>
    <span class="max-w-full truncate font-mono text-xs text-text-muted" :title="url">{{ url }}</span>
    <div class="flex items-center gap-2">
      <button
        type="button"
        class="rounded border border-border px-2.5 py-1 text-xs text-text transition-colors hover:bg-surface-2"
        data-testid="local-service-open-browser"
        @click="openInBrowser"
      >
        {{ t('artifact.render.openInBrowser') }}
      </button>
      <button
        type="button"
        class="rounded border border-border px-2.5 py-1 text-xs text-text transition-colors hover:bg-surface-2"
        data-testid="local-service-copy"
        @click="copyUrl"
      >
        {{ t('artifact.render.copyUrl') }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import Icon from '../../common/Icon.vue';
import type { Artifact } from '../../../composables/useArtifacts';
import { useArtifacts } from '../../../composables/useArtifacts';
import { useChatActions } from '../../../composables/useChatActions';
import { useSession } from '../../../composables/useSession';
import { t } from '../../../services/i18n';
import { showToast } from '../../../services/toast';

const props = defineProps<{ artifact: Artifact }>();

const artifacts = useArtifacts();
const session = useSession();
const chatActions = useChatActions();

const url = computed(() => props.artifact.url ?? props.artifact.content.trim());

function openInBrowser(): void {
  if (!url.value) return;
  const sid = session.activeSessionId.value;
  if (sid) {
    artifacts.openBrowser(sid, url.value);
  } else {
    void window.darvin.openExternal(url.value);
  }
}

async function copyUrl(): Promise<void> {
  if (!url.value) return;
  const ok = await chatActions.copy(url.value);
  showToast(ok ? t('chat.markdown.copied') : t('artifact.render.copyUrl'), ok ? 'success' : 'error');
}
</script>
