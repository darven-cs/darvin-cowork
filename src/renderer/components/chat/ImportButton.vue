<script setup lang="ts">
import { useImportedFiles } from '../../composables/useImportedFiles';
import { t } from '../../services/i18n';
import Icon from '../common/Icon.vue';

const { importFiles, busy } = useImportedFiles();

const onClick = async () => {
  await importFiles();
};
</script>

<template>
  <button
    type="button"
    :disabled="busy"
    :aria-label="t('composer.import')"
    class="flex h-9 w-9 shrink-0 items-center justify-center rounded-full text-text-muted transition-colors hover:bg-surface-2 hover:text-text disabled:cursor-not-allowed disabled:opacity-50"
    data-testid="composer-import"
    @click="onClick"
  >
    <Icon name="paperclip" :size="16" />
  </button>
</template>
