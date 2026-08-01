<template>
  <Teleport to="body">
    <div class="pointer-events-none fixed inset-x-0 top-4 z-[100] flex flex-col items-center gap-2 px-4">
      <div
        v-for="toast in toasts"
        :key="toast.id"
        class="pointer-events-auto flex max-w-full animate-fade-in items-center gap-2.5 rounded-lg border bg-surface px-3.5 py-2 text-sm shadow-lg"
        :class="kindClass(toast.kind)"
        role="status"
      >
        <span class="min-w-0">{{ toast.message }}</span>
        <button
          type="button"
          class="shrink-0 text-text-subtle transition-colors hover:text-text"
          :aria-label="t('toast.dismiss')"
          @click="dismissToast(toast.id)"
        >
          <Icon name="x" :size="14" />
        </button>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { dismissToast, useToasts, type ToastKind } from '../../services/toast';
import { t } from '../../services/i18n';

const { toasts } = useToasts();

function kindClass(kind: ToastKind): string {
  switch (kind) {
    case 'success': return 'border-success/40 text-success';
    case 'error': return 'border-danger/40 text-danger';
    default: return 'border-border text-text';
  }
}
</script>
