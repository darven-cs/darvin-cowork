# Sub-spec 33 — Skills Renderer View

> **父 spec**：[`2026-08-02-skills-and-mcp-modules-design.md`](./2026-08-02-skills-and-mcp-modules-design.md)
>
> **本 spec 范围**：`SkillsView.vue` + composables + service + 4 个子组件 + i18n。**不包含** Go 端 / main 端逻辑（spec 31 / 32）。
>
> 创建日期：2026-08-02
> 状态：⏳ 待启动
> 前置：[spec 32 skills-ipc-and-bootstrap](./2026-08-02-skills-ipc-and-bootstrap.md)

---

## 1. 概述

### 1.1 问题 / 背景

侧栏 `技能` nav 当前跳 `PlaceholderView` 空态。本 spec 把 `SkillsView` 实现为可用 UI，参考 LobsterAI 的三 tab 结构（已安装 / 市场 / 设置），但大幅简化（v0 不做 marketplace 远端拉取，只支持本地文件 / GitHub URL 安装）。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 三个 tab 切换：已安装 / 市场 / 设置 | UI live 验证 |
| G2 | 已安装 tab 显示 skill 卡片列表（name / description / version / enabled 开关 / [详情]） | live 演示 |
| G3 | 市场 tab 支持本地 SKILL.md 安装（文件选择器） + GitHub URL 安装 | live 演示 |
| G4 | medium 风险技能弹安全报告 modal | live 演示 |
| G5 | 设置 tab 显示 bundled skill 列表 + 启用 / 禁用开关 | live 演示 |
| G6 | i18n 30+ key 齐全（zh + en） | `assertSameKeys` 通过 |
| G7 | 移除 `SkillsView` 的 PlaceholderView 路由 | `AppShell.vue` 不再有 skills 占位 |

### 1.3 非目标

- 不做 chat `/skill-name` 触发（spec 39）
- 不做 skill 自动升级检测（v0 仅显式按钮触发）
- 不做评分 / 评论
- 不做远程 marketplace 站点
- 不做 skill 详情页（v0 用 modal）

---

## 2. 用户场景

### 场景 1：进入 SkillsView

**Given** 用户从未进过技能 nav
**When** 用户点侧栏 `技能` 图标
**Then**：
- 跳到 `SkillsView`
- 默认 tab：`已安装`
- 看到 5 个 bundled skill 卡片（code-review / api-design / testing / web-search / docx）

### 场景 2：禁用 skill

**Given** `web-search` enabled 状态，toggle 显示「开」
**When** 用户点 toggle → 关
**Then**：
- 卡片 toggle 立刻变关（乐观更新）
- 调 `window.darvin.skills.setEnabled({ skillId: 'web-search', enabled: false })`
- 失败时 toggle 回滚 + toast「更新失败」

### 场景 3：从本地文件安装

**Given** 用户从 GitHub 下载了 `code-review.zip`，解压到 `~/Downloads/code-review/`
**When** 用户切到 `市场` tab，点 [本地安装] → 文件选择器选 `~/Downloads/code-review/SKILL.md`
**Then**：
- 调 `window.darvin.skills.install({ source: '/path/to/SKILL.md' })`（spec 32 落地）
- main 端做安全扫描：
  - safe / low：直接装 → toast「已安装 code-review v1.2.0」
  - medium：弹安全报告 modal，列出 findings
  - high / critical：toast「风险过高，禁止安装」

### 场景 4：从 GitHub 安装

**Given** 用户切到 `市场` tab，输入框填 `owner/repo`（或完整 URL）
**When** 点 [安装]
**Then** main 端下载 GitHub archive → 解压 → 走场景 3 的安全扫描流程

### 场景 5：升级 skill

**Given** 用户装了一个 GitHub skill v1.0.0；仓库推到 v1.1.0
**When** 用户在 skill 卡片 [详情] modal 里点 [升级]
**Then** main 端下载新版本 + 替换（保留 .env / .meta.json）

