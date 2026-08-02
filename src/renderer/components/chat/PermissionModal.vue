<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { usePermissions } from '../../composables/usePermissions';
import { t } from '../../services/i18n';
import Icon from '../common/Icon.vue';

const { current, respond } = usePermissions();

const inputText = ref('');
const remember = ref(false);
const denyOpen = ref(false);
const denyMessage = ref('');
const interrupt = ref(false);

watch(current, (req) => {
  if (req) {
    inputText.value = JSON.stringify(req.toolInput ?? {}, null, 2);
    denyOpen.value = false;
    denyMessage.value = '';
  }
});

const levelKey = computed(() => {
  if (current.value?.dangerLevel === 'destructive') return 'permission.level.destructive';
  if (current.value?.dangerLevel === 'caution') return 'permission.level.caution';
  return 'permission.level.safe';
});
const levelColor = computed(() => {
  if (current.value?.dangerLevel === 'destructive') return 'text-danger';
  if (current.value?.dangerLevel === 'caution') return 'text-warning';
  return 'text-success';
});
const levelBadge = computed(() => {
  if (current.value?.dangerLevel === 'destructive') return 'bg-danger/10 text-danger';
  if (current.value?.dangerLevel === 'caution') return 'bg-warning/10 text-warning';
  return 'bg-success/10 text-success';
});

async function onAllow(): Promise<void> {
  let updatedInput: unknown;
  try {
    updatedInput = JSON.parse(inputText.value);
  } catch {
    // 入参 JSON 非法：退回原始入参
  }
  await respond('allow', { updatedInput, remember: remember.value });
}

async function onDeny(): Promise<void> {
  await respond('deny', { message: denyMessage.value || undefined, interrupt: interrupt.value });
}
</script>

<template>
  <Teleport to="body">
    <div
      v-if="current"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      data-testid="permission-modal"
    >
      <div class="w-full max-w-md rounded-xl border border-border bg-surface p-4 shadow-lg">
        <div class="mb-3 flex items-center gap-2">
          <Icon name="alert-circle" :size="16" class="shrink-0" :class="levelColor" />
          <h3 class="font-sans text-sm font-semibold text-text">{{ t('permission.title') }}</h3>
          <span class="ml-auto rounded-md px-1.5 py-0.5 text-[11px] font-medium" :class="levelBadge">
            {{ t(levelKey) }}
          </span>
        </div>
        <dl class="space-y-2 text-xs">
          <div class="flex gap-2">
            <dt class="w-14 shrink-0 text-text-subtle">{{ t('permission.tool') }}</dt>
            <dd class="font-mono text-text">{{ current.toolName }}</dd>
          </div>
          <div class="flex gap-2">
            <dt class="w-14 shrink-0 text-text-subtle">{{ t('permission.reason') }}</dt>
            <dd class="text-text">{{ current.reason }}</dd>
          </div>
        </dl>
        <div class="mt-3">
          <label class="mb-1 block text-xs text-text-subtle">{{ t('permission.edit_input') }}</label>
          <textarea
            v-model="inputText"
            rows="5"
            spellcheck="false"
            class="w-full resize-none rounded-md border border-border bg-bg px-2 py-1.5 font-mono text-xs text-text outline-none focus:border-accent"
            data-testid="permission-input"
          />
        </div>
        <div class="mt-3 flex items-center gap-2">
          <label class="flex cursor-pointer items-center gap-1.5 text-xs text-text-muted">
            <input v-model="remember" type="checkbox" class="accent-accent" data-testid="permission-remember" />
            {{ t('permission.remember') }}
          </label>
        </div>
        <div v-if="denyOpen" class="mt-2 space-y-2">
          <input
            v-model="denyMessage"
            :placeholder="t('permission.deny_message')"
            class="w-full rounded-md border border-border bg-bg px-2 py-1.5 text-xs text-text outline-none focus:border-accent"
            data-testid="permission-deny-msg"
          />
          <label class="flex cursor-pointer items-center gap-1.5 text-xs text-text-muted">
            <input v-model="interrupt" type="checkbox" class="accent-accent" data-testid="permission-interrupt" />
            {{ t('permission.interrupt') }}
          </label>
        </div>
        <div class="mt-4 flex justify-end gap-2">
          <button
            type="button"
            class="rounded-md px-3 py-1.5 text-xs font-medium text-text-muted transition-colors hover:bg-surface-2 hover:text-text"
            :class="denyOpen ? 'text-danger' : ''"
            data-testid="permission-deny"
            @click="denyOpen ? onDeny() : (denyOpen = true)"
          >
            {{ t('permission.deny') }}
          </button>
          <button
            type="button"
            class="rounded-md bg-accent px-3 py-1.5 text-xs font-medium text-white transition-opacity hover:opacity-90"
            data-testid="permission-allow"
            @click="onAllow"
          >
            {{ t('permission.allow') }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>
