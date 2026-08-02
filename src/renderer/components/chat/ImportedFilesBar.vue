<script setup lang="ts">
import { useImportedFiles } from '../../composables/useImportedFiles';
import { t } from '../../services/i18n';
import Icon from '../common/Icon.vue';

const { attachments, remove, formatBytes } = useImportedFiles();
</script>

<template>
  <div class="mx-auto max-w-[760px]">
    <div v-if="attachments.length > 0" class="flex flex-wrap items-center gap-1.5">
      <div
        v-for="a in attachments"
        :key="a.path"
        class="flex items-center gap-1.5 rounded-md border border-border bg-surface-2 px-2 py-1 text-xs text-text"
      >
        <Icon name="file-text" :size="12" />
        <span class="max-w-[180px] truncate" :title="a.name">{{ a.name }}</span>
        <span class="text-text-subtle">{{ formatBytes(a.size) }}</span>
        <button
          type="button"
          :aria-label="t('imported.detach')"
          class="text-text-subtle transition-colors hover:text-danger"
          data-testid="imported-remove"
          @click="remove(a.path)"
        >
          <Icon name="x" :size="12" />
        </button>
      </div>
    </div>
    <p v-else class="text-xs text-text-subtle" data-testid="imported-empty">
      {{ t('attachment.empty') }}
    </p>
  </div>
</template>
