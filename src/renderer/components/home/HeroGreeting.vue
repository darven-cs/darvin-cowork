<template>
  <div class="flex flex-col items-center gap-2 text-center">
    <h1
      class="font-display text-[28px] font-medium leading-tight text-text"
    >
      {{ greeting }}<span v-if="userName">，{{ userName }}</span>
    </h1>
    <p class="font-sans text-[14px] text-text-muted">
      {{ t('home.subtitle') }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { t } from '../../services/i18n';

defineProps<{ userName?: string }>();

const greeting = computed(() => {
  const h = new Date().getHours();
  if (h < 5)  return t('home.greeting.evening');
  if (h < 11) return t('home.greeting.morning');
  if (h < 14) return t('home.greeting.noon');
  if (h < 18) return t('home.greeting.afternoon');
  return t('home.greeting.evening');
});
</script>