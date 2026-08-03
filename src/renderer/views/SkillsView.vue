<script setup lang="ts">
/**
 * spec 33 — SkillsView：3 tab（已安装 / 市场 / 设置）+ 2 modal。
 *
 * 数据全部走 `useSkills` singleton。tab 切换只切本地 activeTab，不重拉。
 * install 走 main stub：返回 safe → 立刻 toast；medium → 弹安全报告。
 */
import { computed, ref } from 'vue';
import ChatHeader from '../components/chat/ChatHeader.vue';
import SkillCard from '../components/skills/SkillCard.vue';
import SkillMarketplace from '../components/skills/SkillMarketplace.vue';
import SkillSecurityReportModal from '../components/skills/SkillSecurityReportModal.vue';
import SkillSettingsPanel from '../components/skills/SkillSettingsPanel.vue';
import SkillDetailsModal from '../components/skills/SkillDetailsModal.vue';
import { useSkills } from '../composables/useSkills';
import { showToast } from '../services/toast';
import { t } from '../services/i18n';
import type { DarvinInstallSkillResponse, DarvinSkillSummary } from '../../shared/darvin-api';

defineProps<{ sidePanelOpen: boolean }>();
const emit = defineEmits<{
  'toggle-sidebar': [];
  'toggle-side-panel': [];
}>();

type TabId = 'installed' | 'marketplace' | 'settings';

const tabs: Array<{ id: TabId; labelKey: string }> = [
  { id: 'installed', labelKey: 'skill.tab.installed' },
  { id: 'marketplace', labelKey: 'skill.tab.marketplace' },
  { id: 'settings', labelKey: 'skill.tab.settings' },
];

const activeTab = ref<TabId>('installed');

const { skills, loading, refresh, setEnabled, install, uninstall, upgrade } = useSkills();

// 「已安装」tab 显示全部；「设置」tab 只显示 bundled（panel 内部过滤）。
const visibleSkills = computed<DarvinSkillSummary[]>(() => skills.value);

// 安全报告 modal：null = 关
const securityReport = ref<DarvinInstallSkillResponse | null>(null);
// 详情 modal：null = 关
const detailsSkill = ref<DarvinSkillSummary | null>(null);

async function onToggle(skillId: string, enabled: boolean): Promise<void> {
  try {
    await setEnabled(skillId, enabled);
  } catch {
    // 失败已 toast + 回滚；不向上抛
  }
}

function openDetails(skill: DarvinSkillSummary): void {
  detailsSkill.value = skill;
}

function onDetailsClose(): void {
  detailsSkill.value = null;
}

async function onUpgrade(skillId: string): Promise<void> {
  detailsSkill.value = null;
  await upgrade(skillId);
}

async function onUninstall(skillId: string): Promise<void> {
  detailsSkill.value = null;
  await uninstall(skillId);
}

async function onInstall(source: string): Promise<void> {
  const r = await install(source);
  if (!r) return;
  if (r.riskLevel === 'medium' || r.riskLevel === 'high' || r.riskLevel === 'critical') {
    securityReport.value = r;
  }
}

function onSecurityConfirm(): void {
  // 走 v0 stub 流程：confirm 表示「仍然装」，但 main stub 不真做；
  // 这里只关 modal，提示「需要 main 端真接 scanner」语义。
  showToast(t('skill.install.success', { name: securityReport.value?.skill.name ?? '' }), 'success');
  securityReport.value = null;
}

function onSecurityCancel(): void {
  securityReport.value = null;
}
</script>

<template>
  <div class="flex min-w-0 flex-col h-full">
    <ChatHeader
      :side-panel-open="sidePanelOpen"
      @toggle-sidebar="emit('toggle-sidebar')"
      @toggle-side-panel="emit('toggle-side-panel')"
    />

    <div class="border-b border-border px-6 pt-6 pb-4">
      <h2 class="font-sans text-[20px] font-semibold text-text">{{ t('sidebar.nav.skill') }}</h2>
      <div class="mt-4 flex gap-4">
        <button
          v-for="tab in tabs"
          :key="tab.id"
          type="button"
          :class="[
            'border-b-2 py-2 font-sans text-sm transition-colors',
            activeTab === tab.id
              ? 'border-primary text-primary'
              : 'border-transparent text-text-muted hover:text-text',
          ]"
          :data-testid="`skill-tab-${tab.id}`"
          @click="activeTab = tab.id"
        >
          {{ t(tab.labelKey) }}
        </button>
      </div>
    </div>

    <div class="flex-1 overflow-y-auto p-6">
      <!-- 已安装 tab -->
      <div v-if="activeTab === 'installed'" class="space-y-3">
        <div v-if="loading" class="py-8 text-center font-sans text-xs text-text-muted">
          {{ t('common.loading') }}
        </div>
        <div v-else-if="visibleSkills.length === 0" class="py-8 text-center font-sans text-xs text-text-muted">
          {{ t('skill.empty') }}
        </div>
        <div v-else class="grid grid-cols-1 gap-3 md:grid-cols-2">
          <SkillCard
            v-for="skill in visibleSkills"
            :key="skill.id"
            :skill="skill"
            @toggle="onToggle"
            @details="openDetails"
          />
        </div>
      </div>

      <!-- 市场 tab -->
      <SkillMarketplace v-else-if="activeTab === 'marketplace'" @install="onInstall" />

      <!-- 设置 tab -->
      <SkillSettingsPanel
        v-else
        @toggle="onToggle"
        @details="openDetails"
      />
    </div>

    <!-- 安全报告 modal -->
    <SkillSecurityReportModal
      :open="!!securityReport"
      :report="securityReport"
      @cancel="onSecurityCancel"
      @confirm="onSecurityConfirm"
    />

    <!-- 详情 modal -->
    <SkillDetailsModal
      :open="!!detailsSkill"
      :skill="detailsSkill"
      @close="onDetailsClose"
      @upgrade="onUpgrade"
      @uninstall="onUninstall"
    />

    <!-- refresh 兜底：onMounted 拉取由 useSkills 内部处理；这里暴露按钮让
         用户手动重拉（设置/重装后等 chokidar 推送用）。 -->
    <button
      type="button"
      class="fixed bottom-4 right-4 hidden"
      data-testid="skill-refresh"
      @click="refresh"
    />
  </div>
</template>
