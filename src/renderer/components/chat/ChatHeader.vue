<template>
  <header
    class="flex h-14 shrink-0 items-center justify-between border-b border-border bg-bg px-6"
  >
    <div class="flex items-center gap-3 min-w-0">
      <IconButton
        variant="ghost"
        name="menu"
        :label="t('chat.menu.toggle_sidebar')"
        @click="emit('toggle-sidebar')"
      />
      <h1 class="truncate text-[15px] font-medium text-text">{{ title }}</h1>
    </div>
    <div class="flex items-center gap-2">
      <RuntimeStatusBadge />
      <IconButton
        variant="ghost"
        :name="isDark ? 'sun' : 'moon'"
        :label="t('app.theme.toggle')"
        @click="theme.toggle"
      />
      <IconButton
        variant="ghost"
        :name="sidePanelOpen ? 'panel-right-close' : 'panel-right-open'"
        :label="t('chat.menu.toggle_sidepanel')"
        @click="emit('toggle-side-panel')"
      />
    </div>
  </header>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { t } from '../../services/i18n';
import { useSession } from '../../composables/useSession';
import { useTheme } from '../../composables/useTheme';
import IconButton from '../common/IconButton.vue';
import RuntimeStatusBadge from '../runtime/RuntimeStatusBadge.vue';

const props = defineProps<{ sidePanelOpen: boolean; title?: string }>();
const emit = defineEmits<{ 'toggle-sidebar': []; 'toggle-side-panel': [] }>();

const session = useSession();
const theme = useTheme();
const isDark = computed(() => theme.theme.value === 'dark');
const title = computed(() => {
  if (props.title) return props.title;
  if (session.draftMode.value) return t('app.new_chat');
  const id = session.activeSessionId.value;
  const s = id ? session.sessions.value.find((x) => x.id === id) : undefined;
  return s?.title ?? 'Darvin';
});
</script>
