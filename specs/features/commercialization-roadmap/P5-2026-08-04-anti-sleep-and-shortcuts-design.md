# Anti-Sleep & Shortcuts 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 问题

商业化场景：

- 长任务跑中 OS 自动 sleep → IPC 断流
- 用户依赖全局快捷键（Cmd+Shift+D 唤起）

darvin-cowork 当前未处理。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 进程防休眠句柄（macOS `IOPMAssertion` / Win `SetThreadExecutionState`） | API |
| G2 | 用户级开关（自动 vs 永久） | settings |
| G3 | 全局快捷键注册（Cmd+Shift+D 等） | globalShortcut |
| G4 | 快捷键冲突检测 + UI 提示 | conflict |
| G5 | ≥ 10 测试场景 | tests |

### 1.3 非目标

- 不做键盘热重映射。
- 不做鼠标宏。
- 不做 multipart shortcuts。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `src/main/runtime/manager.ts` | 进程托管 |
| `src/main/index.ts` | application menu / globalShortcut |

## 3. 用户/系统场景

### 场景 1：防休眠 30min

**Given** 用户启动长任务
**When** runtime 检测长任务
**Then** 创建防休眠句柄；任务结束 / 超时 30min 自动释放

### 场景 2：永久防休眠

**Given** 用户设置「永远不进入休眠」
**When** app 运行
**Then** 持续持有防休眠句柄；退出时释放

### 场景 3：全局快捷键

**Given** 用户注册 Cmd+Shift+D
**When** 系统级触发
**Then** darvin 唤起；UI 切到默认 session

### 场景 4：快捷键冲突

**Given** 其他 app 占用 Cmd+Shift+D
**When** darvin 注册
**Then** 注册失败 + UI 红字提示「快捷键已被占用」

## 4. 功能需求

### FR-1 防休眠句柄

```ts
// src/main/libs/anti-sleep.ts
export class AntiSleep {
  acquire(): void
  release(): void
  setPermanent(yes: boolean): void
}
```

实现：

- macOS：`IOPMAssertionCreateWithName` + `kIOPMAssertionTypePreventUserIdleSystemSleep`
- Windows：`SetThreadExecutionState(ES_CONTINUOUS | ES_SYSTEM_REQUIRED)`
- Linux：`org.freedesktop.ScreenSaver` Inhibit `wakelock`

### FR-2 用户级开关

`userSettings.antiSleep.mode` ∈ `auto / permanent / off`。

### FR-3 长任务自动

```ts
useAntiSleep().watchLongTask()
```

task start 时 acquire，end / cancel / 30min timeout release。

### FR-4 全局快捷键

```ts
import { globalShortcut } from 'electron'
globalShortcut.register('CommandOrControl+Shift+D', () => showDarvin())
```

### FR-5 冲突检测

```ts
function isOccupied(accel: string): boolean
```

通过 `globalShortcut.register` 返回值（false = 占用）。

### FR-6 UI 设置页

`SettingsPanelShortcuts.vue`：列表 + 编辑。

### FR-7 ≥ 10 测试场景

| # | 场景 |
|---|---|
| T1 | acquire release 对称 |
| T2 | permanent 模式 |
| T3 | auto 长任务 |
| T4 | 30min 超时释放 |
| T5 | 跨平台兼容（mock） |
| T6 | Cmd+Shift+D 唤起 |
| T7 | 冲突检测 |
| T8 | 重新注册 |
| T9 | UI 编辑 |
| T10 | 系统 sleep 触发 idle 事件 |
| T11 | 异常释放兜底 |

## 5. 安全与隐私

- 防休眠句柄不滥用；仅 long-task 时短暂持有。
- 快捷键放在 darvin 命名空间，不动其他 app 占用。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| 系统 sleep 已经发生 | 重连 supervisor |
| 句柄内核拒绝 | UI 提示「无法阻止系统休眠」 |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/main/libs/anti-sleep.ts`（新） | cross-platform |
| `src/main/libs/shortcut.ts`（新） | registration |
| `src/main/libs/user-settings.ts` | 字段 |
| `src/main/index.ts` | globalShortcut 接入 |
| `src/renderer/components/settings/SettingsPanelShortcuts.vue`（新） | UI |
| `src/renderer/services/i18n.ts` | key |

## 8. 实施顺序与依赖

1. `anti-sleep.ts`
2. `shortcut.ts`
3. UI

> 前置：`runtime-supervision`。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go/TS 单测 ≥ 10 条 |
| V3 | `npm run smoke -- anti-sleep-and-shortcuts` |
| V4 | dev 手工：长任务 + 关闭屏幕 5min 验证 |
| V5 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V6 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- 桌面 widget（v2）。
- 多全局快捷键编排（v2）。
