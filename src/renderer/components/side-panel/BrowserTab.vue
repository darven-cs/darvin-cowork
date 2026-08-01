<template>
  <div class="flex h-full flex-col" data-testid="browser-tab">
    <div class="flex shrink-0 items-center gap-1.5 border-b border-border px-2 py-1.5">
      <input
        v-model="inputUrl"
        type="text"
        :placeholder="t('artifact.browser.placeholder')"
        class="h-6 min-w-0 flex-1 rounded border border-border bg-surface px-2 text-xs text-text outline-none placeholder:text-text-subtle focus:border-primary"
        data-testid="browser-url-input"
        @keydown.enter="go"
      />
      <button
        type="button"
        class="shrink-0 rounded border border-border px-2 py-0.5 text-xs text-text-muted transition-colors hover:bg-surface-hover hover:text-text"
        data-testid="browser-go"
        @click="go"
      >
        {{ t('artifact.browser.go') }}
      </button>
    </div>
    <iframe
      :src="url"
      class="min-h-0 flex-1 border-0 bg-surface"
      :title="url"
      data-testid="browser-iframe"
    />
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import { t } from '../../services/i18n';

const url = ref('https://example.com');
const inputUrl = ref(url.value);

function go(): void {
  let value = inputUrl.value.trim();
  if (!value) return;
  if (!/^https?:\/\//i.test(value)) {
    value = `https://${value}`;
  }
  url.value = value;
}
</script>
