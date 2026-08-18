<script setup lang="ts">
import { ref, computed } from 'vue';
import { t } from '../../services/i18n';
import type { DarvinScheduleInput } from '../../../shared/darvin-api';

const props = defineProps<{
  initial?: { id: string; name: string; enabled: boolean; kind: 'at' | 'every' | 'cron'; schedule: unknown; prompt: string };
  mode?: 'create' | 'edit';
}>();

const emit = defineEmits<{
  submit: [input: DarvinScheduleInput];
  cancel: [];
}>();

const kind = ref<'at' | 'every' | 'cron'>(props.initial?.kind ?? 'cron');
const name = ref(props.initial?.name ?? '');
const prompt = ref(props.initial?.prompt ?? '');
const enabled = ref(props.initial?.enabled ?? true);

const atValue = ref('');
const everyMs = ref(3600000);
const cronExpr = ref('0 9 * * *');
const cronTz = ref('');

function buildInput(): DarvinScheduleInput {
  const base: DarvinScheduleInput = {
    name: name.value.trim(),
    enabled: enabled.value,
    kind: kind.value,
    prompt: prompt.value,
  };
  if (kind.value === 'at') {
    return { ...base, kind: 'at', schedule: { kind: 'at', at: atValue.value } };
  }
  if (kind.value === 'every') {
    return { ...base, kind: 'every', schedule: { kind: 'every', everyMs: everyMs.value } };
  }
  return { ...base, kind: 'cron', schedule: { kind: 'cron', expr: cronExpr.value, tz: cronTz.value || undefined } };
}

function onSubmit(): void {
  if (!name.value.trim() || !prompt.value.trim()) return;
  emit('submit', buildInput());
}

const isEdit = computed(() => props.mode === 'edit');
</script>

<template>
  <form class="rounded-lg border border-border bg-surface-raised p-4" @submit.prevent="onSubmit">
    <h3 class="mb-3 text-base font-medium">
      {{ isEdit ? t('schedule.card.actions.edit') : t('schedule.nav.title') }}
    </h3>
    <div class="grid grid-cols-2 gap-3 text-sm">
      <label class="flex flex-col gap-1">
        <span class="text-text-muted">{{ t('schedule.form.kind.at') }} / {{ t('schedule.form.kind.every') }} / {{ t('schedule.form.kind.cron') }}</span>
        <select v-model="kind" class="rounded border border-border bg-bg px-2 py-1">
          <option value="at">{{ t('schedule.form.kind.at') }}</option>
          <option value="every">{{ t('schedule.form.kind.every') }}</option>
          <option value="cron">{{ t('schedule.form.kind.cron') }}</option>
        </select>
      </label>
      <label class="flex flex-col gap-1">
        <span class="text-text-muted">Name</span>
        <input v-model="name" type="text" class="rounded border border-border bg-bg px-2 py-1" required />
      </label>
      <template v-if="kind === 'at'">
        <label class="col-span-2 flex flex-col gap-1">
          <span class="text-text-muted">{{ t('schedule.form.field.at') }}</span>
          <input v-model="atValue" type="datetime-local" class="rounded border border-border bg-bg px-2 py-1" required />
        </label>
      </template>
      <template v-else-if="kind === 'every'">
        <label class="col-span-2 flex flex-col gap-1">
          <span class="text-text-muted">{{ t('schedule.form.field.every.ms') }}</span>
          <input v-model.number="everyMs" type="number" min="1000" class="rounded border border-border bg-bg px-2 py-1" required />
        </label>
      </template>
      <template v-else>
        <label class="col-span-2 flex flex-col gap-1">
          <span class="text-text-muted">{{ t('schedule.form.field.cron.expr') }}</span>
          <input v-model="cronExpr" type="text" placeholder="0 9 * * *" class="rounded border border-border bg-bg px-2 py-1 font-mono" required />
        </label>
        <label class="col-span-2 flex flex-col gap-1">
          <span class="text-text-muted">{{ t('schedule.form.field.cron.tz') }}</span>
          <input v-model="cronTz" type="text" placeholder="Asia/Shanghai" class="rounded border border-border bg-bg px-2 py-1 font-mono" />
        </label>
      </template>
      <label class="col-span-2 flex flex-col gap-1">
        <span class="text-text-muted">{{ t('schedule.form.field.prompt') }}</span>
        <textarea v-model="prompt" rows="3" class="rounded border border-border bg-bg px-2 py-1 font-mono" required />
      </label>
      <label class="col-span-2 flex items-center gap-2">
        <input v-model="enabled" type="checkbox" />
        <span class="text-text-muted">Enabled</span>
      </label>
    </div>
    <div class="mt-4 flex gap-2">
      <button type="submit" class="rounded bg-primary px-3 py-1 text-sm text-text-inverse">{{ t('common.save') }}</button>
      <button v-if="isEdit" type="button" class="rounded border border-border px-3 py-1 text-sm text-text-muted" @click="emit('cancel')">Cancel</button>
    </div>
  </form>
</template>