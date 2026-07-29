<template>
  <div class="flex items-center gap-2 px-4 pt-4 pb-2">
    <span class="grid h-7 w-7 place-items-center rounded-md bg-primary-muted text-primary">
      <Icon name="mascot-mini" :size="20" />
    </span>
    <span class="text-[15px] font-semibold text-text">{{ t('sidebar.brand') }}</span>
  </div>
</template>

<script setup lang="ts">
import { t } from '../../services/i18n';
import Icon from '../common/Icon.vue';
</script>