### 场景 6：卸载 user skill

**Given** 用户装了 `foo` skill（非 bundled）
**When** 在卡片 [详情] modal 点 [卸载]
**Then** 二次确认 → 调 `window.darvin.skills.uninstall` → fs watcher 触发 reload → 卡片消失

### 场景 7：bundled skill 设置

**Given** 用户切到 `设置` tab
**When** 用户禁用 `docx`（bundled）
**Then**：
- 切换 toggle → 同场景 2
- bundled skill 不允许卸载（按钮 disabled）

### 场景 8：安全报告 modal

**Given** 用户安装的 skill 评分 medium，触发 modal
**When** modal 显示 3 个 finding：1 warning + 2 danger
**Then**：
- 标题「安全报告：code-review v1.0.0」
- 列表每条：dimension icon + severity color + message + file:line
- 底部：[取消] [仍然安装]

---

## 3. 功能需求

### FR-1: 共享 API 增量

```typescript
// src/shared/darvin-api.ts
export interface DarvinApi {
  // ... spec 32 已加 skills
  installSkill(req: { source: string }): Promise<{
    skill: DarvinSkillSummary;
    riskLevel: 'safe' | 'low' | 'medium' | 'high' | 'critical';
  }>;
  uninstallSkill(req: { skillId: string }): Promise<{ ok: boolean }>;
  upgradeSkill(req: { skillId: string }): Promise<{ skill: DarvinSkillSummary }>;
  getSkillDetails(req: { skillId: string }): Promise<{
    skill: DarvinSkillSummary;
    body: string;            // SKILL.md markdown body
    scripts?: Array<{ path: string; content: string }>;
  }>;
}
```

### FR-2: composable

```typescript
// src/renderer/composables/useSkills.ts
export function useSkills() {
  const skills = ref<DarvinSkillSummary[]>([]);
  const loading = ref(false);
  const error = ref<string | null>(null);

  async function refresh(): Promise<void> {
    loading.value = true;
    try {
      const r = await window.darvin.skills.list();
      skills.value = r.skills;
    } catch (e) {
      error.value = (e as Error).message;
    } finally {
      loading.value = false;
    }
  }

  async function setEnabled(skillId: string, enabled: boolean): Promise<void> {
    // 乐观更新
    const idx = skills.value.findIndex(s => s.id === skillId);
    if (idx >= 0) skills.value[idx] = { ...skills.value[idx], enabled };

    try {
      await window.darvin.skills.setEnabled({ skillId, enabled });
    } catch (e) {
      // 回滚
      if (idx >= 0) skills.value[idx] = { ...skills.value[idx], enabled: !enabled };
      throw e;
    }
  }

  async function install(source: string): Promise<{
    skill: DarvinSkillSummary;
    riskLevel: string;
  }> {
    return window.darvin.skills.install({ source });
  }

  async function uninstall(skillId: string): Promise<void> {
    await window.darvin.skills.uninstall({ skillId });
    skills.value = skills.value.filter(s => s.id !== skillId);
  }

  async function upgrade(skillId: string): Promise<void> {
    const r = await window.darvin.skills.upgrade({ skillId });
    const idx = skills.value.findIndex(s => s.id === skillId);
    if (idx >= 0) skills.value[idx] = r.skill;
  }

  // 订阅变化
  onMounted(() => {
    refresh();
    const unsubscribe = window.darvin.skills.onChanged((next) => {
      skills.value = next;
    });
    onUnmounted(() => unsubscribe());
  });

  return {
    skills,
    loading,
    error,
    refresh,
    setEnabled,
    install,
    uninstall,
    upgrade,
    // 派生
    installed: computed(() => skills.value.filter(s => s.isBuiltIn)),
    userSkills: computed(() => skills.value.filter(s => !s.isBuiltIn)),
    bundled: computed(() => skills.value.filter(s => s.isOfficial)),
  };
}
```

