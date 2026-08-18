<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useIm } from '../composables/useIm';
import { t } from '../services/i18n';
import type { DarvinIMInstance } from '../../shared/darvin-api';
import ImInstanceCard from '../components/im/ImInstanceCard.vue';

defineProps<{ sidePanelOpen: boolean }>();
defineEmits<{ 'toggle-sidebar': []; 'toggle-side-panel': [] }>();

const PLATFORMS: Array<{ id: string; labelKey: string; hasGroups: boolean; needsQr: boolean }> = [
  { id: 'qq', labelKey: 'im.nav.qq', hasGroups: true, needsQr: false },
  { id: 'wecom', labelKey: 'im.nav.wecom', hasGroups: true, needsQr: false },
  { id: 'weixin', labelKey: 'im.nav.weixin', hasGroups: false, needsQr: true },
];

const { instances, loadAll, create } = useIm();

const activePlatform = ref('qq');
const adding = ref(false);

// IM instances are app-global (not scoped to the active chat workspace).
onMounted(() => {
  void loadAll();
});

const platformInstances = computed<DarvinIMInstance[]>(() =>
  instances.value.filter((i) => i.channel === activePlatform.value),
);

function startAdd(): void {
  adding.value = true;
}

async function handleCreate(config: Record<string, unknown>): Promise<void> {
  const inst = await create({
    channel: activePlatform.value,
    name: '',
    config,
    enabled: activePlatform.value === 'weixin', // weixin 扫码拿到 token 后立即启动连接
  });
  if (inst) {
    adding.value = false;
    void loadAll();
  }
}
</script>

<template>
  <div class="flex h-full flex-col overflow-y-auto bg-bg p-6">
    <div class="mb-5 flex items-center justify-between">
      <h1 class="text-xl font-semibold text-text">{{ t('im.nav.title') }}</h1>
    </div>

    <div class="flex gap-3 border-b border-border pb-3 mb-5">
      <button
        v-for="p in PLATFORMS"
        :key="p.id"
        class="rounded-md px-3 py-1.5 text-sm transition-colors"
        :class="activePlatform === p.id
          ? 'bg-primary-soft text-primary'
          : 'text-text-muted hover:bg-surface-hover'"
        @click="activePlatform = p.id; adding = false"
      >
        {{ t(p.labelKey) }}
      </button>
      <div class="flex-1" />
      <button
        class="rounded-md bg-primary px-3 py-1.5 text-sm text-white"
        @click="startAdd"
      >
        {{ t('im.add') }}
      </button>
    </div>

    <ImInstanceCard
      :instances="platformInstances"
      :channel="activePlatform"
      :needs-qr="PLATFORMS.find((p) => p.id === activePlatform)?.needsQr ?? false"
      :creating="adding"
      @cancel-create="adding = false"
      @submit-create="handleCreate"
      @created="adding = false"
    />
  </div>
</template>
