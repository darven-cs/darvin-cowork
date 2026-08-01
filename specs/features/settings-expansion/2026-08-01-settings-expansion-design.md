# 设置面板广度扩展

> 编号 **07**。把当前 5 个 tab（account / appearance / shortcuts / models / about）扩到 ~10 个 tab，对齐 LobsterAI 覆盖面。

## 1. 背景

`src/renderer/components/settings/*` 现状 5 tab，3 个有占位（account / appearance / models）。LobsterAI 12 tab：通用 / 外观 / 模型 / Agent 引擎 / 记忆 / 梦境 / 浏览器 / 快捷键 / IM / Email / 插件 / 关于。

darvin-cowork 没有 IM / 多 Agent 引擎 / dreaming 概念，本 spec 只扩与现有架构相关的 tab。

## 2. 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 新增 tab：**通用**（autoLaunch / SQLite backup / 通知 / 代理） | 落地至少 2 个开关 |
| G2 | **外观** tab 升级：UI 字号 11-16 / 代码字号 8-24 / 主题色 3 选 1 | UI/代码字号与主题色通过 `styles/theme.css` 的 `@theme` token 驱动并持久化 |
| G3 | **模型** tab：多 provider 列表（Anthropic / OpenAI / 自定义） | 当前硬编码 anthropic 要松绑 |
| G4 | **快捷键** tab：从 06-sidebar 同步实际绑定 | 真绑定 |
| G5 | **记忆** tab：memory 启用 + embedding provider 配置 | 与 Go agent 协议对接 |
| G6 | **关于** tab：版本 / 架构 / 压缩次数（来自 04）/ 导出日志 | 与 04 联动 |
| G7 | tab 切换走 query string（`?tab=models`）便于深链 | `useRoute` 模式 |

## 3. 非目标

- 不做 IM tab（darvin-cowork 不在范围）
- 不做梦境 tab（OpenClaw 专属功能）
- 不做插件市场（darvin-cowork 不在范围）
- 不做账号 / 登出流程（当前仅本地）

## 4. 设计要点

### 4.1 tab 注册表

```ts
export const SettingsSectionId = {
  General: 'general',
  Appearance: 'appearance',
  Shortcuts: 'shortcuts',
  Models: 'models',
  Memory: 'memory',
  Runtime: 'runtime',  // 引擎状态
  About: 'about',
} as const;
export type SettingsSectionId = typeof SettingsSectionId[keyof typeof SettingsSectionId];
```

### 4.2 字号可调持久化

```ts
// user-settings.ts
export interface UserSettings {
  theme: 'light' | 'dark' | 'system';
  language: 'zh' | 'en';
  uiFontSize: number;  // 11-16
  codeFontSize: number;  // 8-24
  accentColor: 'orange' | 'blue' | 'green';
  autoLaunch: boolean;
  notifications: boolean;
  proxy?: string;
  // ...
}
```

### 4.3 Provider 列表

```ts
export const ModelProvider = {
  Anthropic: 'anthropic',
  OpenAI: 'openai',
  Custom: 'custom',
} as const;
```

每个 provider 一组字段：apiKey / baseUrl / defaultModel。

## 5. 用户场景

### 场景 1：调 UI 字号

**Given** 用户在 appearance tab

**When** 拖动 UI 字号滑块 14 → 16

**Then** 全局 `<html>` 的 `--text-base` 立即更新；持久化

### 场景 2：加 OpenAI provider

**Given** models tab

**When** 点「新增 provider」选 OpenAI，填 API key + base URL + model

**Then** main 写入 yaml；重启 Go agent；下次 prompt 走 OpenAI

### 场景 3：看压缩历史

**Given** about tab

**When** 渲染

**Then** 显示「上下文压缩次数：3」「最近压缩：2026-08-01 14:32」

## 6. 验收

- [ ] 7 个 tab 全部有内容（不再是空态）
- [ ] 字号滑块 / 主题色 radio 实时生效
- [ ] 模型 tab 支持至少 2 个 provider
- [ ] 快捷键 tab 显示实际生效的快捷键（与 06 同步）
- [ ] 关于 tab 显示压缩次数
- [ ] tab 切换支持深链

## 7. 依赖

- **前置**：04-context-compaction-ui（关于 tab 用压缩次数）
- **可并行**：06 / 08
- **后置**：无

## 8. 参考

### darvin-cowork
- `src/renderer/components/settings/SettingsSubNav.vue` — tab 注册
- `src/renderer/components/settings/SettingsPanelAppearance.vue` — 主题 + 语言
- `src/renderer/components/settings/SettingsPanelModels.vue` — provider
- `src/renderer/components/settings/SettingsPanelAbout.vue` — 版本
- `src/renderer/views/SettingsView.vue`
- `src/main/libs/user-settings.ts`

### LobsterAI（借鉴）

> 参考项目根目录：`~/桌面/github-project/LobsterAI`（下述路径均相对该项目根）。组件实现遇阻时直接查该项目源码。

- `src/renderer/components/Settings.tsx`（242KB，只看 tab 入口与面板映射）
- `src/renderer/components/Settings/ModelSettingsSection.tsx`（119KB）
- `src/renderer/components/Settings/DreamingSettingsSection.tsx`
- `src/renderer/components/Settings/IMSettings.tsx`（161KB，本 spec 不实现）

## 9. 关联调研

`specs/features/agent-output-ux-research/2026-08-01-cowork-vs-lobsterai-comparison.md` § 2.5「设置面板」