### FR-3: SkillCard 组件

```vue
<!-- src/renderer/components/skills/SkillCard.vue -->
<template>
  <div class="rounded-lg border border-border bg-surface p-4 flex flex-col gap-3">
    <div class="flex items-start justify-between">
      <div class="flex items-center gap-2">
        <Icon :name="icon" :size="18" />
        <h3 class="text-sm font-medium">{{ skill.name }}</h3>
        <span v-if="skill.isBuiltIn" class="text-[11px] px-1.5 py-0.5 rounded bg-primary-muted text-primary">
          {{ t('skill.badge.builtin') }}
        </span>
        <span v-if="skill.riskLevel && skill.riskLevel !== 'safe'"
              :class="['text-[11px] px-1.5 py-0.5 rounded', riskBadgeClass]">
          {{ t(`skill.risk.${skill.riskLevel}`) }}
        </span>
      </div>
      <Switch :checked="skill.enabled" @change="onToggle" />
    </div>
    <p class="text-xs text-text-muted line-clamp-2">{{ skill.description }}</p>
    <div class="flex items-center justify-between">
      <span class="text-[11px] text-text-subtle">v{{ skill.version || '0.0.0' }}</span>
      <button class="text-xs text-primary hover:underline" @click="emit('details', skill)">
        {{ t('skill.card.details') }}
      </button>
    </div>
  </div>
</template>
```

### FR-4: SkillSecurityReportModal

```vue
<!-- src/renderer/components/skills/SkillSecurityReportModal.vue -->
<template>
  <Modal :open="open" :title="t('skill.security.title', { name: report?.skillName, version: report?.version })" @close="emit('cancel')">
    <div class="space-y-3">
      <div class="flex items-center gap-2">
        <Icon :name="riskIcon" :size="20" :class="riskColorClass" />
        <span class="text-sm font-medium">{{ t(`skill.risk.${report?.riskLevel}`) }}</span>
        <span class="text-xs text-text-subtle">{{ t('skill.security.score', { score: report?.score }) }}</span>
      </div>

      <div class="border-t border-border pt-3 space-y-2">
        <div v-for="(f, i) in report?.findings" :key="i"
             class="flex items-start gap-2 text-xs">
          <span :class="['inline-block w-1.5 h-1.5 rounded-full mt-1.5 shrink-0', severityDotClass(f.severity)]" />
          <div class="flex-1">
            <div class="font-mono text-text-subtle">{{ f.file }}:{{ f.line }}</div>
            <div class="text-text">{{ f.message }}</div>
          </div>
        </div>
      </div>
    </div>
    <template #footer>
      <button class="px-3 py-1.5 text-sm text-text-muted hover:text-text" @click="emit('cancel')">
        {{ t('common.cancel') }}
      </button>
      <button class="px-3 py-1.5 text-sm bg-primary text-white rounded hover:bg-primary-hover" @click="emit('confirm')">
        {{ t('skill.security.confirm') }}
      </button>
    </template>
  </Modal>
</template>
```

### FR-5: SkillMarketplace 组件

```vue
<!-- src/renderer/components/skills/SkillMarketplace.vue -->
<template>
  <div class="space-y-4">
    <div class="flex flex-col gap-2">
      <h2 class="text-base font-medium">{{ t('skill.marketplace.title') }}</h2>
      <p class="text-xs text-text-muted">{{ t('skill.marketplace.desc') }}</p>
    </div>

    <!-- 本地安装 -->
    <div class="rounded-lg border border-border p-4 space-y-2">
      <h3 class="text-sm font-medium">{{ t('skill.marketplace.local.title') }}</h3>
      <button class="text-sm text-primary hover:underline" @click="onPickLocal">
        {{ t('skill.marketplace.local.pick') }}
      </button>
    </div>

    <!-- GitHub 安装 -->
    <div class="rounded-lg border border-border p-4 space-y-2">
      <h3 class="text-sm font-medium">{{ t('skill.marketplace.github.title') }}</h3>
      <input v-model="githubUrl"
             class="w-full px-3 py-1.5 text-sm border border-border rounded"
             :placeholder="t('skill.marketplace.github.placeholder')" />
      <button class="text-sm text-primary hover:underline disabled:opacity-50"
              :disabled="!githubUrl || installing"
              @click="onInstallGithub">
        {{ installing ? t('skill.marketplace.installing') : t('skill.marketplace.install') }}
      </button>
    </div>
  </div>
</template>
```

