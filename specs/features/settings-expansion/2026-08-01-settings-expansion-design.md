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

- [x] 7 个 tab 全部有内容（general / appearance / shortcuts / models / memory / runtime / about）
- [x] 字号滑块 / 主题色 radio 实时生效
- [x] 模型 tab 支持至少 2 个 provider
- [x] 快捷键 tab 显示实际生效的快捷键（与 06 同步）
- [x] 关于 tab 显示压缩次数
- [x] tab 切换支持深链

### 落地补充（实现期决议）

- **tab 注册表抽独立模块**：`settings-sections.ts` 导出 `SettingsSections` 常量数组 + `SettingsSectionId` 类型 + `isSettingsSectionId` 校验（`<script setup>` 不能导出运行时函数）；`SettingsSubNav` / `SettingsView` 共用。**account tab 移除**（spec 非目标：不做账号/登出，7 tab 对齐设计 doc §4.1 注册表）。
- **G1 通用**：autoLaunch 走真实 OS 开关（`app.getLoginItemSettings()` / `setLoginItemSettings({ openAtLogin })`），notifications / proxy 持久化到用户 yaml `app` 块。新 IPC `get_app_preferences` / `set_app_preferences`。
- **G2 外观**：新建 `useAppearance`（localStorage 持久化），按 LobsterAI `applyTypographyPreferences` 同款——`uiFontSize/14` 比例整体缩放 `--text-xs..2xl`（body + markdown 消费），code 字号单独写 `--text-code`（新增 `@theme` token，CodeBlock 的 `text-[13px]` 改 `text-code`），主题色写 `html[data-accent]` → theme.css 里 `html[data-accent=blue|green]` 覆盖 `--color-primary` 家族（放 dark 块后，light/dark 都生效）。
- **G3 模型**：`DarvinLLMConfig` 扩展 `provider` / `activeProvider` / `defaultModel` / `providers` 块。**后端约束**：Go 只注册 anthropic（`llm.NewProvider` 对未知名直接报错 + `main.go` `os.Exit(1)`），所以非 anthropic 保存只写 yaml `providers.<name>` 块、不重启、不激活（避免下次启动 agent 崩溃）；pending note 提示「Go 接入前仍用 Anthropic」。anthropic 保存走原重启路径。`user-settings.ts` yaml 解析/写入扩展 `llm.provider/default_model` + `app.*` + `memory.*` + `providers.*` 两级嵌套。
- **G4 快捷键**：替换虚构的 ⌘N/⌘F/⌘D/⌘,/⌘J，改为真实绑定——`Ctrl/Cmd+1-5`（06 useShortcuts）+ `Enter` 发送 / `Shift+Enter` 换行 / `Ctrl/Cmd+Enter` 强制发送（Composer / PromptDock）+ `Esc` 关浮层（useFloatingPanel）。
- **G5 记忆**：Go 侧 `sections.go` 明确 "no memory system wired"，设置面板落地 + 持久化到 yaml `memory` 块（enabled / embedding_provider / api_key），hint 标注「记忆运行时尚未在 darvin-agent 接入」。
- **G6 关于**：版本走新 IPC `get_app_info`（`app.getVersion()` + `process.versions.electron` + platform/arch），替换硬编码 v0.1.0；压缩次数聚合 `useMessages.compactionsBySessionId`（跨 session 求和 + 最近时间 Intl 格式化）；导出日志 = 复制诊断信息（版本/平台/压缩次数）到剪贴板 + toast。
- **G7 深链**：`SettingsView` onMounted 读 `?tab=` query 校验后设初始 active，tab 切换 `history.replaceState` 更新 query（不刷新）。

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
