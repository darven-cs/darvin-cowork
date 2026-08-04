# Artifact Panel UX — 商业化迭代 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

[`2026-08-02-artifact-panel-ux-design.md`](../artifact-panel-ux/2026-08-02-artifact-panel-ux-design.md) 给出 UX 设计。本文是商业化迭代：键盘流、token budget 视图、跨设备 config 同步。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 键盘流：J/K 切换 artifact；Cmd+E 导出 | shortcut |
| G2 | token budget 视图（按 artifact 类型） | view |
| G3 | panel 折叠持久化 | preference |
| G4 | 跨设备 config 同步（settings.json） | sync |
| G5 | 与 v1 完全兼容 | backward |
| G6 | ≥ 10 测试场景 | tests |

### 1.3 非目标

- 不做 device-to-device 直连同步（仅通过 settings.json 服务器拉取）。
- 不做 3D/AR artifact。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/features/artifact-panel-ux/2026-08-02-...` | v1 |
| `src/renderer/components/side-panel/` | 占位 |
| `src/renderer/services/i18n.ts` | 已有 key |

## 3. 用户/系统场景

### 场景 1：键盘切换

**Given** artifact panel 打开
**When** 用户按 `J`
**Then** 选中下一个 artifact；触发 sandbox 加载

### 场景 2：token budget

**Given** 用户在 settings → artifact
**When** 切换到 budget tab
**Then** 展示按类型聚合的 token / cost 图表

### 场景 3：折叠持久化

**Given** 用户收起 panel
**When** 重启 app
**Then** 仍保持收起

### 场景 4：跨设备

**Given** 用户在 mac 上做配置调整
**When** 同步服务推送更新
**Then** darvin 拉取并应用；不影响本地数据库

## 4. 功能需求

### FR-1 键盘流

| shortcut | 行为 |
|---|---|
| `J` | 选中下一个 |
| `K` | 选中上一个 |
| `Cmd+E` | export |
| `Cmd+W` | close panel |
| `Cmd+1..9` | 切换 tab |

### FR-2 token budget 视图

`useArtifactBudget()`：

```ts
const totals = ref({ html: { tokens: 0, cost: 0 }, mermaid: ..., ... })
```

### FR-3 panel 折叠持久化

`userSettings` 新增字段：`artifactPanel.collapsed: boolean`。

### FR-4 跨设备

通过 `oauth-login` + 配置同步端点（v2 占位）。

### FR-5 v1 兼容

保留原 v1 全部分析与设计原则；UX 不破坏既有快捷键。

### FR-6 ≥ 10 测试场景

| # | 场景 |
|---|---|
| T1 | J/K 切换 |
| T2 | Cmd+E export |
| T3 | Cmd+W close |
| T4 | Cmd+1 tab 切 |
| T5 | token budget 计算 |
| T6 | panel 折叠持久化 |
| T7 | v1 兼容 |
| T8 | i18n 文案 |
| T9 | dark / light token |
| T10 | 跨设备拉取 mock |
| T11 | settings sync 失败回退 |

## 5. 安全与隐私

- 跨设备 sync 通过 TLS + oauth。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| sync 服务 5xx | 本地配置生效；失败 prompt |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/renderer/composables/useArtifactKeyboard.ts`（新） | 快捷键 |
| `src/renderer/composables/useArtifactBudget.ts`（新） | budget |
| `src/main/libs/user-settings.ts` | `artifactPanel.collapsed` 字段 |
| `src/renderer/services/i18n.ts` | 新 key |

## 8. 实施顺序与依赖

1. keyboard 流
2. budget
3. collapse 持久化
4. 跨设备

> 前置：`artifact-panel-ux/v1`。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | TS 单测 ≥ 10 条 |
| V3 | `npm run smoke -- artifact-panel-ux-commercialization` |
| V4 | dev 手工：J/K |
| V5 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V6 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- 完整 UX 重做。
- 跨设备直连（v2）。