### FR-6: SkillsView 主视图

```vue
<!-- src/renderer/views/SkillsView.vue -->
<template>
  <div class="flex h-full flex-col">
    <ChatHeader :title="t('sidebar.nav.skill')" @toggle-sidebar="..." />

    <!-- tab 切换 -->
    <div class="border-b border-border px-4 flex gap-4">
      <button v-for="t in tabs" :key="t.id"
              :class="['py-2 text-sm', activeTab === t.id ? 'text-primary border-b-2 border-primary' : 'text-text-muted']"
              @click="activeTab = t.id">
        {{ t.label }}
      </button>
    </div>

    <div class="flex-1 overflow-y-auto p-4">
      <!-- 已安装 tab -->
      <div v-if="activeTab === 'installed'" class="space-y-3">
        <div v-if="loading" class="text-center text-text-muted py-8">{{ t('common.loading') }}</div>
        <div v-else-if="!skills.length" class="text-center text-text-muted py-8">
          {{ t('skill.empty') }}
        </div>
        <div v-else class="grid grid-cols-1 md:grid-cols-2 gap-3">
          <SkillCard v-for="skill in skills" :key="skill.id"
                     :skill="skill"
                     @details="openDetails"
                     @toggle="onToggle" />
        </div>
      </div>

      <!-- 市场 tab -->
      <SkillMarketplace v-else-if="activeTab === 'marketplace'"
                        @install="onInstall" />

      <!-- 设置 tab -->
      <div v-else-if="activeTab === 'settings'" class="space-y-3">
        <SkillSettingsPanel />
      </div>
    </div>

    <!-- 安全报告 modal -->
    <SkillSecurityReportModal v-if="securityReport"
                              :open="!!securityReport"
                              :report="securityReport"
                              @cancel="securityReport = null"
                              @confirm="onSecurityConfirm" />

    <!-- 详情 modal -->
    <SkillDetailsModal v-if="detailsSkill"
                       :skill="detailsSkill"
                       @close="detailsSkill = null"
                       @upgrade="onUpgrade"
                       @uninstall="onUninstall" />
  </div>
</template>
```

### FR-7: 路由更新（`src/renderer/layout/AppShell.vue`）

```typescript
// 把 PLACEHOLDERS.skill 改成走 SkillsView
// 或者：直接新增 case 'skills': return () => h(SkillsView)
```

### FR-8: i18n 新增 key（~30 个）

