<script setup lang="ts">
/**
 * SkillsView 市场 tab。
 *
 * 本地 SKILL.md 选择器（v0 不接 dialog，弹系统文件选择框走
 * window.darvin.pickAttachments 复用）+ GitHub URL 输入框。
 * 装按钮触发父组件 install 流程。
 */
import { ref } from 'vue';
import { t } from '../../services/i18n';

const emit = defineEmits<{
  install: [source: string];
}>();

const githubUrl = ref('');
const installing = ref(false);

function isValidGitHubUrl(s: string): boolean {
  const v = s.trim();
  if (!v) return false;
  // owner/repo
  if (/^[\w.-]+\/[\w.-]+$/.test(v)) return true;
  // https URL
  if (/^https?:\/\/(www\.)?github\.com\/[\w.-]+\/[\w.-]+\/?$/.test(v)) return true;
  return false;
}

async function onPickLocal(): Promise<void> {
  try {
    const r = await window.darvin.pickAttachments();
    const first = r.attachments[0];
    if (first?.path) {
      installing.value = true;
      try {
        emit('install', first.path);
      } finally {
        installing.value = false;
      }
    }
  } catch {
    // 用户取消选择对话框
  }
}

function onInstallGithub(): void {
  if (!isValidGitHubUrl(githubUrl.value)) {
    // 用 showToast 走 service；但直接调会引入循环 import，
    // 父组件监听 install 事件后再 toast 即可。
    return;
  }
  installing.value = true;
  try {
    emit('install', githubUrl.value.trim());
  } finally {
    installing.value = false;
  }
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex flex-col gap-1">
      <h2 class="font-sans text-base font-medium text-text">{{ t('skill.marketplace.title') }}</h2>
      <p class="font-sans text-xs text-text-muted">{{ t('skill.marketplace.desc') }}</p>
    </div>

    <!-- 本地安装 -->
    <div class="space-y-2 rounded-lg border border-border p-4">
      <h3 class="font-sans text-sm font-medium text-text">{{ t('skill.marketplace.local.title') }}</h3>
      <button
        type="button"
        class="font-sans text-sm text-primary transition-opacity hover:opacity-80 disabled:opacity-50"
        :disabled="installing"
        data-testid="skill-marketplace-local-pick"
        @click="onPickLocal"
      >
        {{ t('skill.marketplace.local.pick') }}
      </button>
    </div>

    <!-- GitHub 安装 -->
    <div class="space-y-2 rounded-lg border border-border p-4">
      <h3 class="font-sans text-sm font-medium text-text">{{ t('skill.marketplace.github.title') }}</h3>
      <input
        v-model="githubUrl"
        type="text"
        class="w-full rounded-md border border-border bg-bg px-3 py-1.5 font-sans text-sm text-text outline-none placeholder:text-text-muted focus:border-primary"
        :placeholder="t('skill.marketplace.github.placeholder')"
        data-testid="skill-marketplace-github-input"
      />
      <button
        type="button"
        class="font-sans text-sm text-primary transition-opacity hover:opacity-80 disabled:opacity-50"
        :disabled="!githubUrl || installing"
        data-testid="skill-marketplace-github-install"
        @click="onInstallGithub"
      >
        {{ installing ? t('skill.marketplace.installing') : t('skill.marketplace.install') }}
      </button>
    </div>
  </div>
</template>
