<template>
  <section class="flex flex-col gap-4">
    <div class="flex flex-col gap-2">
      <h3 class="font-sans text-[15px] font-semibold text-text">{{ t('settings.about.title') }}</h3>
      <p class="font-sans text-[12.5px] text-text-muted">{{ t('settings.about.desc') }}</p>
    </div>

    <div class="flex flex-col gap-1.5 rounded-md border border-border bg-surface px-3 py-2.5">
      <div class="flex items-center justify-between">
        <span class="font-sans text-[12.5px] text-text-muted">{{ t('settings.about.version') }}</span>
        <span class="font-mono text-[12.5px] text-text">v0.1.0</span>
      </div>
      <div class="flex items-center justify-between">
        <span class="font-sans text-[12.5px] text-text-muted">{{ t('settings.about.runtime') }}</span>
        <span class="font-mono text-[12.5px] text-text">darvin-agent (Go)</span>
      </div>
      <div class="flex items-center justify-between">
        <span class="font-sans text-[12.5px] text-text-muted">{{ t('settings.about.electron') }}</span>
        <span class="font-mono text-[12.5px] text-text">43.2.0</span>
      </div>
    </div>

    <div class="flex flex-col gap-1.5">
      <h4 class="font-sans text-[13px] font-medium text-text">{{ t('settings.about.arch_title') }}</h4>
      <p class="font-sans text-[12.5px] leading-[1.6] text-text-muted">
        Electron 渲染层（Vue 3 + Tailwind v4）通过 contextBridge 调 Go agent 子进程，
        Go 端负责 agent 循环、工具调用、记忆、上下文压缩、模型切换等业务逻辑。
      </p>
      <a class="font-mono text-[12px] text-primary hover:underline" href="#" @click.prevent>
        docs/系统架构.md →
      </a>
    </div>

    <div class="flex flex-col gap-1.5">
      <h4 class="font-sans text-[13px] font-medium text-text">{{ t('settings.about.licenses_title') }}</h4>
      <ul class="flex flex-col gap-0.5 font-sans text-[12.5px] text-text-muted">
        <li>Vue 3 — MIT</li>
        <li>Tailwind CSS v4 — MIT</li>
        <li>Electron — MIT</li>
        <li>darvin-agent — MIT</li>
      </ul>
    </div>
  </section>
</template>

<script setup lang="ts">
import { t } from '../../services/i18n';
</script>