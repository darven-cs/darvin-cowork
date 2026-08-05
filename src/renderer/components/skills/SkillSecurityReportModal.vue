<script setup lang="ts">
/**
 * 安装时弹出的安全报告 modal。
 *
 * 标题 + 风险等级 + 风险分 + findings 列表 + [取消] [仍然安装]。
 * 父组件 SkillsView 持有 report ref；null = 关。
 */
import { computed } from 'vue';
import type { DarvinInstallSkillResponse } from '../../../shared/darvin-api';
import Icon from '../common/Icon.vue';
import { t } from '../../services/i18n';

const props = defineProps<{
  report: DarvinInstallSkillResponse | null;
  open: boolean;
}>();

const emit = defineEmits<{
  cancel: [];
  confirm: [];
}>();

const riskIcon = computed(() => {
  switch (props.report?.riskLevel) {
    case 'medium':   return 'alert-circle';
    case 'high':
    case 'critical': return 'shield';
    default:         return 'check';
  }
});

const riskColor = computed(() => {
  switch (props.report?.riskLevel) {
    case 'medium':   return 'text-warning';
    case 'high':
    case 'critical': return 'text-danger';
    default:         return 'text-success';
  }
});

function severityDotClass(severity: string): string {
  switch (severity) {
    case 'critical': return 'bg-danger';
    case 'danger':   return 'bg-danger/70';
    case 'warning':  return 'bg-warning';
    default:         return 'bg-text-subtle';
  }
}
</script>

<template>
  <Teleport to="body">
    <div
      v-if="open && report"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      data-testid="skill-security-modal"
    >
      <div class="w-full max-w-md rounded-xl border border-border bg-surface p-4 shadow-lg">
        <div class="mb-3 flex items-center gap-2">
          <Icon name="shield" :size="16" class="shrink-0" :class="riskColor" />
          <h3 class="font-sans text-sm font-semibold text-text">
            {{ t('skill.security.title', { name: report.skill.name, version: report.skill.version ?? '0.0.0' }) }}
          </h3>
        </div>

        <div class="mb-3 flex items-center gap-2 text-xs">
          <Icon :name="riskIcon" :size="14" :class="riskColor" />
          <span class="font-medium text-text">{{ t(`skill.risk.${report.riskLevel}`) }}</span>
          <span v-if="report.riskScore !== undefined" class="text-text-subtle">
            {{ t('skill.security.score', { score: report.riskScore }) }}
          </span>
        </div>

        <div v-if="report.riskFindings && report.riskFindings.length > 0" class="space-y-2 border-t border-border pt-3">
          <div
            v-for="(f, i) in report.riskFindings"
            :key="i"
            class="flex items-start gap-2 text-xs"
          >
            <span
              class="mt-1.5 inline-block h-1.5 w-1.5 shrink-0 rounded-full"
              :class="severityDotClass(f.severity)"
            />
            <div class="min-w-0 flex-1">
              <div class="font-mono text-text-subtle">{{ f.file }}:{{ f.line }}</div>
              <div class="text-text">{{ f.message }}</div>
            </div>
          </div>
        </div>

        <div class="mt-4 flex justify-end gap-2">
          <button
            type="button"
            class="rounded-md px-3 py-1.5 font-sans text-xs font-medium text-text-muted transition-colors hover:bg-surface-2 hover:text-text"
            data-testid="skill-security-cancel"
            @click="emit('cancel')"
          >
            {{ t('common.cancel') }}
          </button>
          <button
            type="button"
            class="rounded-md bg-primary px-3 py-1.5 font-sans text-xs font-medium text-white transition-opacity hover:opacity-90"
            data-testid="skill-security-confirm"
            @click="emit('confirm')"
          >
            {{ t('skill.security.confirm') }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>
