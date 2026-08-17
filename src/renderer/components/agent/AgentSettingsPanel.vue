<template>
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
    <div class="w-[min(720px,92vw)] max-h-[90vh] overflow-y-auto rounded-lg border border-border bg-surface-raised p-5">
      <div class="mb-3 flex items-center justify-between">
        <h3 class="font-sans text-[15px] font-semibold text-text">{{ t('settings.agents.edit') }}</h3>
        <button type="button" class="text-text-muted hover:text-text" @click="emit('close')">×</button>
      </div>
      <div v-if="loading" class="py-8 text-center font-sans text-[12.5px] text-text-muted">…</div>
      <div v-else-if="!agent" class="py-8 text-center font-sans text-[12.5px] text-text-muted">—</div>
      <div v-else class="flex flex-col gap-3">
        <p
          v-if="agent.source === 'preset'"
          class="rounded-md border border-warning/40 bg-warning/10 px-3 py-2 font-sans text-[12px] text-text-muted"
        >
          {{ t('settings.agents.locked_notice') }}
        </p>

        <div class="grid grid-cols-2 gap-3">
          <Field :label="t('settings.agents.field.name')" v-model="form.name" />
          <Field :label="t('settings.agents.field.name_en')" v-model="form.nameEn" />
          <Field :label="t('settings.agents.field.description')" v-model="form.description" />
          <Field :label="t('settings.agents.field.description_en')" v-model="form.descriptionEn" />
          <Field :label="t('settings.agents.field.icon')" v-model="form.icon" />
          <Field :label="t('settings.agents.field.color')" v-model="form.color" />
        </div>
        <Field :label="t('settings.agents.field.identity')" v-model="form.identity" multiline />
        <Field :label="t('settings.agents.field.identity_en')" v-model="form.identityEn" multiline />
        <Field :label="t('settings.agents.field.systemPrompt')" v-model="form.systemPrompt" multiline />
        <Field :label="t('settings.agents.field.systemPrompt_en')" v-model="form.systemPromptEn" multiline />

        <div class="mt-2 flex justify-end gap-2">
          <button type="button" class="rounded-md border border-border bg-surface px-3 py-1.5 font-sans text-[12.5px] text-text" @click="emit('close')">取消</button>
          <button type="button" class="rounded-md bg-primary px-3 py-1.5 font-sans text-[12.5px] font-medium text-white hover:bg-primary-hover" @click="onSave">保存</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue';
import Field from './AgentFormField.vue';
import { useAgents } from '../../composables/useAgents';
import { useToasts } from '../../services/toast';
import { t } from '../../services/i18n';
import type { DarvinAgent } from '../../../shared/darvin-api';

const props = defineProps<{ agentId: string; workspaceId: string }>();
const emit = defineEmits<{ close: [] }>();

const agentsApi = useAgents();
const { showToast } = useToasts();

const loading = ref(false);
const agent = computed<DarvinAgent | null>(() =>
  agentsApi.agents.value.find((a) => a.id === props.agentId) ?? null,
);

interface Form {
  name: string;
  nameEn: string;
  description: string;
  descriptionEn: string;
  identity: string;
  identityEn: string;
  systemPrompt: string;
  systemPromptEn: string;
  icon: string;
  color: string;
}

const form = reactive<Form>({
  name: '', nameEn: '', description: '', descriptionEn: '',
  identity: '', identityEn: '', systemPrompt: '', systemPromptEn: '',
  icon: '', color: '',
});

watch(
  agent,
  (a) => {
    if (!a) return;
    form.name = a.name;
    form.nameEn = a.nameEn;
    form.description = a.description;
    form.descriptionEn = a.descriptionEn;
    form.identity = a.identity;
    form.identityEn = a.identityEn;
    form.systemPrompt = a.systemPrompt;
    form.systemPromptEn = a.systemPromptEn;
    form.icon = a.icon;
    form.color = a.color;
  },
  { immediate: true },
);

async function onSave(): Promise<void> {
  if (!agent.value) return;
  try {
    await agentsApi.updateAgent(agent.value.id, { ...form });
    showToast(t('settings.agents.saved'), 'success');
    emit('close');
  } catch (e) {
    showToast(t('settings.agents.save_failed') + ': ' + (e as Error).message, 'error');
  }
}

// Force the loading state to show on first mount if the agents list is
// still empty (workspace just switched).
if (!agent.value && agentsApi.agents.value.length === 0) {
  loading.value = true;
  void agentsApi.listAgents(props.workspaceId).finally(() => { loading.value = false; });
}
</script>