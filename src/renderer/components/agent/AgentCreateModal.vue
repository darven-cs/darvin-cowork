<template>
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
    <div class="w-[min(560px,92vw)] rounded-lg border border-border bg-surface-raised p-5">
      <div class="mb-3 flex items-center justify-between">
        <h3 class="font-sans text-[15px] font-semibold text-text">{{ t('settings.agents.create') }}</h3>
        <button type="button" class="text-text-muted hover:text-text" @click="emit('close')">×</button>
      </div>
      <div class="mb-3 flex gap-1 rounded-md border border-border bg-surface p-1 text-[12.5px]">
        <button
          type="button"
          :class="[
            'flex-1 rounded-sm py-1.5 font-sans',
            tab === 'blank' ? 'bg-bg text-text shadow-sm' : 'text-text-muted hover:text-text',
          ]"
          @click="tab = 'blank'"
        >
          {{ t('settings.agents.modal_create_blank') }}
        </button>
        <button
          type="button"
          :class="[
            'flex-1 rounded-sm py-1.5 font-sans',
            tab === 'preset' ? 'bg-bg text-text shadow-sm' : 'text-text-muted hover:text-text',
          ]"
          @click="tab = 'preset'"
        >
          {{ t('settings.agents.modal_create_preset') }}
        </button>
      </div>

      <div v-if="tab === 'preset'" class="mb-3 flex flex-col gap-1">
        <label class="font-sans text-[12.5px] text-text-muted">Preset</label>
        <select v-model="fromPresetId" class="rounded-md border border-border bg-surface px-3 py-1.5 font-sans text-[13px] text-text outline-none focus:border-primary">
          <option value="" disabled>{{ t('settings.agents.modal_preset_placeholder') }}</option>
          <option v-for="p in presetAgents" :key="p.id" :value="p.id">{{ p.name }}</option>
        </select>
      </div>

      <div class="flex flex-col gap-2">
        <label class="flex flex-col gap-1">
          <span class="font-sans text-[12.5px] text-text-muted">{{ t('settings.agents.field.name') }}</span>
          <input v-model="name" type="text" class="rounded-md border border-border bg-surface px-3 py-1.5 font-sans text-[13px] text-text outline-none focus:border-primary" />
        </label>
      </div>

      <div class="mt-4 flex justify-end gap-2">
        <button type="button" class="rounded-md border border-border bg-surface px-3 py-1.5 font-sans text-[12.5px] text-text" @click="emit('close')">取消</button>
        <button type="button" class="rounded-md bg-primary px-3 py-1.5 font-sans text-[12.5px] font-medium text-white hover:bg-primary-hover disabled:opacity-50" :disabled="!canSubmit" @click="onSubmit">确定</button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';
import { useAgents } from '../../composables/useAgents';
import { useToasts } from '../../services/toast';
import { t, getLang } from '../../services/i18n';

const props = defineProps<{ workspaceId: string }>();
const emit = defineEmits<{
  close: [];
  created: [string];
}>();

const agentsApi = useAgents();
const { showToast } = useToasts();

const tab = ref<'blank' | 'preset'>('blank');
const fromPresetId = ref('');
const name = ref('');

const presetAgents = computed(() =>
  agentsApi.agents.value
    .filter((a) => a.source === 'preset' && !a.isDefault)
    .map((a) => ({ id: a.id, name: getLang() === 'en' ? (a.nameEn || a.name) : a.name })),
);

const canSubmit = computed(() => {
  if (tab.value === 'preset') return fromPresetId.value !== '';
  return name.value.trim() !== '';
});

async function onSubmit(): Promise<void> {
  try {
    const created = await agentsApi.createAgent({
      workspaceId: props.workspaceId,
      name: name.value.trim() || '',
      fromPresetId: tab.value === 'preset' ? fromPresetId.value : undefined,
    });
    showToast(t('settings.agents.created'), 'success');
    emit('created', created.id);
  } catch (e) {
    showToast(t('settings.agents.create_failed') + ': ' + (e as Error).message, 'error');
  }
}
</script>