<script setup lang="ts">
/**
 * SkillsView 设置 tab：bundled skill 列表 + 启停。
 *
 * 跟「已安装」tab 共用 SkillCard，区别在于设置 tab 只显示 bundled，
 * 不显示 [卸载] 按钮（bundled 不能卸）。
 */
import { computed } from 'vue';
import SkillCard from './SkillCard.vue';
import type { DarvinSkillSummary } from '../../../shared/darvin-api';
import { useSkills } from '../../composables/useSkills';
import { t } from '../../services/i18n';

const emit = defineEmits<{
  toggle: [skillId: string, enabled: boolean];
  details: [skill: DarvinSkillSummary];
}>();

const { skills } = useSkills();
const bundledSkills = computed(() => skills.value.filter((s) => s.isOfficial));
</script>

<template>
  <div class="space-y-3">
    <div v-if="bundledSkills.length === 0" class="py-8 text-center font-sans text-xs text-text-muted">
      {{ t('skill.empty') }}
    </div>
    <div v-else class="grid grid-cols-1 gap-3 md:grid-cols-2">
      <SkillCard
        v-for="skill in bundledSkills"
        :key="skill.id"
        :skill="skill"
        @toggle="(id, enabled) => emit('toggle', id, enabled)"
        @details="(s) => emit('details', s)"
      />
    </div>
  </div>
</template>
