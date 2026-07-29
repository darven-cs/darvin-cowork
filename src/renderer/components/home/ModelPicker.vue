<template>
  <!--
    PR-2 chip 版：单按钮显示当前模型，dropdown 由 PR-3 落地。
  -->
  <button
    type="button"
    :aria-label="t('model.label')"
    class="inline-flex h-7 items-center gap-1.5 rounded-md px-2 text-[13px] text-text-muted transition-colors hover:bg-surface-2 hover:text-text"
    @click="$emit('open')"
  >
    <Icon name="bolt" :size="13" />
    <span class="font-medium">{{ currentLabel }}</span>
    <Icon name="chevron-down" :size="12" />
  </button>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { t } from '../../services/i18n';
import Icon from '../common/Icon.vue';
import { useModel } from '../../composables/useModel';

defineEmits<{ open: [] }>();

const model = useModel();
const currentLabel = computed(
  () => model.options.find((o) => o.id === model.currentModel.value)?.label ?? '',
);
</script>