| Key | 中文 | 英文 |
|-----|------|------|
| `skill.tab.installed` | 已安装 | Installed |
| `skill.tab.marketplace` | 市场 | Marketplace |
| `skill.tab.settings` | 设置 | Settings |
| `skill.empty` | 还没有安装任何技能 | No skills installed |
| `skill.badge.builtin` | 内置 | Built-in |
| `skill.risk.safe` | 安全 | Safe |
| `skill.risk.low` | 低风险 | Low risk |
| `skill.risk.medium` | 中风险 | Medium risk |
| `skill.risk.high` | 高风险 | High risk |
| `skill.risk.critical` | 严重风险 | Critical risk |
| `skill.card.details` | 详情 | Details |
| `skill.card.upgrade` | 升级 | Upgrade |
| `skill.card.uninstall` | 卸载 | Uninstall |
| `skill.marketplace.title` | 安装新技能 | Install New Skill |
| `skill.marketplace.desc` | 从本地 SKILL.md 或 GitHub 仓库安装技能 | Install skills from local SKILL.md or GitHub repo |
| `skill.marketplace.local.title` | 从本地文件 | From Local File |
| `skill.marketplace.local.pick` | 选择 SKILL.md 文件… | Pick SKILL.md… |
| `skill.marketplace.github.title` | 从 GitHub | From GitHub |
| `skill.marketplace.github.placeholder` | owner/repo 或 https://github.com/owner/repo | owner/repo or https://github.com/owner/repo |
| `skill.marketplace.install` | 安装 | Install |
| `skill.marketplace.installing` | 安装中… | Installing… |
| `skill.install.success` | 已安装 {name} | Installed {name} |
| `skill.install.failed` | 安装失败：{error} | Install failed: {error} |
| `skill.install.high_risk_blocked` | 风险过高，禁止安装 | Risk too high, installation blocked |
| `skill.upgrade.success` | 已升级 {name} 到 v{version} | Upgraded {name} to v{version} |
| `skill.upgrade.failed` | 升级失败：{error} | Upgrade failed: {error} |
| `skill.uninstall.confirm` | 确认卸载 {name}？ | Confirm uninstall {name}? |
| `skill.uninstall.success` | 已卸载 {name} | Uninstalled {name} |
| `skill.security.title` | 安全报告：{name} v{version} | Security Report: {name} v{version} |
| `skill.security.score` | 风险分：{score}/100 | Risk score: {score}/100 |
| `skill.security.confirm` | 仍然安装 | Install anyway |
| `skill.toggle.failed` | 更新失败，请重试 | Update failed, please retry |

---

## 4. 实现方案

### 4.1 文件清单

```
src/renderer/
├── views/
│   └── SkillsView.vue                 🆕
├── composables/
│   ├── useSkills.ts                   🆕
│   └── useSkills.test.ts              🆕
├── services/
│   ├── skillService.ts                🆕
│   └── skillService.test.ts           🆕
├── components/skills/
│   ├── SkillCard.vue                  🆕
│   ├── SkillCard.test.ts              🆕
│   ├── SkillMarketplace.vue           🆕
│   ├── SkillSecurityReportModal.vue   🆕
│   ├── SkillSettingsPanel.vue         🆕
│   ├── SkillDetailsModal.vue          🆕
│   └── index.ts                       🆕 export
├── layout/
│   └── AppShell.vue                   移除 skills 的 PlaceholderView 路由
├── services/
│   └── i18n.ts                        +30 key
└── assets/icons/
    ├── plugin.svg                     🆕
    ├── shield.svg                     🆕
    └── trash.svg                      🆕
```

### 4.2 关键代码片段

#### 4.2.1 useSkills（见 FR-2）

#### 4.2.2 SkillsView（见 FR-6）

#### 4.2.3 安全报告 modal（见 FR-4）

### 4.3 测试策略

| 测试 | 覆盖 |
|------|------|
| `useSkills.test.ts` | refresh / setEnabled 乐观更新 / 失败回滚 / 订阅 onChanged |
| `SkillCard.test.ts` | enabled switch / details 按钮 / risk 徽章 |
| `skillService.test.ts` | install / uninstall / upgrade 调 window.darvin.skills.* |

---

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| SkillsView 没装任何 skill | 空态卡片「还没有安装任何技能」+ 引导到市场 tab |
| 用户点击已禁用 skill 的 `启用` 按钮 | 调 setEnabled(true) |
| 安全扫描 0 finding | score=0, level=safe，直接装不弹 modal |
| GitHub URL 格式错 | toast「URL 格式不正确」 |
| GitHub 仓库不存在 | toast「下载失败：404」 |
| SKILL.md 不含 frontmatter | main 端拒绝（spec 32 报错） |
| 用户安装同名 skill 覆盖 bundled | 提示「已覆盖内置技能 X」 |
| 卸载 bundled skill | 按钮 disabled + tooltip「内置技能不能卸载」 |
| 详情 modal 中点「升级」但已是最新版本 | main 端报错 toast「已是最新版本」 |

