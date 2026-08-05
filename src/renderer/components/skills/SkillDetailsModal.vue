<script setup lang="ts">
/**
 * Skill 详情 modal：body + scripts 列表 + 升级 / 卸载 按钮。
 *
 * bunded skill：卸载按钮 disabled；非 bundled：卸载需二次确认（用 confirm()）。
 */
import { onMounted, ref } from 'vue';
import type { DarvinGetSkillDetailsResponse, DarvinSkillSummary } from '../../../shared/darvin-api';
import Icon from '../common/Icon.vue';
import { t } from '../../services/i18n';

const props = defineProps<{
  skill: DarvinSkillSummary | null;
  open: boolean;
}>();

const emit = defineEmits<{
  close: [];
  upgrade: [skillId: string];
  uninstall: [skillId: string];
}>();

const details = ref<DarvinGetSkillDetailsResponse | null>(null);
const loading = ref(false);

async function fetchDetails(): Promise<void> {
  if (!props.skill) return;
  loading.value = true;
  try {
    details.value = await window.darvin.getSkillDetails({ skillId: props.skill.id });
  } catch {
    details.value = null;
  } finally {
    loading.value = false;
  }
}

onMounted(() => {
  if (props.open) void fetchDetails();
});

function onUpgrade(): void {
  if (!props.skill) return;
  emit('upgrade', props.skill.id);
}

function onUninstall(): void {
  if (!props.skill) return;
  if (props.skill.isBuiltIn) return;
  const ok = window.confirm(t('skill.uninstall.confirm', { name: props.skill.name }));
  if (ok) emit('uninstall', props.skill.id);
}
</script>

<template>
  <Teleport to="body">
    <div
      v-if="open && skill"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      data-testid="skill-details-modal"
    >
      <div class="flex max-h-[80vh] w-full max-w-lg flex-col rounded-xl border border-border bg-surface shadow-lg">
        <div class="flex shrink-0 items-center gap-2 border-b border-border p-4">
          <Icon name="plugin" :size="16" class="text-text-muted" />
          <h3 class="font-sans text-sm font-semibold text-text">{{ skill.name }}</h3>
          <span class="ml-auto font-mono text-[11px] text-text-subtle">v{{ skill.version || '0.0.0' }}</span>
        </div>

        <div class="min-h-0 flex-1 overflow-y-auto p-4 text-xs">
          <div v-if="loading" class="py-8 text-center text-text-muted">{{ t('common.loading') }}</div>
          <pre v-else-if="details" class="whitespace-pre-wrap font-sans text-text">{{ details.body }}</pre>
          <div v-if="details?.scripts && details.scripts.length > 0" class="mt-3 space-y-1 border-t border-border pt-3">
            <div class="font-mono text-[11px] text-text-subtle">scripts:</div>
            <div v-for="s in details.scripts" :key="s.path" class="font-mono text-[11px] text-text">
              {{ s.path }}
            </div>
          </div>
        </div>

        <div class="flex shrink-0 items-center justify-end gap-2 border-t border-border p-4">
          <button
            type="button"
            class="rounded-md px-3 py-1.5 font-sans text-xs font-medium text-text-muted transition-colors hover:bg-surface-2 hover:text-text"
            data-testid="skill-details-close"
            @click="emit('close')"
          >
            {{ t('common.cancel') }}
          </button>
          <button
            v-if="!skill.isBuiltIn"
            type="button"
            class="rounded-md bg-primary px-3 py-1.5 font-sans text-xs font-medium text-white transition-opacity hover:opacity-90"
            :data-testid="`skill-upgrade-${skill.id}`"
            @click="onUpgrade"
          >
            {{ t('common.upgrade') }}
          </button>
          <button
            type="button"
            class="rounded-md px-3 py-1.5 font-sans text-xs font-medium text-danger transition-colors hover:bg-danger/10 disabled:opacity-40"
            :disabled="skill.isBuiltIn"
            :data-testid="`skill-uninstall-${skill.id}`"
            @click="onUninstall"
          >
            {{ t('common.uninstall') }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>
