<template>
  <div class="flex min-w-0 flex-col h-full">
    <ChatHeader
      :side-panel-open="sidePanelOpen"
      @toggle-sidebar="emit('toggle-sidebar')"
      @toggle-side-panel="emit('toggle-side-panel')"
    />

    <div class="border-b border-border px-6 pt-6 pb-4">
      <h2 class="font-sans text-[20px] font-semibold text-text">{{ t('settings.title') }}</h2>
    </div>

    <div class="flex min-h-0 flex-1">
      <SettingsSubNav :active="active" @select="onSelect" />
      <div class="min-w-0 flex-1 overflow-y-auto px-6 py-6">
        <div class="mx-auto max-w-[640px]">
          <SettingsPanelAccount v-if="active === 'account'" />
          <SettingsPanelAppearance v-else-if="active === 'appearance'" />
          <SettingsPanelShortcuts v-else-if="active === 'shortcuts'" />
          <SettingsPanelModels v-else-if="active === 'models'" />
          <SettingsPanelAbout v-else />
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import ChatHeader from '../components/chat/ChatHeader.vue';
import SettingsSubNav, { type SettingsSectionId } from '../components/settings/SettingsSubNav.vue';
import SettingsPanelAccount from '../components/settings/SettingsPanelAccount.vue';
import SettingsPanelAppearance from '../components/settings/SettingsPanelAppearance.vue';
import SettingsPanelShortcuts from '../components/settings/SettingsPanelShortcuts.vue';
import SettingsPanelModels from '../components/settings/SettingsPanelModels.vue';
import SettingsPanelAbout from '../components/settings/SettingsPanelAbout.vue';
import { t } from '../services/i18n';

defineProps<{ sidePanelOpen: boolean }>();
const emit = defineEmits<{ 'toggle-sidebar': []; 'toggle-side-panel': [] }>();

const active = ref<SettingsSectionId>('appearance');

function onSelect(id: SettingsSectionId) {
  active.value = id;
}
</script>