---

## 6. 涉及文件

| 文件 | 变更 |
|------|------|
| `src/renderer/views/SkillsView.vue` | 🆕 |
| `src/renderer/composables/useSkills.ts` | 🆕 |
| `src/renderer/composables/useSkills.test.ts` | 🆕 |
| `src/renderer/services/skillService.ts` | 🆕 |
| `src/renderer/components/skills/SkillCard.vue` | 🆕 |
| `src/renderer/components/skills/SkillMarketplace.vue` | 🆕 |
| `src/renderer/components/skills/SkillSecurityReportModal.vue` | 🆕 |
| `src/renderer/components/skills/SkillSettingsPanel.vue` | 🆕 |
| `src/renderer/components/skills/SkillDetailsModal.vue` | 🆕 |
| `src/renderer/components/skills/index.ts` | 🆕 |
| `src/renderer/components/skills/*.test.ts` | 🆕 |
| `src/renderer/layout/AppShell.vue` | 移除 skills 的 PlaceholderView 路由 |
| `src/renderer/services/i18n.ts` | +30 key |
| `src/renderer/assets/icons/plugin.svg` | 🆕 |
| `src/renderer/assets/icons/shield.svg` | 🆕 |
| `src/renderer/assets/icons/trash.svg` | 🆕 |
| `src/shared/darvin-api.ts` | +`installSkill` / `uninstallSkill` / `upgradeSkill` / `getSkillDetails` |

---

## 7. 验收标准

**通用**：
- [ ] `npm run lint` + `npm run test` 通过
- [ ] `npm run build` 成功
- [ ] i18n `assertSameKeys(dictZh, dictEn)` 通过

**FR-1 API 增量**：
- [ ] `installSkill` / `uninstallSkill` / `upgradeSkill` / `getSkillDetails` 类型齐全

**FR-2 useSkills**：
- [ ] `refresh()` 调 `window.darvin.skills.list()`
- [ ] `setEnabled` 乐观更新 + 失败回滚
- [ ] 订阅 `onChanged` 同步更新

**FR-3 SkillCard**：
- [ ] 显示 name / description / version / switch / [详情]
- [ ] isBuiltIn 显示徽章
- [ ] risk !== safe 显示风险徽章

**FR-4 SecurityReportModal**：
- [ ] 显示 risk level + score + findings 列表
- [ ] [取消] [仍然安装] 按钮

**FR-5 Marketplace**：
- [ ] 本地文件选择器触发
- [ ] GitHub URL 输入 + 安装按钮

**FR-6 SkillsView**：
- [ ] 三个 tab 切换
- [ ] 空态卡片
- [ ] loading 状态

**FR-7 路由**：
- [ ] `AppShell.vue` 不再有 skills 占位
- [ ] `Sidebar` 跳到 `SkillsView`

**FR-8 i18n**：
- [ ] 30+ key 齐全（zh + en）
- [ ] 缺 key dev warn 触发

**live 验证**：
- 侧栏 → 技能 → 看到 5 个 bundled skill
- toggle 切 web-search → ≤500ms 状态变化
- 市场 tab → 本地装一个 SKILL.md → 安全报告 modal（如 medium）
- 详情 modal → 升级（如 GitHub skill）→ toast 提示
- 详情 modal → 卸载 → 卡片消失

---

## 8. 与其他 spec 的关系

**前置**：spec 31 + 32

**下游**：
- spec 38 改造 `tool.Registry` 让 agent 实际能调用 skill
- spec 39 `/skill-name` chat 内触发

**并行**：spec 34 / 35 / 36 / 37（MCP）

---

## 9. 状态变更日志

- 2026-08-02 · 完成 spec 设计；待用户确认后启动实现