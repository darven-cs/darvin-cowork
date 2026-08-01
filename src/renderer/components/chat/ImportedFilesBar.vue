<script setup lang="ts">
import { useImportedFiles, MAX_WORKSPACE_BYTES } from '../../composables/useImportedFiles';
import { t } from '../../services/i18n';
import Icon from '../common/Icon.vue';

const { files, workspaceBytes, remove, notice, formatBytes } = useImportedFiles();

const reveal = () => window.darvin.revealWorkspace();
</script>

<template>
  <div class="mx-auto max-w-[760px]">
    <div v-if="files.length > 0" class="flex flex-wrap items-center gap-1.5">
      <div
        v-for="f in files"
        :key="f.id"
        class="flex items-center gap-1.5 rounded-md border border-border bg-surface-2 px-2 py-1 text-xs text-text"
      >
        <Icon name="file-text" :size="12" />
        <span class="max-w-[180px] truncate" :title="f.originalName">{{ f.originalName }}</span>
        <span class="text-text-subtle">{{ formatBytes(f.size) }}</span>
        <button
          type="button"
          :aria-label="t('imported.remove')"
          class="text-text-subtle transition-colors hover:text-danger"
          data-testid="imported-remove"
          @click="remove(f.relativePath)"
        >
          <Icon name="x" :size="12" />
        </button>
      </div>
      <button
        type="button"
        :aria-label="t('imported.reveal')"
        class="rounded-md px-1.5 py-1 text-xs text-text-subtle transition-colors hover:bg-surface-2 hover:text-text"
        data-testid="imported-reveal"
        @click="reveal"
      >
        {{ t('imported.workspace_meter') }}：{{ formatBytes(workspaceBytes) }} / {{ formatBytes(MAX_WORKSPACE_BYTES) }}
      </button>
    </div>
    <p v-if="notice" class="mt-1 text-xs text-warning" data-testid="imported-notice">
      {{ notice }}
    </p>
    <p v-else-if="files.length === 0" class="text-xs text-text-subtle" data-testid="imported-empty">
      {{ t('imported.empty') }}
    </p>
  </div>
</template>
