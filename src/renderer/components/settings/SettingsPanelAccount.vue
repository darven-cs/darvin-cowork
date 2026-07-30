<template>
  <section class="flex flex-col gap-3">
    <h3 class="font-sans text-[15px] font-semibold text-text">{{ t('settings.account.title') }}</h3>
    <p class="font-sans text-[12.5px] text-text-muted">{{ t('settings.account.desc') }}</p>

    <div class="mt-2 flex flex-col gap-3">
      <label class="flex flex-col gap-1">
        <span class="font-sans text-[12.5px] text-text-muted">{{ t('settings.account.username') }}</span>
        <input
          type="text"
          value="darven"
          readonly
          class="cursor-not-allowed rounded-md border border-border bg-surface-2 px-3 py-1.5 font-sans text-[13px] text-text-muted outline-none"
        />
      </label>
      <label class="flex flex-col gap-1">
        <span class="font-sans text-[12.5px] text-text-muted">{{ t('settings.account.email') }}</span>
        <input
          type="email"
          value="1926248272@qq.com"
          readonly
          class="cursor-not-allowed rounded-md border border-border bg-surface-2 px-3 py-1.5 font-sans text-[13px] text-text-muted outline-none"
        />
      </label>
    </div>

    <button
      type="button"
      class="mt-3 self-start rounded-md border border-border px-3 py-1.5 font-sans text-[13px] text-text-muted transition-colors hover:bg-surface-hover hover:text-text"
      @click="onLogout"
    >
      {{ t('settings.account.logout') }}
    </button>
  </section>
</template>

<script setup lang="ts">
import { t } from '../../services/i18n';

function onLogout() {
  // eslint-disable-next-line no-console
  console.warn(t('settings.account.logout_warn'));
}
</script>