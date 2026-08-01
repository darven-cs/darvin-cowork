<template>
  <ArtifactPanel v-if="tab === 'artifact' && sessionId" :key="sessionId" :session-id="sessionId" />
  <div v-else class="flex flex-1 flex-col items-center justify-center px-6 text-center">
    <p class="font-display text-lg italic text-text-muted">{{ title }}</p>
    <p class="mt-1 text-sm text-text-subtle">{{ subtitle }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { t } from '../../services/i18n';
import type { SidePanelTab } from '../../composables/useSidePanel';
import { useSession } from '../../composables/useSession';
import ArtifactPanel from './ArtifactPanel.vue';

const props = defineProps<{ tab: SidePanelTab }>();

const session = useSession();
const sessionId = computed(() => session.activeSessionId.value);

const title = computed(() => {
  switch (props.tab) {
    case 'tools':    return 'Tools';
    case 'thinking': return 'Thinking';
    case 'artifact': return 'Artifact';
    default:         return '';
  }
});

const subtitle = computed(() => {
  switch (props.tab) {
    case 'tools':    return t('sidepanel.empty.tools');
    case 'thinking': return t('sidepanel.empty.thinking');
    case 'artifact': return t('sidepanel.empty.artifact');
    default:         return '';
  }
});
</script>
