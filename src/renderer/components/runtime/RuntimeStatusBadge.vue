<template>
  <span
    class="inline-flex shrink-0 items-center gap-1.5 rounded-full bg-surface-2 px-2 py-0.5 text-[11px] font-medium select-none"
    :class="toneClass"
    :title="label"
  >
    <span class="h-1.5 w-1.5 rounded-full" :class="dotClass"></span>
    <span>{{ label }}</span>
  </span>
</template>

<script setup lang="ts">
/**
 * Runtime 状态指示：绿 = 在线，琥珀 = 子进程掉了，红 = 二进制没编译。
 */
import { computed, onMounted, onUnmounted, ref } from 'vue';
import type { DarvinRuntimeStatus } from '../../../shared/darvin-api';
import { t } from '../../services/i18n';

const POLL_MS = 2000;

const status = ref<DarvinRuntimeStatus>('offline');
let timer: number | undefined;

async function refresh(): Promise<void> {
  try {
    status.value = await window.darvin.status();
  } catch {
    status.value = 'offline';
  }
}

onMounted(() => {
  void refresh();
  timer = window.setInterval(() => void refresh(), POLL_MS);
});

onUnmounted(() => {
  if (timer !== undefined) window.clearInterval(timer);
});

// 'ready' 是给远期 supervisor 预留的（子进程在跑且队列已空），暂未发出，与 'online' 同处理。
const label = computed(() => {
  switch (status.value) {
    case 'online':
    case 'ready':
      return t('app.runtime.ready');
    case 'no-binary':
      return t('app.runtime.no_binary');
    default:
      return t('app.runtime.offline');
  }
});

const toneClass = computed(() => {
  switch (status.value) {
    case 'online':
    case 'ready':
      return 'text-success';
    case 'no-binary':
      return 'text-danger';
    default:
      return 'text-warning';
  }
});

const dotClass = computed(() => {
  switch (status.value) {
    case 'online':
    case 'ready':
      return 'bg-success';
    case 'no-binary':
      return 'bg-danger';
    default:
      return 'bg-warning';
  }
});
</script>
