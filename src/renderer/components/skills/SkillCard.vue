<script setup lang="ts">
/**
 * 单 skill 卡片：name / description / version / switch / 风险徽章 / 详情。
 *
 * 从 useSkills 拿 skill 引用 + 透传 toggle / details 事件。
 * 父组件 SkillsView 接到事件后调 useSkills.setEnabled 或 openDetails。
 */
import { computed } from 'vue';
import type { DarvinSkillSummary } from '../../../shared/darvin-api';
import Icon from '../common/Icon.vue';
import { t } from '../../services/i18n';

const props = defineProps<{
  skill: DarvinSkillSummary;
}>();

const emit = defineEmits<{
  toggle: [skillId: string, enabled: boolean];
  details: [skill: DarvinSkillSummary];
}>();

const riskBadgeClass = computed(() => {
  switch (props.skill.riskLevel) {
    case 'low':      return 'bg-success/10 text-success';
    case 'medium':   return 'bg-warning/10 text-warning';
    case 'high':     return 'bg-danger/10 text-danger';
    case 'critical': return 'bg-danger text-white';
    case 'safe':
    default:         return '';
  }
});

const showRiskBadge = computed(() =>
  props.skill.riskLevel !== undefined && props.skill.riskLevel !== 'safe',
);

function onToggle(e: Event): void {
  const target = e.target as HTMLInputElement;
  emit('toggle', props.skill.id, target.checked);
}

function onDetails(): void {
  emit('details', props.skill);
}
</script>

<template>
  <div
    class="flex flex-col gap-3 rounded-lg border border-border bg-surface p-4"
    :data-testid="`skill-card-${skill.id}`"
  >
    <div class="flex items-start justify-between gap-2">
      <div class="flex min-w-0 flex-1 items-center gap-2">
        <Icon name="plugin" :size="16" class="shrink-0 text-text-muted" />
        <h3 class="truncate font-sans text-sm font-medium text-text">{{ skill.name }}</h3>
        <span
          v-if="skill.isBuiltIn"
          class="shrink-0 rounded bg-primary-muted px-1.5 py-0.5 text-[10px] font-medium text-primary"
        >
          {{ t('skill.badge.builtin') }}
        </span>
        <span
          v-if="showRiskBadge"
          class="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium"
          :class="riskBadgeClass"
        >
          {{ t(`skill.risk.${skill.riskLevel}`) }}
        </span>
      </div>
      <label class="relative inline-flex cursor-pointer items-center">
        <input
          type="checkbox"
          class="peer sr-only"
          :checked="skill.enabled"
          :data-testid="`skill-toggle-${skill.id}`"
          @change="onToggle"
        />
        <span
          class="h-5 w-9 rounded-full bg-border transition-colors peer-checked:bg-primary peer-focus:outline-none peer-focus:ring-2 peer-focus:ring-primary/30 after:absolute after:left-0.5 after:top-0.5 after:h-4 after:w-4 after:rounded-full after:bg-white after:transition-transform peer-checked:after:translate-x-4"
        />
      </label>
    </div>

    <p class="line-clamp-2 font-sans text-xs text-text-muted">{{ skill.description }}</p>

    <div class="flex items-center justify-between">
      <span class="font-mono text-[11px] text-text-subtle">v{{ skill.version || '0.0.0' }}</span>
      <button
        type="button"
        class="font-sans text-xs text-primary transition-opacity hover:opacity-80"
        :data-testid="`skill-details-${skill.id}`"
        @click="onDetails"
      >
        {{ t('skill.card.details') }}
      </button>
    </div>
  </div>
</template>
